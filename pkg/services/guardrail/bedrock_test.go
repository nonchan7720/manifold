package guardrail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// fakeBedrock は ApplyGuardrail の入力を記録し、テキストごとに fn の結果を返す。
type fakeBedrock struct {
	mu     sync.Mutex
	inputs []*bedrockruntime.ApplyGuardrailInput
	fn     func(text string) (*bedrockruntime.ApplyGuardrailOutput, error)
}

func (f *fakeBedrock) ApplyGuardrail(
	_ context.Context, in *bedrockruntime.ApplyGuardrailInput, _ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ApplyGuardrailOutput, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	text := in.Content[0].(*types.GuardrailContentBlockMemberText).Value.Text
	return f.fn(aws.ToString(text))
}

// anonymizeEmail は "@" を含むテキストを機密情報フィルタの ANONYMIZED として書き換える。
func anonymizeEmail(text string) (*bedrockruntime.ApplyGuardrailOutput, error) {
	if !strings.Contains(text, "@") {
		return &bedrockruntime.ApplyGuardrailOutput{Action: types.GuardrailActionNone}, nil
	}
	return &bedrockruntime.ApplyGuardrailOutput{
		Action:  types.GuardrailActionGuardrailIntervened,
		Outputs: []types.GuardrailOutputContent{{Text: aws.String(`{"email":"{EMAIL}"}`)}},
		Assessments: []types.GuardrailAssessment{{
			SensitiveInformationPolicy: &types.GuardrailSensitiveInformationPolicyAssessment{
				PiiEntities: []types.GuardrailPiiEntityFilter{{
					Action: types.GuardrailSensitiveInformationPolicyActionAnonymized,
					Type:   types.GuardrailPiiEntityTypeEmail,
				}},
			},
		}},
	}, nil
}

// bedrockClient は BedrockHandler を立て、それを呼ぶ HTTPGuardrail を返す。
func bedrockClient(t *testing.T, f *fakeBedrock) *HTTPGuardrail {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("POST "+APIPath, NewBedrockHandler(f, "gr-123", "1"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewHTTPGuardrail(config.GuardrailConfig{URL: srv.URL + APIPath}, srv.Client())
}

func TestBedrockHandler_Anonymized(t *testing.T) {
	f := &fakeBedrock{fn: anonymizeEmail}
	g := bedrockClient(t, f)

	res, err := g.Apply(t.Context(), Request{
		Texts:     []string{`{"id":1}`, `{"email":"a@example.com"}`},
		InputType: InputTypeRequest,
	})
	require.NoError(t, err)
	require.Equal(t, ActionIntervened, res.Action)
	// texts は入力と同じ順序で、変更の無いものは元のまま返る。
	require.Equal(t, []string{`{"id":1}`, `{"email":"{EMAIL}"}`}, res.Texts)

	require.Len(t, f.inputs, 2)
	for _, in := range f.inputs {
		require.Equal(t, "gr-123", aws.ToString(in.GuardrailIdentifier))
		require.Equal(t, "1", aws.ToString(in.GuardrailVersion))
		require.Equal(t, types.GuardrailContentSourceInput, in.Source)
	}
}

func TestBedrockHandler_None(t *testing.T) {
	f := &fakeBedrock{fn: anonymizeEmail}
	res, err := bedrockClient(t, f).Apply(t.Context(), Request{
		Texts: []string{`{"id":1}`}, InputType: InputTypeResponse,
	})
	require.NoError(t, err)
	require.Equal(t, Result{Action: ActionNone}, res)
	require.Equal(t, types.GuardrailContentSourceOutput, f.inputs[0].Source)
}

func TestBedrockHandler_Blocked(t *testing.T) {
	for _, tc := range []struct {
		name       string
		out        *bedrockruntime.ApplyGuardrailOutput
		wantReason string
	}{
		{
			name: "blocking policy",
			out: &bedrockruntime.ApplyGuardrailOutput{
				Action:       types.GuardrailActionGuardrailIntervened,
				ActionReason: aws.String("Guardrail blocked."),
				Outputs:      []types.GuardrailOutputContent{{Text: aws.String("Sorry.")}},
				Assessments: []types.GuardrailAssessment{{
					ContentPolicy: &types.GuardrailContentPolicyAssessment{
						Filters: []types.GuardrailContentFilter{{
							Action: types.GuardrailContentPolicyActionBlocked,
						}},
					},
				}},
			},
			wantReason: "Guardrail blocked.",
		},
		{
			name: "intervened without outputs",
			out: &bedrockruntime.ApplyGuardrailOutput{
				Action: types.GuardrailActionGuardrailIntervened,
			},
			wantReason: "blocked by guardrail",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBedrock{fn: func(string) (*bedrockruntime.ApplyGuardrailOutput, error) {
				return tc.out, nil
			}}
			res, err := bedrockClient(t, f).Apply(t.Context(), Request{
				Texts: []string{"x"}, InputType: InputTypeRequest,
			})
			require.NoError(t, err)
			require.Equal(t, Result{Action: ActionBlocked, BlockedReason: tc.wantReason}, res)
		})
	}
}

func TestBedrockHandler_Errors(t *testing.T) {
	f := &fakeBedrock{fn: func(string) (*bedrockruntime.ApplyGuardrailOutput, error) {
		return nil, errors.New("AccessDeniedException")
	}}
	_, err := bedrockClient(t, f).Apply(t.Context(), Request{
		Texts: []string{"x"}, InputType: InputTypeRequest,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "returned status 502")

	h := NewBedrockHandler(f, "gr-123", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, APIPath, strings.NewReader("nope"),
	))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, APIPath, nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
