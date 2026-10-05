package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// viper は tools.overrides のキーを小文字化するが、ツール名は大文字小文字を
// 区別するので、設定ファイルの表記へ戻して返す。
func TestLoadInternal_ToolOverrides_PreserveKeyCase(t *testing.T) {
	t.Setenv("TEST_TOK", "secret")
	cfg, err := loadFromYAML(t, `
mcpServers:
  petstore:
    description: ${TEST_TOK}
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    tools:
      overrides:
        getPetById:
          name: get_pet
        listDocs:
          tool: ListDocs
          name: list_docs
`)
	require.NoError(t, err)
	got := cfg.MCPServer["petstore"].Tools.ResolvedOverrides()
	require.Equal(t, "get_pet", got["getPetById"].Name)
	require.Equal(t, "list_docs", got["ListDocs"].Name)
	require.NotContains(t, got, "getpetbyid")
}

func TestLoadInternal_ToolOverrides_KeysDifferingByCaseAreAnError(t *testing.T) {
	_, err := loadFromYAML(t, `
mcpServers:
  petstore:
    description: p
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    tools:
      overrides:
        getPet:
          name: a
        getpet:
          name: b
`)
	require.ErrorContains(t, err, "differ only by case")
}
