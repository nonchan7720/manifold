package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMCPToolRegistry(t *testing.T) {
	r := NewMCPToolRegistry()
	require.NotNil(t, r)
	require.Empty(t, r.ListTools())
}

func TestMCPToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewMCPToolRegistry()

	handler := func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		return []byte("result"), "text/plain", nil
	}

	r.RegisterTool("tool1", "Test Tool 1", map[string]any{"type": "object"}, handler)

	tool := r.GetTool("tool1")
	require.NotNil(t, tool)
	require.Equal(t, "tool1", tool.tool.Name)
	require.Equal(t, "Test Tool 1", tool.tool.Description)
	require.NotNil(t, tool.handler)
}

func TestMCPToolRegistry_GetNotFound(t *testing.T) {
	r := NewMCPToolRegistry()
	tool := r.GetTool("nonexistent")
	require.Nil(t, tool)
}

func TestMCPToolRegistry_ListTools(t *testing.T) {
	r := NewMCPToolRegistry()

	handler := func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		return nil, "", nil
	}

	r.RegisterTool("tool_a", "Tool A", nil, handler)
	r.RegisterTool("tool_b", "Tool B", nil, handler)
	r.RegisterTool("tool_c", "Tool C", nil, handler)

	tools := r.ListTools()
	require.Len(t, tools, 3)
}

func TestMCPToolRegistry_RegisterOverwrite(t *testing.T) {
	r := NewMCPToolRegistry()

	handler1 := func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		return []byte("v1"), "text/plain", nil
	}
	handler2 := func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		return []byte("v2"), "text/plain", nil
	}

	r.RegisterTool("mytool", "Version 1", nil, handler1)
	r.RegisterTool("mytool", "Version 2", nil, handler2)

	// 上書きされる
	tool := r.GetTool("mytool")
	require.NotNil(t, tool)
	require.Equal(t, "Version 2", tool.tool.Description)

	result, contentType, err := tool.handler(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, []byte("v2"), result)
	require.Equal(t, "text/plain", contentType)
}

func TestMCPToolRegistry_HandlerExecution(t *testing.T) {
	r := NewMCPToolRegistry()

	handler := func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		name, _ := input["name"].(string)
		return []byte("Hello, " + name), "text/plain", nil
	}

	r.RegisterTool("greet", "Greet tool", nil, handler)
	tool := r.GetTool("greet")
	require.NotNil(t, tool)

	result, contentType, err := tool.handler(context.Background(), map[string]any{"name": "World"})
	require.NoError(t, err)
	require.Equal(t, []byte("Hello, World"), result)
	require.Equal(t, "text/plain", contentType)
}

func TestMCPToolRegistry_InputSchema(t *testing.T) {
	r := NewMCPToolRegistry()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "integer"},
		},
	}

	r.RegisterTool(
		"fetch",
		"Fetch resource",
		schema,
		func(ctx context.Context, input map[string]any) ([]byte, string, error) {
			return nil, "", nil
		},
	)

	tool := r.GetTool("fetch")
	require.NotNil(t, tool)
	require.Equal(t, schema, tool.tool.InputSchema)
}

func TestWrapIfArray(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string // 期待するJSON（比較は正規化して行う）
		wantErr bool
	}{
		{
			name:  "単純な配列はラップされる",
			input: `[1,2,3]`,
			want:  `{"items":[1,2,3]}`,
		},
		{
			name:  "オブジェクトはそのまま返る",
			input: `{"foo":"bar"}`,
			want:  `{"foo":"bar"}`,
		},
		{
			name:  "文字列（スカラー値）はそのまま返る",
			input: `"hello"`,
			want:  `"hello"`,
		},
		{
			name:  "数値はそのまま返る",
			input: `123`,
			want:  `123`,
		},
		{
			name:  "先頭に空白がある配列もラップされる",
			input: "   [1,2,3]",
			want:  `{"items":[1,2,3]}`,
		},
		{
			name: "先頭に改行がある配列もラップされる",
			input: `
			   [1,2,3]
			`,
			want: `{"items":[1,2,3]}`,
		},
		{
			name:  "空の配列もラップされる",
			input: `[]`,
			want:  `{"items":[]}`,
		},
		{
			name:  "ネストした配列もラップされる",
			input: `[[1,2],[3,4]]`,
			want:  `{"items":[[1,2],[3,4]]}`,
		},
		{
			name:    "壊れたJSON配列はエラーになる",
			input:   `[1,2,`,
			wantErr: true,
		},
		{
			name:  "空文字列はそのまま返る",
			input: ``,
			want:  ``,
		},
		{
			name:  "空白のみはそのまま返る",
			input: `   `,
			want:  `   `,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wrapIfArray([]byte(tt.input))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("エラーを期待していたが nil だった: input=%q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("予期しないエラー: %v (input=%q)", err, tt.input)
			}

			// 空文字列/空白のケースはそのままバイト比較
			if tt.input == "" || bytes.TrimSpace([]byte(tt.input)) == nil && tt.input != "" {
				if string(got) != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
				return
			}

			// JSONとして意味的に等しいかを比較（キー順やスペースの違いを無視）
			var gotVal, wantVal any
			if len(bytes.TrimSpace(got)) > 0 {
				if err := json.Unmarshal(got, &gotVal); err != nil {
					t.Fatalf("got のJSONパースに失敗: %v (got=%q)", err, got)
				}
			}
			if len(bytes.TrimSpace([]byte(tt.want))) > 0 {
				if err := json.Unmarshal([]byte(tt.want), &wantVal); err != nil {
					t.Fatalf("want のJSONパースに失敗: %v (want=%q)", err, tt.want)
				}
			}

			gotNorm, _ := json.Marshal(gotVal)
			wantNorm, _ := json.Marshal(wantVal)
			if string(gotNorm) != string(wantNorm) {
				t.Errorf("got %s, want %s", gotNorm, wantNorm)
			}
		})
	}
}

// nilスライスを渡した場合の挙動も確認しておく
func TestWrapIfArray_NilInput(t *testing.T) {
	got, err := wrapIfArray(nil)
	if err != nil {
		t.Fatalf("予期しないエラー: %v", err)
	}
	if got != nil {
		t.Errorf("nil入力に対してnilではない結果が返った: %q", got)
	}
}
