package fileguard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
)

// inputFile はリクエストに含まれるファイル1件。
type inputFile struct {
	extract.SourceFile
	// unreadable が空でなければ、中身がリクエストに無いファイル（URL や file_id で
	// 渡されたもの）で、その説明。拒否の理由に使う。
	unreadable string
	// remoteURL は URL で渡されたファイルの URL。許可したホストなら取得して検査する
	// （Config.Fetcher）。
	remoteURL string
}

// 中身がリクエストに無いファイルの説明。
const (
	unreadableImageURL = "images given by URL"
	unreadableFileURL  = "files given by URL"
	unreadableFileID   = "files given by file_id"
	unreadableNoData   = "files given without their content"
	unreadablePart     = "message parts of a type it cannot read"
)

// errTooManyFiles は、リクエストのファイルが上限（Config.MaxFiles）を超えたことを表す。
var errTooManyFiles = errors.New("the request has more files than the guardrail checks at once")

// メッセージの形が読めないときのエラー。
var (
	errBadContent = &badRequestError{
		"the content of a message must be a string or an array of parts",
	}
	errBadParts   = &badRequestError{"the content of a message must be an array of parts"}
	errTooDeep    = &badRequestError{"the parts of a message are nested too deeply"}
	errBadImages  = &badRequestError{"images must be an array of strings"}
	errBadFiles   = &badRequestError{"files must be an array of {uri, mime_type, data}"}
	errBadMessage = &badRequestError{"structured_messages must be an array of messages"}
)

// eachElement は JSON の配列 raw の要素を、1件ずつ fn に渡す。配列全体を一度に
// 復元しないので、要素の多い配列（空に近い要素を大量に並べたものなど）でも、使う
// メモリは処理中の1件ぶんで済み、fn がエラーを返した時点で打ち切れる。null は空の
// 配列として扱う。配列として読めなければ bad を返す。
func eachElement(raw json.RawMessage, bad error, fn func(json.RawMessage) error) error {
	if strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return bad
	}
	for dec.More() {
		var item json.RawMessage
		if dec.Decode(&item) != nil {
			return bad
		}
		if err := fn(item); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return bad
	}
	return nil
}

// fileList は、上限つきのファイルの一覧。
type fileList struct {
	files []inputFile
	limit int
}

// add は f を加える。上限を超えるなら errTooManyFiles を返す。
func (l *fileList) add(f inputFile) error {
	if len(l.files) >= l.limit {
		return errTooManyFiles
	}
	l.files = append(l.files, f)
	return nil
}

// requestFiles は req に含まれるファイルを、種類を揃えた一覧にする。
//
//   - images: 画像。base64 か data URL。LiteLLM はメッセージの image_url を
//     そのまま入れるので、http(s) の URL のこともある。
//   - files: Manifold の拡張。画像以外のファイル。
//   - structured_messages: LiteLLM が送るメッセージ。PDF などのファイルは
//     ここの content にしか入らない。画像（image_url）は images にも入るので、
//     ここからは取らない。
//
// URL で渡されたファイルは、許可したホスト（Config.Fetcher）からだけ取得する
// （呼び出し側が指定した URL を取得すると SSRF の入口になる）。それ以外の URL と
// file_id で渡されたファイルは、中身を検査できないファイルとして扱う。
//
// ファイルが limit 件を超えたら、残りを読まずに errTooManyFiles を返す。
func requestFiles(req map[string]json.RawMessage, limit int) ([]inputFile, error) {
	list := &fileList{limit: limit}
	if raw, ok := req[keyImages]; ok {
		if err := eachElement(raw, errBadImages, list.addImage); err != nil {
			return nil, err
		}
	}
	if raw, ok := req[keyFiles]; ok {
		if err := eachElement(raw, errBadFiles, list.addAPIFile); err != nil {
			return nil, err
		}
	}
	if raw, ok := req[keyStructuredMessages]; ok {
		if err := eachElement(raw, errBadMessage, list.addMessage); err != nil {
			return nil, err
		}
	}
	return list.files, nil
}

