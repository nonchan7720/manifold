package mcpsrv

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SEP-2322（multi round-trip request）の resultType の補完。
//
// 2026-07-28 以降のプロトコルでは、tools/call・resources/read・prompts/get の
// 結果に resultType（"complete" または "input_required"）が必須になる。
// go-sdk は組み込みハンドラ（Server.callTool 等）の中でこれを付けるが、
// パススルー（newBackendPassthroughMiddleware・newBackendResourcesMiddleware・
// newServiceAgentsMiddleware）はそのハンドラを通らず、バックエンドの結果を
// そのまま返す。バックエンドとの交渉が 2026-07-28 より古い版で済んでいる場合、
// その結果に resultType は無く、下流の新しい版のクライアント
// （Claude Agent SDK の CLI 等）は応答を不正として捨てる。
//
// resultType は SDK の非公開フィールドで、JSON の marshal / unmarshal でのみ
// 読み書きできる。そのため一度 JSON に詰め直して補う。

const (
	// resultTypeProtocolVersion は resultType が必須になったプロトコルの版。
	resultTypeProtocolVersion = "2026-07-28"
	// resultTypeKey は結果の JSON で resultType を持つキー。
	resultTypeKey = "resultType"
	// resultTypeComplete は結果が完了していることを表す resultType の値。
	resultTypeComplete = "complete"
)

// relayedResult は tools/call・resources/read・prompts/get の結果を表す型。
type relayedResult interface {
	mcp.CallToolResult | mcp.ReadResourceResult | mcp.GetPromptResult
}

// callerRequiresResultType は req の呼び出し元（下流のセッション）が
// 2026-07-28 以降のプロトコルで接続しているかを返す。Stateless 配信では、
// 新しい版はリクエストごとの _meta から、古い版は MCP-Protocol-Version ヘッダー
// から SDK が InitializeParams を組み立てる。SDK の組み込みハンドラと同じく、
// InitializeParams が無ければ最新版とみなす。
func callerRequiresResultType(req mcp.Request) bool {
	if req == nil {
		return false
	}
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok || ss == nil {
		return false
	}
	iparams := ss.InitializeParams()
	return iparams == nil || iparams.ProtocolVersion >= resultTypeProtocolVersion
}

// completeRelayedResult は中継で返す res に、req の呼び出し元が 2026-07-28 以降
// なら resultType を補って返す。それより古い呼び出し元には res をそのまま返す。
func completeRelayedResult[R relayedResult](req mcp.Request, res *R) (*R, error) {
	if res == nil || !callerRequiresResultType(req) {
		return res, nil
	}
	return withCompleteResultType(res)
}

// withCompleteResultType は res に resultType が無ければ "complete" を補った複製を
// 返す。バックエンドが既に resultType（"input_required" 等）を付けている場合は
// その値を変えず、res をそのまま返す。
func withCompleteResultType[R relayedResult](res *R) (*R, error) {
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("marshal relayed result: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode relayed result: %w", err)
	}
	if _, ok := fields[resultTypeKey]; ok {
		return res, nil
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	value, err := json.Marshal(resultTypeComplete)
	if err != nil {
		return nil, fmt.Errorf("marshal resultType: %w", err)
	}
	fields[resultTypeKey] = value
	raw, err = json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal relayed result: %w", err)
	}
	out := new(R)
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("decode relayed result: %w", err)
	}
	return out, nil
}
