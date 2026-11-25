package oasbreaking

import (
	"strings"
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

var versioningIDs = map[string]bool{
	"api-version-not-bumped":       true,
	"api-version-decreased":        true,
	"api-major-version-not-bumped": true,
}

// TestCheck_VersioningPolicy pins the versioning policy changes oasdiff
// reported as checker rules up to v1.37: one info-level spec-wide change when
// a breaking change ships without a major version increase.
func TestCheck_VersioningPolicy(t *testing.T) {
	withVersion := func(spec, version string) string {
		return strings.Replace(spec, `"version": "1.0.0"`, `"version": "`+version+`"`, 1)
	}
	tests := []struct {
		name            string
		base, revision  string
		wantID, wantMsg string
	}{
		{
			name: "not bumped", base: "1.0.0", revision: "1.0.0",
			wantID:  "api-version-not-bumped",
			wantMsg: "a breaking change was detected but the version is still `1.0.0`",
		},
		{
			name:     "decreased",
			base:     "1.2.0",
			revision: "1.1.0",
			wantID:   "api-version-decreased",
			wantMsg:  "a breaking change was detected but the version decreased from `1.2.0` to `1.1.0`",
		},
		{
			name:     "major not bumped",
			base:     "1.0.0",
			revision: "1.1.0",
			wantID:   "api-major-version-not-bumped",
			wantMsg:  "a breaking change was detected but the major version did not increase, from `1.0.0` to `1.1.0`",
		},
		{name: "major bumped", base: "1.0.0", revision: "2.0.0"},
		{
			name:     "pre-1.0 patch bumped",
			base:     "0.2.0",
			revision: "0.2.1",
			wantID:   "api-major-version-not-bumped",
			wantMsg:  "a breaking change was detected but the major version did not increase, from `0.2.0` to `0.2.1`",
		},
		{name: "pre-1.0 minor bumped", base: "0.2.0", revision: "0.3.0"},
		{name: "not semver", base: "2024-01-01", revision: "2024-01-01"},
		{name: "only base is semver", base: "1.0.0", revision: "2024-01-01"},
		{name: "only revision is semver", base: "2024-01-01", revision: "1.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes, err := Check(
				loadSpec(t, withVersion(baseSpec, tt.base)),
				loadSpec(t, withVersion(requiredParamSpec, tt.revision)),
			)
			require.NoError(t, err)
			require.Equal(t, LevelErr, MaxLevel(changes))
			var got []Change
			for _, c := range changes {
				if versioningIDs[c.ID] {
					got = append(got, c)
				}
			}
			if tt.wantID == "" {
				require.Empty(t, got)
				return
			}
			require.Equal(t, []Change{{Level: LevelInfo, ID: tt.wantID, Message: tt.wantMsg}}, got)
		})
	}
}

// TestCheck_VersioningPolicy_NoBreakingChange: a non-breaking change needs
// no version increase.
func TestCheck_VersioningPolicy_NoBreakingChange(t *testing.T) {
	changes, err := Check(loadSpec(t, baseSpec), loadSpec(t, addedOperationSpec))
	require.NoError(t, err)
	for _, c := range changes {
		require.False(t, versioningIDs[c.ID], "changes: %+v", changes)
	}
}

func TestCheck_VersioningPolicy_WarningOnly(t *testing.T) {
	base := loadSpec(t, baseSpec)
	op := base.Paths.Value("/pet/{petId}").Get
	op.AddParameter(openapi3.NewQueryParameter("verbose").WithSchema(openapi3.NewBoolSchema()))

	changes, err := Check(base, loadSpec(t, baseSpec))
	require.NoError(t, err)
	require.Equal(t, LevelWarn, MaxLevel(changes))
	require.Len(t, changes, 1)
	require.Equal(t, "request-parameter-removed", changes[0].ID)
	require.Equal(t, "GET /pet/{petId}", changes[0].Operation)
}

func TestCheck_VersioningPolicy_VersionOnly(t *testing.T) {
	revision := loadSpec(t, baseSpec)
	revision.Info.Version = "1.1.0"

	changes, err := Check(loadSpec(t, baseSpec), revision)
	require.NoError(t, err)
	require.Equal(t, []Change{{
		Level:   LevelInfo,
		ID:      "api-version-changed",
		Message: "the API version changed from `1.0.0` to `1.1.0`",
	}}, changes)
}