// addImage は images の1件を加える。
func (l *fileList) addImage(raw json.RawMessage) error {
	var image string
	if json.Unmarshal(raw, &image) != nil {
		return errBadImages
	}
	if isRemoteURL(image) {
		return l.add(inputFile{unreadable: unreadableImageURL, remoteURL: image})
	}
	f, err := decodeInline(image)
	if err != nil {
		return &badRequestError{"images must be base64, data URLs or http(s) URLs"}
	}
	return l.add(inputFile{SourceFile: f})
}

// addAPIFile は files の1件を加える。
func (l *fileList) addAPIFile(raw json.RawMessage) error {
	var f apiFile
	if json.Unmarshal(raw, &f) != nil {
		return errBadFiles
	}
	if len(f.Data) == 0 && f.URI != "" {
		// 中身が無く URI だけのファイルは、読めないので検査できない。
		return l.add(inputFile{unreadable: unreadableNoData})
	}
	return l.add(inputFile{
		URI: f.URI, MIMEType: extract.NormalizeMIME(f.MIMEType, f.Data), Data: f.Data,
	})
}

// messagePart はメッセージの content の1件。ファイルは OpenAI の Chat Completions
// の形（type: file、file の中に file_data / file_id）と、Responses API の形
// （type: input_file、直下に file_data / file_url / file_id）の両方を読む。
type messagePart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text"`
	File     *messageFile `json:"file"`
	FileData string       `json:"file_data"`
	FileURL  string       `json:"file_url"`
	FileID   string       `json:"file_id"`
	// tool_result（content）と function_call_output（output）は、中にパートの配列を
	// 持てる。
	Content json.RawMessage `json:"content"`
	Output  json.RawMessage `json:"output"`
}

type messageFile struct {
	FileData string `json:"file_data"`
	FileURL  string `json:"file_url"`
	FileID   string `json:"file_id"`
}

// maxPartDepth は、パートの入れ子（tool_result の中のパートなど）を辿る深さの上限。
// これより深いメッセージは、中身を読めないので拒否する。
const maxPartDepth = 8

// content の形。
const (
	contentEmpty  = iota // 省略、null
	contentString        // 文字列
	contentParts         // パートの配列
	contentOther         // それ以外（オブジェクト、数値など）
)

// contentShape は、content（メッセージの content、ツールの結果の content / output）の
// 形を返す。
func contentShape(raw json.RawMessage) int {
	content := strings.TrimSpace(string(raw))
	switch {
	case content == "" || content == "null":
		return contentEmpty
	case strings.HasPrefix(content, `"`):
		return contentString
	case strings.HasPrefix(content, "["):
		return contentParts
	default:
		return contentOther
	}
}

// addMessage は structured_messages の1件の content にあるファイルを加える。
func (l *fileList) addMessage(raw json.RawMessage) error {
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return errBadMessage
	}
	return l.addContent(message.Content, 0)
}

// addContent は content にあるファイルを加える。content は文字列か、パートの配列。
// 文字列（や null、省略）にファイルは無い。それ以外の形（オブジェクトなど）を
// 無視すると、その中のファイルが未検査で通るので、拒否する。
func (l *fileList) addContent(raw json.RawMessage, depth int) error {
	switch contentShape(raw) {
	case contentEmpty, contentString:
		return nil
	case contentOther:
		return errBadContent
	}
	if depth > maxPartDepth {
		return errTooDeep
	}
	return eachElement(raw, errBadParts, func(item json.RawMessage) error {
		var part messagePart
		if json.Unmarshal(item, &part) != nil {
			return errBadParts
		}
		return l.addPart(part, depth)
	})
}

// addPart はパート1件にあるファイルを加える。
func (l *fileList) addPart(part messagePart, depth int) error {
	switch partKind(part.Type) {
	case partNotFile:
		// 最上位の image_url は呼び出し側（LiteLLM）が images に入れて送るが、
		// ツールの結果の中の image_url は images に入らない。読まずに通すと
		// 未検査になるので、読めないファイルとして扱う。
		if depth > 0 && strings.EqualFold(strings.TrimSpace(part.Type), "image_url") {
			return l.add(inputFile{unreadable: unreadablePart})
		}
		// ツールの結果は、中にパートの配列（ファイルを含みうる）を持てる。
		for _, nested := range []json.RawMessage{part.Content, part.Output} {
			if err := l.addContent(nested, depth+1); err != nil {
				return err
			}
		}
		return nil
	case partUnknown:
		// 読み方を知らない種類（音声、動画、他の API の形の画像や文書など）を
		// 無視すると、その中身が未検査で通る。読めないファイルとして扱う。
		return l.add(inputFile{unreadable: unreadablePart})
	}
	file := messageFile{FileData: part.FileData, FileURL: part.FileURL, FileID: part.FileID}
	if part.File != nil {
		file = *part.File
	}
	switch {
	case file.FileData != "" && !isRemoteURL(file.FileData):
		f, err := decodeInline(file.FileData)
		if err != nil {
			return &badRequestError{"file_data must be base64 or a data URL"}
		}
		return l.add(inputFile{SourceFile: f})
	case file.FileData != "" || file.FileURL != "":
		remote := file.FileURL
		if file.FileData != "" {
			remote = file.FileData
		}
		return l.add(inputFile{unreadable: unreadableFileURL, remoteURL: remote})
	default:
		return l.add(inputFile{unreadable: unreadableFileID})
	}
}

