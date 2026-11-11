// Package fileguard はファイル用ガードレールの本体。Generic Guardrail API
// （https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api）で受けた
// リクエストのファイル（images と、Manifold の拡張である files）をテキストに起こし、
// 下流のテキスト用ガードレールへ渡して判定する。
//
//	Manifold ──▶ fileguard ──▶ 下流のテキスト用ガードレール
//	                 └──▶ Extractor（LLM / OCR サービス）
//
// 下流には2回に分けて送る。
//
//   - 元の texts: リクエストをそのまま（images / files を除いて）送り、判定を
//     そのまま返す。書き換え（マスキング）も呼び出し元へ返る。
//   - ファイルから起こしたテキスト: 通すか拒否するかだけを見る。ファイル自体は
//     書き換えられないので、下流が書き換えを返した場合は既定で拒否する。
package fileguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
	"golang.org/x/sync/errgroup"
)

// DefaultMaxChunks は Config.MaxChunks の既定値。
const DefaultMaxChunks = 1000

// APIPath は Generic Guardrail API のエンドポイントのパス。
const APIPath = "/beta/litellm_basic_guardrail_api"

// maxRequestSize は受け付けるリクエストの最大バイト数。
const maxRequestSize = 32 << 20

// 判定（action）。
const (
	actionNone       = "NONE"
	actionIntervened = "GUARDRAIL_INTERVENED"
	actionBlocked    = "BLOCKED"
)

// リクエストの JSON のキー。これ以外のキーは中身を見ずに下流へ渡す。
const (
	keyTexts              = "texts"
	keyImages             = "images"
	keyFiles              = "files"
	keyInputType          = "input_type"
	keyTools              = "tools"
	keyToolCalls          = "tool_calls"
	keyStructuredMessages = "structured_messages"
)

// レスポンスの JSON のキー（texts はリクエストと同じ keyTexts）。
const (
	keyAction        = "action"
	keyBlockedReason = "blocked_reason"
)

// Config は Handler の設定。
type Config struct {
	// Extractor はテキストとして読めないファイルを起こす。
	Extractor extract.Extractor
	// Downstream は下流のテキスト用ガードレール。
	Downstream *Downstream
	// ExtractTimeout はファイル1件の書き起こしのタイムアウト。
	ExtractTimeout time.Duration
	// Concurrency は1リクエストのファイルを並行して起こす数。
	Concurrency int
	// MaxFileBytes はファイル1件の最大バイト数。超えたファイルは拒否する。
	MaxFileBytes int
	// Fetcher は、URL で渡されたファイルを許可したホストから取得する。nil なら
	// URL のファイルは取得しない（検査できないファイルとして扱う）。
	Fetcher *Fetcher
	// AllowUnsupported が true なら、起こせない種類のファイルと、中身を取得できない
	// ファイル（許可していないホストの URL、file_id）を検査せずに通す。
	// false（既定）なら拒否する。
	AllowUnsupported bool
	// AllowIntervened が true なら、起こしたテキストに下流が書き換えを返しても通す。
	// false（既定）なら拒否する（ファイル自体は書き換えられないため）。
	AllowIntervened bool
	// ExtractedInputType は起こしたテキストを下流へ送るときの input_type。
	// 空なら元のリクエストの input_type を使う。
	ExtractedInputType string
	// ChunkChars は起こしたテキストを下流へ送るときの分割の大きさ（文字数）。
	// 0 なら分割しない。
	ChunkChars int
	// MaxChunks は、1リクエストでファイルから起こしたテキストを下流へ送るときの
	// 断片の数の上限（分割しないときは、テキストのあるファイルの数）。超えた
	// リクエストは拒否する。0 以下なら DefaultMaxChunks。
	MaxChunks int
	// ChunkOverlap は隣り合う断片を重ねる文字数。境界で切れた語句を下流が検出
	// できるようにする。ChunkChars の半分まで（超える分は切り詰める）。
	ChunkOverlap int
}

