package mcpsrv

import (
	"context"
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