// メッセージのパートの種類。
const (
	partFile = iota
	partNotFile
	partUnknown
)

// partKind は content のパートの type を、ファイル（file / input_file）、ファイルを
// 運ばないと分かっている種類、それ以外（未知）に分ける。image_url の画像は、
// 呼び出し側（LiteLLM）が images に入れて送るので、ここでは読まない。
func partKind(partType string) int {
	switch strings.ToLower(strings.TrimSpace(partType)) {
	case "file", "input_file":
		return partFile
	case "text", "input_text", "output_text", "summary_text", "refusal",
		"image_url",
		"thinking", "redacted_thinking", "reasoning",
		"tool_use", "tool_result", "function_call", "function_call_output":
		return partNotFile
	default:
		return partUnknown
	}
}

func isRemoteURL(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// decodeInline は base64、または data URL（data:<種類>;base64,<中身>）をデコードする。
// 種類は data URL の申告と中身から決める。
func decodeInline(s string) (extract.SourceFile, error) {
	declared := ""
	if rest, ok := strings.CutPrefix(s, "data:"); ok {
		meta, payload, found := strings.Cut(rest, ",")
		if !found {
			return extract.SourceFile{}, fmt.Errorf("malformed data URL")
		}
		declared, _, _ = strings.Cut(meta, ";")
		s = payload
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return extract.SourceFile{}, err
	}
	return extract.SourceFile{MIMEType: extract.NormalizeMIME(declared, data), Data: data}, nil
}

// structuredMessageTexts は structured_messages の content にあるテキストを返す。
// content が文字列ならその文字列、パートの配列なら text / input_text / output_text の
// パートのテキスト。
//
// 呼び出し側が texts を付けずにメッセージだけを送ってきたときに使う。texts だけを
// 検査する下流（同梱の Bedrock サイドカーなど）は、メッセージを転送しても中のテキストを
// 検査しないので、取り出して texts として送る。
func structuredMessageTexts(raw json.RawMessage) ([]string, error) {
	var texts []string
	err := eachElement(raw, errBadMessage, func(item json.RawMessage) error {
		var message struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(item, &message) != nil {
			return errBadMessage
		}
		return contentTexts(message.Content, 0, &texts)
	})
	if err != nil {
		return nil, err
	}
	return texts, nil
}

// contentTexts は、content（文字列か、パートの配列）にあるテキストを out に加える。
// ツールの結果（tool_result の content、function_call_output の output）の中も辿る。
// 読めない形や、深すぎる入れ子は、中のテキストを黙って捨てずに拒否する。
func contentTexts(raw json.RawMessage, depth int, out *[]string) error {
	switch contentShape(raw) {
	case contentEmpty:
		return nil
	case contentOther:
		return errBadContent
	case contentString:
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return errBadContent
		}
		if text != "" {
			*out = append(*out, text)
		}
		return nil
	}
	if depth > maxPartDepth {
		return errTooDeep
	}
	return eachElement(raw, errBadParts, func(item json.RawMessage) error {
		var part messagePart
		if json.Unmarshal(item, &part) != nil {
			return errBadParts
		}
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "text", "input_text", "output_text":
			if part.Text != "" {
				*out = append(*out, part.Text)
			}
		case "tool_result", "function_call_output":
			for _, nested := range []json.RawMessage{part.Content, part.Output} {
				if err := contentTexts(nested, depth+1, out); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
