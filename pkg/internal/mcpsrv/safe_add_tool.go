package mcpsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addToolSafely は (*mcp.Server).AddTool と同じく t を srv に登録する。ただし AddTool が
// 受け付けない定義では panic せず、エラーを返す。
//
// AddTool に渡るツール定義は、ゲートウェイが完全には制御できない場所（タブの tools/list、
// 取得した OpenAPI spec）から来る。また AddTool は HTTP ハンドラの外（edge の WebSocket
// ゴルーチン、SDK の読み取りゴルーチン、spec の再取得）で呼ばれることが多く、そこでの
// panic は middleware.Recover に届かない。そのため validateToolDefinition で事前に検査し、
// 将来の SDK が検査を増やした場合に備えて、呼び出し自体も recover で守る。
func addToolSafely(srv *mcp.Server, t *mcp.Tool, h mcp.ToolHandler) (rErr error) {
	if srv == nil {
		return errors.New("server is nil")
	}
	if h == nil {
		return errors.New("tool handler is nil")
	}
	if err := validateToolDefinition(t); err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("AddTool panicked; tool skipped",
				slog.String("tool", t.Name), slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())))
			rErr = fmt.Errorf("add tool %q: %v", t.Name, r)
		}
	}()
	srv.AddTool(t, h)
	return nil
}

// validateToolDefinition は (*mcp.Server).AddTool が panic する条件（go-sdk v1.8.0）を
// 再現し、不正な定義を AddTool に渡す前に弾く。スキーマはどちらも JSON を往復させてから
// 調べるので、SDK が受け付ける表現（map[string]any、json.RawMessage、
// *jsonschema.Schema など）をすべて扱える。
func validateToolDefinition(t *mcp.Tool) error {
	if t == nil {
		return errors.New("tool is nil")
	}
	if t.InputSchema == nil {
		return errors.New("missing input schema")
	}
	var input map[string]any
	if err := remarshalJSON(t.InputSchema, &input); err != nil {
		return fmt.Errorf("input schema is not a JSON object: %w", err)
	}
	if typ, _ := input["type"].(string); typ != "object" {
		return fmt.Errorf(`input schema must have type "object" (got %v)`, input["type"])
	}
	if t.OutputSchema != nil {
		var output any
		if err := remarshalJSON(t.OutputSchema, &output); err != nil {
			return fmt.Errorf("output schema is not valid JSON: %w", err)
		}
	}
	if err := validateParamHeaderAnnotations(t.InputSchema); err != nil {
		return fmt.Errorf("invalid parameter header annotations: %w", err)
	}
	return nil
}

// remarshalJSON は from を JSON 経由で to に変換する。
func remarshalJSON(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}

// headerSchemaProperty は、SDK の x-mcp-header 検査が入力スキーマのプロパティから読む
// フィールドだけを取り出す。
type headerSchemaProperty struct {
	Type       string                          `json:"type"`
	XMCPHeader json.RawMessage                 `json:"x-mcp-header,omitempty"`
	Properties map[string]headerSchemaProperty `json:"properties,omitempty"`
}

// validateParamHeaderAnnotations は SDK の x-mcp-header 注釈の検査（失敗すると AddTool が
// panic する）を再現する。注釈はどの深さのプロパティにも付けられるが、プリミティブ型
// （string / integer / boolean）のプロパティに限られ、値は空でない HTTP トークンで、
// スキーマ全体で大文字小文字を区別せず一意でなければならない。この形に decode できない
// スキーマは、SDK と同じく検査しない。
func validateParamHeaderAnnotations(schema any) error {
	var root headerSchemaProperty
	if err := remarshalJSON(schema, &root); err != nil {
		return nil //nolint:nilerr // SDK もこの場合は検査しない
	}
	if len(root.Properties) == 0 {
		return nil
	}
	return validateParamHeadersIn(root.Properties, "", map[string]bool{})
}

func validateParamHeadersIn(
	props map[string]headerSchemaProperty,
	prefix string,
	seen map[string]bool,
) error {
	for propName, prop := range props {
		path := propName
		if prefix != "" {
			path = prefix + "." + propName
		}
		if prop.XMCPHeader != nil {
			if prop.Type != "string" && prop.Type != "integer" && prop.Type != "boolean" {
				return fmt.Errorf(
					"property %q: x-mcp-header can only be applied to primitive types "+
						"(integer, string, boolean), got %q", path, prop.Type,
				)
			}
			var headerName string
			if err := json.Unmarshal(prop.XMCPHeader, &headerName); err != nil || headerName == "" {
				return fmt.Errorf("property %q: x-mcp-header must be a non-empty string", path)
			}
			if err := validateHeaderToken(headerName); err != nil {
				return fmt.Errorf("property %q: %w", path, err)
			}
			lower := strings.ToLower(headerName)
			if seen[lower] {
				return fmt.Errorf(
					"property %q: duplicate x-mcp-header value %q (case-insensitive)",
					path,
					headerName,
				)
			}
			seen[lower] = true
		}
		if len(prop.Properties) > 0 {
			if err := validateParamHeadersIn(prop.Properties, path, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateHeaderToken は name が HTTP のフィールド名のトークン構文
// （1*tchar、RFC 9110 §5.6.2）に従うか検査する。
func validateHeaderToken(name string) error {
	for _, c := range name {
		if !isHeaderTChar(c) {
			return fmt.Errorf("x-mcp-header value %q contains invalid character %q", name, c)
		}
	}
	return nil
}

func isHeaderTChar(c rune) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
