package oastomcptool

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func paths(fields []BinaryField) [][]string {
	var out [][]string
	for _, f := range fields {
		out = append(out, f.Path)
	}
	return out
}

func TestResponseBinaryFields_OpenAPI3(t *testing.T) {
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{
	  "/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}},
	  "/b":{"get":{"responses":{"200":{"description":"ok","content":{"application/vnd.x+json":{"schema":{"type":"array","items":{"type":"object","properties":{"f":{"type":"string","format":"binary","contentMediaType":"image/png"}}}}}}}}}},
	  "/c":{"get":{"responses":{"200":{"description":"ok","content":{"image/png":{"schema":{"type":"string","format":"binary"}}}}}}},
	  "/d":{"get":{"responses":{"400":{"description":"err","content":{"application/json":{"schema":{"type":"object","properties":{"f":{"type":"string","format":"binary"}}}}}}}}},
	  "/e":{"get":{"responses":{"200":{"description":"ok","content":{"text/plain":{"schema":{"type":"object","properties":{"f":{"type":"string","format":"binary"}}}}}}}}}
	},"components":{"schemas":{
	  "A":{"type":"object","properties":{"x":{"type":"string","format":"binary"},"n":{"$ref":"#/components/schemas/N"},"self":{"$ref":"#/components/schemas/A"}}},
	  "N":{"type":"object","properties":{"list":{"type":"array","items":{"type":"string","format":"binary"}}}}
	}}}`
	doc, err := openapi3.NewLoader().LoadFromData([]byte(spec))
	require.NoError(t, err)

	tests := []struct {
		name string
		path string
		want [][]string
		mt   string
	}{
		{
			"ref, nested, array, recursive",
			"/a",
			[][]string{{"n", "list", BinaryArrayItem}, {"x"}},
			"",
		},
		{
			"+json array of objects with media type",
			"/b",
			[][]string{{BinaryArrayItem, "f"}},
			"image/png",
		},
		{"whole body binary is not a field", "/c", nil, ""},
		{"non-2xx ignored", "/d", nil, ""},
		{"non-json ignored", "/e", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResponseBinaryFields(doc.Paths.Find(tt.path).Get)
			require.ElementsMatch(t, tt.want, paths(got))
			if tt.mt != "" {
				require.Equal(t, tt.mt, got[0].MediaType)
			}
		})
	}
}

func TestResponseBinaryFieldsSwagger(t *testing.T) {
	const spec = `{"swagger":"2.0","info":{"title":"t","version":"1"},"paths":{
	  "/a":{"get":{"produces":["application/json"],"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/A"}}}}},
	  "/b":{"get":{"produces":["image/png"],"responses":{"200":{"description":"ok","schema":{"type":"file"}}}}}
	},"definitions":{
	  "A":{"type":"object","properties":{"x":{"type":"file"},"y":{"type":"string","format":"binary"},"l":{"type":"array","items":{"$ref":"#/definitions/B"}}}},
	  "B":{"type":"object","properties":{"z":{"type":"string","format":"binary"},"a":{"$ref":"#/definitions/A"}}}
	}}`
	var doc openapi2.T
	require.NoError(t, doc.UnmarshalJSON([]byte(spec)))

	got := ResponseBinaryFieldsSwagger(doc.Paths["/a"].Get, &doc)
	require.ElementsMatch(t, [][]string{
		{"x"}, {"y"}, {"l", BinaryArrayItem, "z"},
	}, paths(got))
	require.Empty(t, ResponseBinaryFieldsSwagger(doc.Paths["/b"].Get, &doc))
}
