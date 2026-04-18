package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterOpenAPI(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", "")
	require.NoError(t, err)
	require.NotNil(t, r)

	// OAS3 fixture には 19 オペレーションが定義されている
	tools := r.ListTools()
	require.Len(t, tools, 19)
}

func TestRegisterSwagger(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_swagger.json", "")
	require.NoError(t, err)
	require.NotNil(t, r)

	// Swagger fixture には 20 オペレーションが定義されている
	tools := r.ListTools()
	require.Len(t, tools, 20)
}

func TestRegisterOpenAPI_Error_InvalidPath(t *testing.T) {
	_, err := RegisterOpenAPI(t.Context(), "fixtures/nonexistent.json", "")
	require.Error(t, err)
}

func TestRegisterSwagger_Error_InvalidPath(t *testing.T) {
	_, err := RegisterOpenAPI(t.Context(), "fixtures/nonexistent_swagger.json", "")
	require.Error(t, err)
}

func TestRegisterOpenAPI_ToolNaming(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", "")
	require.NoError(t, err)

	// operationId を小文字化したものがツール名になる
	cases := []string{
		"updatepet",
		"addpet",
		"findpetsbystatus",
		"findpetsbytags",
		"getpetbyid",
		"updatepetwithform",
		"deletepet",
		"uploadfile",
		"getinventory",
		"placeorder",
		"getorderbyid",
		"deleteorder",
		"createuser",
		"createuserswithlistinput",
		"loginuser",
		"logoutuser",
		"getuserbyname",
		"updateuser",
		"deleteuser",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			tool := r.GetTool(name)
			require.NotNil(t, tool, "tool %q should be registered", name)
			require.Equal(t, name, tool.tool.Name)
		})
	}
}

func TestRegisterSwagger_ToolNaming(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_swagger.json", "")
	require.NoError(t, err)

	cases := []string{
		"uploadfile",
		"addpet",
		"updatepet",
		"findpetsbystatus",
		"findpetsbytags",
		"getpetbyid",
		"updatepetwithform",
		"deletepet",
		"getinventory",
		"placeorder",
		"getorderbyid",
		"deleteorder",
		"createuserswithlistinput",
		"getuserbyname",
		"updateuser",
		"deleteuser",
		"loginuser",
		"logoutuser",
		"createuserswitharrayinput",
		"createuser",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			tool := r.GetTool(name)
			require.NotNil(t, tool, "tool %q should be registered", name)
			require.Equal(t, name, tool.tool.Name)
		})
	}
}

func TestRegisterOpenAPI_Description_UsesSummary(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", "")
	require.NoError(t, err)

	cases := []struct {
		toolName    string
		wantSummary string
	}{
		{"updatepet", "Update an existing pet."},
		{"addpet", "Add a new pet to the store."},
		{"getinventory", "Returns pet inventories by status."},
		{"loginuser", "Logs user into the system."},
		{"logoutuser", "Logs out current logged in user session."},
	}
	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			tool := r.GetTool(tc.toolName)
			require.NotNil(t, tool)
			require.Equal(t, tc.wantSummary, tool.tool.Description)
		})
	}
}

func TestRegisterSwagger_Description_UsesSummary(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_swagger.json", "")
	require.NoError(t, err)

	cases := []struct {
		toolName    string
		wantSummary string
	}{
		{"addpet", "Add a new pet to the store"},
		{"updatepet", "Update an existing pet"},
		{"getinventory", "Returns pet inventories by status"},
		{"loginuser", "Logs user into the system"},
	}
	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			tool := r.GetTool(tc.toolName)
			require.NotNil(t, tool)
			require.Equal(t, tc.wantSummary, tool.tool.Description)
		})
	}
}

func TestRegisterOpenAPI_InputSchema(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", "")
	require.NoError(t, err)

	// getPetById はパスパラメータ petId を持つ
	tool := r.GetTool("getpetbyid")
	require.NotNil(t, tool)

	schema, ok := tool.tool.InputSchema.(map[string]any)
	require.True(t, ok, "InputSchema should be map[string]any")
	require.Equal(t, "object", schema["type"])

	props, propsOk := schema["properties"].(map[string]any)
	require.True(t, propsOk, "properties should be map[string]any")
	require.Contains(t, props, "petId")
}

func TestRegisterSwagger_InputSchema(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_swagger.json", "")
	require.NoError(t, err)

	// getPetById はパスパラメータ petId を持つ
	tool := r.GetTool("getpetbyid")
	require.NotNil(t, tool)

	schema, ok := tool.tool.InputSchema.(map[string]any)
	require.True(t, ok, "InputSchema should be map[string]any")
	require.Equal(t, "object", schema["type"])

	props, propsOk := schema["properties"].(map[string]any)
	require.True(t, propsOk, "properties should be map[string]any")
	require.Contains(t, props, "petId")
}

func TestRegisterOpenAPI_Handler_NotNil(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", "")
	require.NoError(t, err)

	tool := r.GetTool("updatepet")
	require.NotNil(t, tool)
	require.NotNil(t, tool.handler)
}

func TestRegisterOpenAPI_BaseUrl_Override(t *testing.T) {
	customBaseUrl := "https://example.com/api"
	// baseUrl を上書きしてもエラーにならず、ツールが登録されること
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", customBaseUrl)
	require.NoError(t, err)
	require.NotNil(t, r)

	tools := r.ListTools()
	require.Len(t, tools, 19)
}

func TestRegisterOpenAPI_GetTool_NotFound(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_oas.json", "")
	require.NoError(t, err)

	tool := r.GetTool("nonexistenttool")
	require.Nil(t, tool)
}