// Handler は Generic Guardrail API を受ける http.Handler。
type Handler struct {
	cfg Config
}

// NewHandler は cfg の Handler を返す。
func NewHandler(cfg Config) *Handler {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	// 重なりは断片の半分まで。それより大きいと断片が1文字ずつしか進まず、分割後の
	// 合計が元のテキストの何倍にも膨らむ（半分までなら2倍以下に収まる）。
	cfg.ChunkOverlap = min(max(cfg.ChunkOverlap, 0), cfg.ChunkChars/2)
	if cfg.MaxChunks <= 0 {
		cfg.MaxChunks = DefaultMaxChunks
	}
	return &Handler{cfg: cfg}
}

// apiFile は files の1件。data は JSON では base64。
type apiFile struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// apiResponse は Generic Guardrail API のレスポンス。
type apiResponse struct {
	Action        string   `json:"action"`
	BlockedReason string   `json:"blocked_reason,omitempty"`
	Texts         []string `json:"texts,omitempty"`
	// passthrough は、下流が GUARDRAIL_INTERVENED で返した texts 以外のフィールド
	// （書き換えた tools・tool_calls・structured_messages など）。呼び出し側が適用
	// できるように、中身を見ずにそのまま返す。
	passthrough map[string]json.RawMessage
}

// MarshalJSON は passthrough のフィールドを加えて JSON にする。
func (r apiResponse) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(r.passthrough)+3)
	for k, v := range r.passthrough {
		out[k] = v
	}
	out[keyAction] = r.Action
	if r.BlockedReason != "" {
		out[keyBlockedReason] = r.BlockedReason
	}
	if len(r.Texts) > 0 {
		out[keyTexts] = r.Texts
	}
	return json.Marshal(out)
}

