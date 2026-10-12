package guardrail

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func newGuardrailStub(
	t *testing.T, status int, response string, got *map[string]any, gotHeader *http.Header,
) *HTTPGuardrail {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			_ = json.Unmarshal(raw, got)
		}
		if gotHeader != nil {
			*gotHeader = r.Header.Clone()
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return NewHTTPGuardrail(config.AuditGuardrailConfig{
		URL:     srv.URL + "/beta/litellm_basic_guardrail_api",
		Headers: map[string]string{"X-Api-Key": "secret"},
	}, srv.Client())
}

func TestHTTPGuardrail_Request(t *testing.T) {
	var (
		got    map[string]any
		header http.Header
	)
	g := newGuardrailStub(t, http.StatusOK, `{"action":"NONE"}`, &got, &header)
	res, err := g.Apply(t.Context(), Request{
		Texts:     []string{`{"email":"a@example.com"}`},
		InputType: InputTypeRequest,
		Params:    map[string]any{"tool": "getpet"},
	})
	require.NoError(t, err)
	require.Equal(t, ActionNone, res.Action)

	require.Equal(t, []any{`{"email":"a@example.com"}`}, got["texts"])
	require.Equal(t, "request", got["input_type"])
	require.NotEmpty(t, got["litellm_call_id"])
	require.NotEmpty(t, got["litellm_trace_id"])
	require.Equal(t, map[string]any{}, got["request_data"])
	require.Equal(t, map[string]any{"tool": "getpet"}, got["additional_provider_specific_params"])
	require.Equal(t, "application/json", header.Get("Content-Type"))
	require.Equal(t, "secret", header.Get("X-Api-Key"))
}

func TestHTTPGuardrail_Responses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		response string
		want     Result
		wantErr  string
	}{
		{
			name:     "intervened",
			status:   http.StatusOK,
			response: `{"action":"GUARDRAIL_INTERVENED","texts":["{\"email\":\"[EMAIL]\"}"]}`,
			want:     Result{Action: ActionIntervened, Texts: []string{`{"email":"[EMAIL]"}`}},
		},
		{
			name:     "blocked",
			status:   http.StatusOK,
			response: `{"action":"BLOCKED","blocked_reason":"prohibited"}`,
			want:     Result{Action: ActionBlocked, BlockedReason: "prohibited"},
		},
		{
			name:     "intervened with mismatched texts",
			status:   http.StatusOK,
			response: `{"action":"GUARDRAIL_INTERVENED","texts":[]}`,
			wantErr:  "returned 0 texts for 1 inputs",
		},
		{
			name:     "unknown action",
			status:   http.StatusOK,
			response: `{"action":"MAYBE"}`,
			wantErr:  `unknown action "MAYBE"`,
		},
		{
			name:     "not json",
			status:   http.StatusOK,
			response: `nope`,
			wantErr:  "decode response",
		},
		{
			name:    "server error",
			status:  http.StatusInternalServerError,
			wantErr: "returned status 500",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuardrailStub(t, tc.status, tc.response, nil, nil)
			res, err := g.Apply(
				t.Context(),
				Request{Texts: []string{"x"}, InputType: InputTypeRequest},
			)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, res)
		})
	}
}
