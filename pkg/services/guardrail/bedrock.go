package guardrail

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"golang.org/x/sync/errgroup"
)

// APIPath は Generic Guardrail API のエンドポイントのパス。
const APIPath = "/beta/litellm_basic_guardrail_api"

// maxRequestSize は BedrockHandler が受け付けるリクエストの最大バイト数。
const maxRequestSize = 16 << 20

// bedrockApplyConcurrency は1リクエストの texts を並行して ApplyGuardrail する数。
const bedrockApplyConcurrency = 4

// BedrockApplier は ApplyGuardrail を呼ぶクライアント（*bedrockruntime.Client）。
type BedrockApplier interface {
	ApplyGuardrail(
		ctx context.Context,
		params *bedrockruntime.ApplyGuardrailInput,
		optFns ...func(*bedrockruntime.Options),
	) (*bedrockruntime.ApplyGuardrailOutput, error)
}

// BedrockHandler は Generic Guardrail API を受け、AWS Bedrock Guardrails の
// ApplyGuardrail に変換する http.Handler。manifold guardrail bedrock が
// サイドカーとして配信する。
//
// ApplyGuardrail の outputs は入力のテキストと 1 対 1 で対応する保証が無いため、
// texts を1件ずつ ApplyGuardrail し、結果を同じ順序で返す。いずれかが拒否
// （BLOCKED のポリシーがある）なら全体を BLOCKED、書き換えがあれば
// GUARDRAIL_INTERVENED、どれも変更が無ければ NONE を返す。
type BedrockHandler struct {
	client  BedrockApplier
	id      string
	version string
}

// NewBedrockHandler は guardrail id / version の Bedrock Guardrail を使う
// BedrockHandler を返す。
func NewBedrockHandler(client BedrockApplier, id, version string) *BedrockHandler {
	return &BedrockHandler{client: client, id: id, version: version}
}

func (h *BedrockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req apiRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestSize)).
		Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.apply(r.Context(), req)
	if err != nil {
		slog.ErrorContext(r.Context(), "bedrock guardrail failed", slog.Any("error", err))
		writeJSONError(w, http.StatusBadGateway, "bedrock guardrail failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// bedrockTextResult は texts の1件を ApplyGuardrail した結果。
type bedrockTextResult struct {
	intervened bool
	blocked    bool
	reason     string
	text       string
}

func (h *BedrockHandler) apply(ctx context.Context, req apiRequest) (apiResponse, error) {
	source := types.GuardrailContentSourceInput
	if req.InputType == InputTypeResponse {
		source = types.GuardrailContentSourceOutput
	}

	results := make([]bedrockTextResult, len(req.Texts))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(bedrockApplyConcurrency)
	for i, text := range req.Texts {
		eg.Go(func() error {
			res, err := h.applyText(egCtx, source, text)
			if err != nil {
				return fmt.Errorf("texts[%d]: %w", i, err)
			}
			results[i] = res
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return apiResponse{}, err
	}

	out := apiResponse{Action: ActionNone}
	texts := make([]string, len(req.Texts))
	for i, res := range results {
		if res.blocked {
			reason := res.reason
			if reason == "" {
				reason = "blocked by guardrail"
			}
			return apiResponse{Action: ActionBlocked, BlockedReason: reason}, nil
		}
		texts[i] = req.Texts[i]
		if res.intervened {
			out.Action = ActionIntervened
			texts[i] = res.text
		}
	}
	if out.Action == ActionIntervened {
		out.Texts = texts
	}
	return out, nil
}

func (h *BedrockHandler) applyText(
	ctx context.Context, source types.GuardrailContentSource, text string,
) (bedrockTextResult, error) {
	out, err := h.client.ApplyGuardrail(ctx, &bedrockruntime.ApplyGuardrailInput{
		GuardrailIdentifier: aws.String(h.id),
		GuardrailVersion:    aws.String(h.version),
		Source:              source,
		Content: []types.GuardrailContentBlock{
			&types.GuardrailContentBlockMemberText{
				Value: types.GuardrailTextBlock{Text: aws.String(text)},
			},
		},
	})
	if err != nil {
		return bedrockTextResult{}, err
	}
	if out.Action != types.GuardrailActionGuardrailIntervened {
		return bedrockTextResult{}, nil
	}
	reason := aws.ToString(out.ActionReason)
	// outputs が空の介入は、書き換え後のテキストが分からないので拒否として扱う。
	if bedrockBlocked(out.Assessments) || len(out.Outputs) == 0 {
		return bedrockTextResult{blocked: true, reason: reason}, nil
	}
	var b strings.Builder
	for _, o := range out.Outputs {
		b.WriteString(aws.ToString(o.Text))
	}
	return bedrockTextResult{intervened: true, text: b.String()}, nil
}

// bedrockBlocked は assessments のいずれかのポリシーが BLOCKED と判定したかを
// 返す。機密情報フィルタの ANONYMIZED（マスキング）は拒否ではない。
func bedrockBlocked(assessments []types.GuardrailAssessment) bool {
	for _, a := range assessments {
		if sensitiveInformationBlocked(a.SensitiveInformationPolicy) ||
			contentBlocked(a.ContentPolicy) ||
			topicBlocked(a.TopicPolicy) ||
			wordBlocked(a.WordPolicy) ||
			contextualGroundingBlocked(a.ContextualGroundingPolicy) {
			return true
		}
	}
	return false
}

func sensitiveInformationBlocked(p *types.GuardrailSensitiveInformationPolicyAssessment) bool {
	if p == nil {
		return false
	}
	for _, e := range p.PiiEntities {
		if e.Action == types.GuardrailSensitiveInformationPolicyActionBlocked {
			return true
		}
	}
	for _, e := range p.Regexes {
		if e.Action == types.GuardrailSensitiveInformationPolicyActionBlocked {
			return true
		}
	}
	return false
}

func contentBlocked(p *types.GuardrailContentPolicyAssessment) bool {
	if p == nil {
		return false
	}
	for _, f := range p.Filters {
		if f.Action == types.GuardrailContentPolicyActionBlocked {
			return true
		}
	}
	return false
}

func topicBlocked(p *types.GuardrailTopicPolicyAssessment) bool {
	if p == nil {
		return false
	}
	for _, tp := range p.Topics {
		if tp.Action == types.GuardrailTopicPolicyActionBlocked {
			return true
		}
	}
	return false
}

func wordBlocked(p *types.GuardrailWordPolicyAssessment) bool {
	if p == nil {
		return false
	}
	for _, w := range p.CustomWords {
		if w.Action == types.GuardrailWordPolicyActionBlocked {
			return true
		}
	}
	for _, w := range p.ManagedWordLists {
		if w.Action == types.GuardrailWordPolicyActionBlocked {
			return true
		}
	}
	return false
}

func contextualGroundingBlocked(p *types.GuardrailContextualGroundingPolicyAssessment) bool {
	if p == nil {
		return false
	}
	for _, f := range p.Filters {
		if f.Action == types.GuardrailContextualGroundingPolicyActionBlocked {
			return true
		}
	}
	return false
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