// extracted はファイル1件を起こした結果。blocked が空でなければ拒否の理由。
type extracted struct {
	text    string
	blocked string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestSize)).
		Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.apply(r.Context(), req)
	if err != nil {
		if bad, ok := errors.AsType[*badRequestError](err); ok {
			writeError(w, http.StatusBadRequest, bad.message)
			return
		}
		// 理由はログにだけ出す。呼び出し元（Manifold）は 200 以外を fail closed で扱う。
		slog.ErrorContext(r.Context(), "file guardrail failed", slog.Any("error", err))
		writeError(w, http.StatusBadGateway, "file guardrail failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// badRequestError はリクエストの形が不正なことを表す。
type badRequestError struct{ message string }

func (e *badRequestError) Error() string { return e.message }

func (h *Handler) apply(ctx context.Context, req map[string]json.RawMessage) (apiResponse, error) {
	files, err := requestFiles(req)
	if err != nil {
		return apiResponse{}, err
	}
	var texts []string
	if raw, ok := req[keyTexts]; ok {
		if err := json.Unmarshal(raw, &texts); err != nil {
			return apiResponse{}, &badRequestError{"texts must be an array of strings"}
		}
	}

	results, err := h.extractAll(ctx, files)
	if err != nil {
		return apiResponse{}, err
	}
	var fileTexts []string
	pieces := 0
	for _, res := range results {
		if res.blocked != "" {
			return apiResponse{Action: actionBlocked, BlockedReason: res.blocked}, nil
		}
		if res.text == "" {
			continue
		}
		// 断片を作る前に数を確かめる。小さい断片に分ける設定だと、小さいファイルからでも
		// 数百万件の断片ができ、メモリと下流へのリクエストが膨らむ。
		pieces += chunkCount(res.text, h.cfg.ChunkChars, h.cfg.ChunkOverlap)
		if pieces > h.cfg.MaxChunks {
			return apiResponse{
				Action:        actionBlocked,
				BlockedReason: "the text of the files is too long for the guardrail check",
			}, nil
		}
		fileTexts = append(fileTexts, chunk(res.text, h.cfg.ChunkChars, h.cfg.ChunkOverlap)...)
	}

	// 元のリクエストと、ファイルから起こしたテキストを、並行して下流へ送る。
	var original, fromFiles apiResponse
	original.Action, fromFiles.Action = actionNone, actionNone
	eg, egCtx := errgroup.WithContext(ctx)
	// テキストが無くても、ツール呼び出しやメッセージがあれば下流はそれを検査するので送る。
	if len(texts) > 0 || hasItems(req[keyToolCalls]) || hasItems(req[keyStructuredMessages]) {
		eg.Go(func() (err error) {
			original, err = h.cfg.Downstream.Apply(egCtx, originalRequest(req), len(texts))
			return err
		})
	}
	if len(fileTexts) > 0 {
		eg.Go(func() (err error) {
			body := extractedRequest(req, fileTexts, h.cfg.ExtractedInputType)
			fromFiles, err = h.cfg.Downstream.Apply(egCtx, body, len(fileTexts))
			return err
		})
	}
	if err := eg.Wait(); err != nil {
		return apiResponse{}, err
	}

	switch {
	case original.Action == actionBlocked:
		return original, nil
	case fromFiles.Action == actionBlocked:
		return apiResponse{Action: actionBlocked, BlockedReason: fromFiles.BlockedReason}, nil
	case fromFiles.Action == actionIntervened && !h.cfg.AllowIntervened:
		return apiResponse{
			Action:        actionBlocked,
			BlockedReason: "the guardrail would rewrite the content of a file, which cannot be rewritten",
		}, nil
	}
	return original, nil
}

// extractAll は files を並行してテキストに起こす。結果は files と同じ順序。
func (h *Handler) extractAll(ctx context.Context, files []inputFile) ([]extracted, error) {
	results := make([]extracted, len(files))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(h.cfg.Concurrency)
	for i, f := range files {
		eg.Go(func() (rErr error) {
			// Extractor は外部から来たファイルを扱うので、panic でプロセスを落とさない。
			defer func() {
				if r := recover(); r != nil {
					rErr = fmt.Errorf("files[%d]: extractor panicked: %v", i, r)
				}
			}()
			res, err := h.extractOne(egCtx, f)
			if err != nil {
				return fmt.Errorf("files[%d] (%s): %w", i, f.MIMEType, err)
			}
			results[i] = res
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

func (h *Handler) extractOne(ctx context.Context, in inputFile) (extracted, error) {
	if in.remoteURL != "" {
		fetched, allowed, err := h.cfg.Fetcher.Fetch(ctx, in.remoteURL)
		switch {
		case errors.Is(err, errFetchTooLarge):
			return extracted{blocked: "a file exceeds the size limit of the guardrail check"}, nil
		case err != nil:
			return extracted{}, err
		case allowed:
			in = inputFile{SourceFile: fetched}
		}
	}
	if in.unreadable != "" {
		// 中身を取得できない（許可していないホストの URL、file_id）ので、検査できない。
		if h.cfg.AllowUnsupported {
			slog.WarnContext(ctx, "file guardrail passed a file it cannot read unchecked",
				slog.String("kind", in.unreadable))
			return extracted{}, nil
		}
		return extracted{blocked: "the guardrail cannot check " + in.unreadable}, nil
	}
	f := in.SourceFile
	if len(f.Data) == 0 {
		return extracted{}, nil
	}
	if h.cfg.MaxFileBytes > 0 && len(f.Data) > h.cfg.MaxFileBytes {
		return extracted{blocked: "a file exceeds the size limit of the guardrail check"}, nil
	}
	if extract.IsText(f.MIMEType, f.Data) {
		return extracted{text: string(f.Data)}, nil
	}
	if h.cfg.Extractor == nil || !h.cfg.Extractor.Supports(f.MIMEType) {
		if h.cfg.AllowUnsupported {
			slog.WarnContext(ctx, "file guardrail passed an unsupported file unchecked",
				slog.String("mime_type", f.MIMEType), slog.Int("bytes", len(f.Data)))
			return extracted{}, nil
		}
		return extracted{
			blocked: "the guardrail cannot check files of type " + printable(f.MIMEType),
		}, nil
	}

	if h.cfg.ExtractTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.cfg.ExtractTimeout)
		defer cancel()
	}
	start := time.Now()
	text, err := h.cfg.Extractor.Extract(ctx, f)
	// ファイルの中身と起こしたテキストはログに出さない。
	slog.InfoContext(ctx, "file guardrail transcribed a file",
		slog.String("mime_type", f.MIMEType), slog.Int("bytes", len(f.Data)),
		slog.Int("text_bytes", len(text)), slog.Duration("elapsed", time.Since(start)),
		slog.Bool("ok", err == nil))
	if errors.Is(err, extract.ErrTruncated) {
		// 残りが未検査になるので、途中までのテキストで通すことはしない。
		return extracted{blocked: "a file is too large to transcribe for the guardrail check"}, nil
	}
	if err != nil {
		return extracted{}, err
	}
	return extracted{text: text}, nil
}

// printable は外部から来た文字列 s を、拒否の理由に載せられる形（表示できる
// 文字だけ、最大 100 文字）にする。
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	if runes := []rune(s); len(runes) > 100 {
		s = string(runes[:100])
	}
	return s
}

// chunk は text を最大 size 文字ずつに分ける。size が 0 以下なら分けない。
//
// 隣り合う断片は overlap 文字ずつ重ねる。境界で切れた語句は、どの断片にも全体が
// 入らないと下流が検出できないためで、overlap 文字までの語句は必ずどれかの断片に
// 全体が入る。overlap は size の半分まで（それを超える分は切り詰める）なので、
// 分割後の合計は元のテキストの2倍以下に収まる。
func chunk(text string, size, overlap int) []string {
	runes := []rune(text)
	if size <= 0 || len(runes) <= size {
		return []string{text}
	}
	step := size - min(max(overlap, 0), size/2)
	out := make([]string, 0, len(runes)/step+1)
	for start := 0; ; start += step {
		end := min(start+size, len(runes))
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			return out
		}
	}
}

// chunkCount は chunk(text, size, overlap) が返す断片の数を、断片を作らずに返す。
func chunkCount(text string, size, overlap int) int {
	n := utf8.RuneCountInString(text)
	if size <= 0 || n <= size {
		return 1
	}
	step := size - min(max(overlap, 0), size/2)
	// 最初の断片のあと、残りの n-size 文字を step 文字ずつ進む。
	return 1 + (n-size+step-1)/step
}

// hasItems は raw が要素のある JSON の配列かを返す。
func hasItems(raw json.RawMessage) bool {
	var items []json.RawMessage
	return json.Unmarshal(raw, &items) == nil && len(items) > 0
}

// originalRequest は req から images と files を除いた、下流へそのまま送る本文。
func originalRequest(req map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(req))
	for k, v := range req {
		if k != keyImages && k != keyFiles {
			out[k] = v
		}
	}
	return out
}

// extractedRequest は、ファイルから起こした texts を下流へ送る本文。呼び出しの
// 文脈（litellm_call_id・request_data・additional_provider_specific_params など）は
// req から引き継ぎ、元のリクエストの texts に対応するもの（tools など）は除く。
func extractedRequest(
	req map[string]json.RawMessage, texts []string, inputType string,
) map[string]json.RawMessage {
	out := originalRequest(req)
	delete(out, keyTools)
	delete(out, keyToolCalls)
	delete(out, keyStructuredMessages)
	// []string と string の Marshal は失敗しない。
	out[keyTexts], _ = json.Marshal(texts)
	if inputType != "" {
		out[keyInputType], _ = json.Marshal(inputType)
	}
	return out
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
