package oastomcptool

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestResponseShape(t *testing.T) {
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{
	  "/pet":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Pet"}}}}}}},
	  "/pets":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"array","items":{"$ref":"#/components/schemas/Pet"}}}}}}}},
	  "/page":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{
	    "total":{"type":"integer"},"items":{"type":"array","items":{"$ref":"#/components/schemas/Pet"}}}}}}}}}},
	  "/employee":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Employee"}}}}}}},
	  "/either":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{
	    "oneOf":[{"$ref":"#/components/schemas/Category"},{"type":"object","properties":{"error":{"type":"string"}}}]}}}}}}},
	  "/statuses":{"post":{"responses":{
	    "400":{"description":"bad","content":{"application/json":{"schema":{"type":"object","properties":{"error":{"type":"string"}}}}}},
	    "202":{"description":"accepted","content":{"application/json":{"schema":{"type":"object","properties":{"job":{"type":"string"}}}}}},
	    "201":{"description":"created","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}}}}}},
	    "200":{"description":"no body"}}}},
	  "/media":{"get":{"responses":{"200":{"description":"ok","content":{
	    "application/vnd.b+json":{"schema":{"type":"object","properties":{"b":{"type":"string"}}}},
	    "application/vnd.a+json":{"schema":{"type":"object","properties":{"a":{"type":"string"}}}},
	    "text/plain":{"schema":{"type":"string"}},
	    "application/json; charset=utf-8":{"schema":{"type":"object","properties":{"plain":{"type":"string"}}}}}}}}},
	  "/hal":{"get":{"responses":{"200":{"description":"ok","content":{
	    "application/hal+json":{"schema":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}}}},
	  "/text":{"get":{"responses":{"200":{"description":"ok","content":{"text/plain":{"schema":{"type":"string"}}}}}}},
	  "/error-only":{"get":{"responses":{"404":{"description":"nope","content":{"application/json":{"schema":{"type":"object","properties":{"error":{"type":"string"}}}}}}}}},
	  "/scalar":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"string"}}}}}}}
	},"components":{"schemas":{
	  "Pet":{"type":"object","properties":{
	    "id":{"type":"integer"},"name":{"type":"string"},
	    "category":{"$ref":"#/components/schemas/Category"},
	    "photoUrls":{"type":"array","items":{"type":"string"}},
	    "tags":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},
	    "parent":{"$ref":"#/components/schemas/Pet"}}},
	  "Category":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}},
	  "Person":{"type":"object","properties":{"name":{"type":"string"}}},
	  "Employee":{"allOf":[{"$ref":"#/components/schemas/Person"}],"type":"object","properties":{
	    "manager":{"$ref":"#/components/schemas/Person"},
	    "reports":{"type":"array","items":{"$ref":"#/components/schemas/Employee"}}}}
	}}}`
	doc, err := openapi3.NewLoader().LoadFromData([]byte(spec))
	require.NoError(t, err)

	const pet = "category{id, name}, id, name, parent{...}, photoUrls[], tags[]{id, name}"
	tests := []struct {
		name      string
		path      string
		method    string
		want      string
		mediaType string
	}{
		{"a referenced object, with a recursive field", "/pet", "GET", pet, "application/json"},
		{"an array of objects", "/pets", "GET", "[]{" + pet + "}", "application/json"},
		{
			"an object with a list",
			"/page",
			"GET",
			"items[]{" + pet + "}, total",
			"application/json",
		},
		{
			"allOf is read with the schema's own fields",
			"/employee", "GET", "manager{name}, name, reports[]{...}", "application/json",
		},
		{
			"oneOf branches are read together",
			"/either",
			"GET",
			"error, id, name",
			"application/json",
		},
		{"the lowest 2xx with a JSON body", "/statuses", "POST", "id", "application/json"},
		{
			"application/json is preferred over +json",
			"/media", "GET", "plain", "application/json; charset=utf-8",
		},
		{"+json alone", "/hal", "GET", "[]{id}", "application/hal+json"},
		{"a JSON scalar has no fields", "/scalar", "GET", "", "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shape, mediaType, ok := ResponseShape(doc.Paths.Find(tt.path).GetOperation(tt.method))
			require.True(t, ok)
			require.Equal(t, tt.want, shape.Describe(1<<20))
			require.Equal(t, tt.mediaType, mediaType)
		})
	}

	for _, path := range []string{"/text", "/error-only"} {
		_, _, ok := ResponseShape(doc.Paths.Find(path).Get)
		require.False(t, ok, path)
	}
	_, _, ok := ResponseShape(nil)
	require.False(t, ok)
	_, _, ok = ResponseShape(&openapi3.Operation{})
	require.False(t, ok)
}

func TestResponseShapeSwagger(t *testing.T) {
	const spec = `{"swagger":"2.0","info":{"title":"t","version":"1"},"produces":["application/json"],"paths":{
	  "/pet":{"get":{"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Pet"}}}}},
	  "/pets":{"get":{"responses":{"200":{"description":"ok","schema":{"type":"array","items":{"$ref":"#/definitions/Pet"}}}}}},
	  "/employee":{"get":{"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Employee"}}}}},
	  "/statuses":{"post":{"responses":{
	    "default":{"description":"error","schema":{"type":"object","properties":{"error":{"type":"string"}}}},
	    "201":{"description":"created","schema":{"type":"object","properties":{"id":{"type":"integer"}}}},
	    "200":{"description":"no body"}}}},
	  "/hal":{"get":{"produces":["text/plain","application/hal+json"],"responses":{"200":{"description":"ok","schema":{"type":"object","properties":{"id":{"type":"integer"}}}}}}},
	  "/xml":{"get":{"produces":["application/xml"],"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Pet"}}}}},
	  "/file":{"get":{"responses":{"200":{"description":"ok","schema":{"type":"file"}}}}},
	  "/missing":{"get":{"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Nope"}}}}}
	},"definitions":{
	  "Pet":{"type":"object","properties":{
	    "id":{"type":"integer"},"name":{"type":"string"},
	    "category":{"$ref":"#/definitions/Category"},
	    "tags":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
	    "parent":{"$ref":"#/definitions/Pet"}}},
	  "Category":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}},
	  "Person":{"type":"object","properties":{"name":{"type":"string"}}},
	  "Employee":{"allOf":[{"$ref":"#/definitions/Person"},{"type":"object","properties":{
	    "manager":{"$ref":"#/definitions/Person"},
	    "reports":{"type":"array","items":{"$ref":"#/definitions/Employee"}}}}]}
	}}`
	doc, err := ParseSwaggerSpec(t.Context(), []byte(spec))
	require.NoError(t, err)

	const pet = "category{id, name}, id, name, parent{...}, tags[]{name}"
	tests := []struct {
		name      string
		path      string
		method    string
		want      string
		mediaType string
	}{
		{"a referenced object, with a recursive field", "/pet", "GET", pet, "application/json"},
		{"an array of objects", "/pets", "GET", "[]{" + pet + "}", "application/json"},
		{
			"allOf is read with the schema's own fields",
			"/employee", "GET", "manager{name}, name, reports[]{...}", "application/json",
		},
		{"the lowest 2xx with a schema", "/statuses", "POST", "id", "application/json"},
		{"the operation's produces", "/hal", "GET", "id", "application/hal+json"},
		{"a file has no fields", "/file", "GET", "", "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operation := doc.Paths[tt.path].GetOperation(tt.method)
			shape, mediaType, ok := ResponseShapeSwagger(operation, doc)
			require.True(t, ok)
			require.Equal(t, tt.want, shape.Describe(1<<20))
			require.Equal(t, tt.mediaType, mediaType)
		})
	}

	for _, path := range []string{"/xml", "/missing"} {
		_, _, ok := ResponseShapeSwagger(doc.Paths[path].Get, doc)
		require.False(t, ok, path)
	}
	_, _, ok := ResponseShapeSwagger(nil, doc)
	require.False(t, ok)

	// produces がどこにも無ければ、JSON で返るものとして読む。
	doc.Produces = nil
	shape, mediaType, ok := ResponseShapeSwagger(doc.Paths["/pet"].Get, doc)
	require.True(t, ok)
	require.Equal(t, pet, shape.Describe(1<<20))
	require.Equal(t, "application/json", mediaType)
}
