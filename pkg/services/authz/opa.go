package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

type opaAllowResult struct {
	Result *bool `json:"result"`
}

// opaAllowedToolsResult's Result entries are decoded as raw maps because the
// {"<server>","<toolName>"} keys are configurable (config.AuthzInput), not
// fixed JSON tags.
type opaAllowedToolsResult struct {
	Result *[]map[string]any `json:"result"`
}

// OPADecider is a Decider backed by an OPA sidecar's REST data API.
type OPADecider struct {
	opaURL       string
	decisionPath config.AuthzDecisionPath
	input        config.AuthzInput
	httpClient   *http.Client
}

var _ Decider = (*OPADecider)(nil)

// NewOPADecider builds an OPADecider for cfg. When httpClient is nil, one is
// built from the shared internal transport with cfg.Timeout — callers that
// need a different transport (e.g. for tests) pass their own client.
func NewOPADecider(cfg config.AuthzConfig, httpClient *http.Client) *OPADecider {
	cfg = cfg.WithDefaults()
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: client.CustomTransport(),
			Timeout:   cfg.Timeout,
		}
	}
	return &OPADecider{
		opaURL:       cfg.OPAURL,
		decisionPath: cfg.DecisionPath,
		input:        cfg.Input,
		httpClient:   httpClient,
	}
}

// post sends {"input": input} to path and decodes the response body into
// out. A non-200 response is reported as an error without decoding.
func (d *OPADecider) post(ctx context.Context, path string, input, out any) error {
	body, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		return fmt.Errorf("authz: encode OPA request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		d.opaURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("authz: build OPA request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("authz: call OPA: %w", err)
	}
	defer resp.Body.Close() //nolint: errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authz: OPA %s returned status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("authz: decode OPA response: %w", err)
	}
	return nil
}

func (d *OPADecider) Allow(ctx context.Context, p Principal, t ToolRef) (bool, error) {
	var out opaAllowResult
	err := d.post(ctx, d.decisionPath.Call, map[string]any{
		d.input.User:   p.UserID,
		d.input.Groups: p.Groups,
		d.input.Server: t.Server,
		d.input.Tool:   t.Name,
	}, &out)
	if err != nil {
		return false, err
	}
	if out.Result == nil {
		return false, fmt.Errorf("authz: OPA response missing result")
	}
	return *out.Result, nil
}

func (d *OPADecider) AllowedTools(
	ctx context.Context, p Principal, tools []ToolRef,
) ([]ToolRef, error) {
	if len(tools) == 0 {
		return []ToolRef{}, nil
	}

	inputTools := make([]map[string]any, len(tools))
	for i, t := range tools {
		inputTools[i] = map[string]any{
			d.input.Server:   t.Server,
			d.input.ToolName: t.Name,
		}
	}

	var out opaAllowedToolsResult
	err := d.post(ctx, d.decisionPath.List, map[string]any{
		d.input.User:   p.UserID,
		d.input.Groups: p.Groups,
		d.input.Tools:  inputTools,
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Result == nil {
		return nil, fmt.Errorf("authz: OPA response missing result")
	}

	allowed := make(map[ToolRef]struct{}, len(*out.Result))
	for _, entry := range *out.Result {
		server, _ := entry[d.input.Server].(string)
		name, _ := entry[d.input.ToolName].(string)
		allowed[ToolRef{Server: server, Name: name}] = struct{}{}
	}

	filtered := make([]ToolRef, 0, len(tools))
	for _, t := range tools {
		if _, ok := allowed[t]; ok {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// AllowCatalog reports whether p may read the unfiltered tool catalog
// (GET /mcp/list?tools=true).
func (d *OPADecider) AllowCatalog(ctx context.Context, p Principal) (bool, error) {
	var out opaAllowResult
	err := d.post(ctx, d.decisionPath.Catalog, map[string]any{
		d.input.User:   p.UserID,
		d.input.Groups: p.Groups,
	}, &out)
	if err != nil {
		return false, err
	}
	if out.Result == nil {
		return false, fmt.Errorf("authz: OPA response missing result")
	}
	return *out.Result, nil
}
