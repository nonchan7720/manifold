package oasbreaking

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

const baseSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Petstore", "version": "1.0.0"},
  "paths": {
    "/pet/{petId}": {
      "get": {
        "operationId": "getPetById",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/pet/{petId}/uploadImage": {
      "post": {
        "operationId": "uploadFile",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

// requiredParamSpec is baseSpec with a new required query parameter on
// getPetById and uploadFile removed.
const requiredParamSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Petstore", "version": "1.0.0"},
  "paths": {
    "/pet/{petId}": {
      "get": {
        "operationId": "getPetById",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "verbose", "in": "query", "required": true, "schema": {"type": "boolean"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

// addedOperationSpec is baseSpec with an extra DELETE /pet/{petId}.
const addedOperationSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Petstore", "version": "1.0.0"},
  "paths": {
    "/pet/{petId}": {
      "get": {
        "operationId": "getPetById",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "ok"}}
      },
      "delete": {
        "operationId": "deletePet",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/pet/{petId}/uploadImage": {
      "post": {
        "operationId": "uploadFile",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func loadSpec(t *testing.T, data string) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData([]byte(data))
	require.NoError(t, err)
	return doc
}

func TestCheck_NoChanges(t *testing.T) {
	changes, err := Check(loadSpec(t, baseSpec), loadSpec(t, baseSpec))
	require.NoError(t, err)
	require.Empty(t, changes)
	require.Equal(t, LevelNone, MaxLevel(changes))
}

func TestCheck_BreakingChanges(t *testing.T) {
	changes, err := Check(loadSpec(t, baseSpec), loadSpec(t, requiredParamSpec))
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	require.Equal(t, LevelErr, MaxLevel(changes))

	ResolveTools(changes, map[string]string{
		"GET /pet/{petId}":              "getpetbyid",
		"POST /pet/{petId}/uploadImage": "uploadfile",
	})

	byID := map[string]Change{}
	for _, c := range changes {
		byID[c.ID] = c
	}
	param, ok := byID["new-required-request-parameter"]
	require.True(t, ok, "changes: %+v", changes)
	require.Equal(t, LevelErr, param.Level)
	require.Equal(t, "GET /pet/{petId}", param.Operation)
	require.Equal(t, "getpetbyid", param.Tool)
	require.Contains(t, param.Message, "verbose")

	removed, ok := byID["api-path-removed-without-deprecation"]
	require.True(t, ok, "changes: %+v", changes)
	require.Equal(t, "POST /pet/{petId}/uploadImage", removed.Operation)
	require.Equal(t, "uploadfile", removed.Tool)

	// Most severe first.
	for i := 1; i < len(changes); i++ {
		require.GreaterOrEqual(t, changes[i-1].Level, changes[i].Level)
	}
}

func TestCheck_NonBreakingAddition(t *testing.T) {
	changes, err := Check(loadSpec(t, baseSpec), loadSpec(t, addedOperationSpec))
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	require.Equal(t, LevelInfo, MaxLevel(changes))
	require.Equal(t, Counts{Info: len(changes)}, Count(changes))
	require.Equal(t, "DELETE /pet/{petId}", changes[0].Operation)
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{
		"ERR": LevelErr, "err": LevelErr, "WARN": LevelWarn, "info": LevelInfo,
		"": LevelNone, "none": LevelNone,
	} {
		got, err := ParseLevel(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := ParseLevel("fatal")
	require.Error(t, err)
}
