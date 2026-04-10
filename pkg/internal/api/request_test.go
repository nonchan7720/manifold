package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequest_GET(t *testing.T) {
	var receivedMethod, receivedCustomHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedCustomHeader = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`)) //nolint: errcheck
	}))
	defer server.Close()

	resp, err := DoRequest(
		context.Background(),
		&http.Client{},
		server.URL+"/test",
		"get",
		false,
		nil,
		"",
		map[string]string{"X-Api-Key": "secret"},
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.MethodGet, receivedMethod)
	assert.Equal(t, "secret", receivedCustomHeader)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "ok")
}

func TestDoRequest_POST_WithBody(t *testing.T) {
	var receivedContentType string
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	bodyBytes := []byte(`{"name":"test"}`)
	resp, err := DoRequest(
		context.Background(),
		&http.Client{},
		server.URL+"/create",
		"post",
		true,
		bodyBytes,
		"application/json",
		nil,
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "application/json", receivedContentType)
	assert.Equal(t, bodyBytes, receivedBody)
}

func TestDoRequest_POST_NoBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Empty(t, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// withBody=trueでもbodyBytesが空ならbodyなし
	resp, err := DoRequest(
		context.Background(),
		&http.Client{},
		server.URL+"/post",
		"post",
		true,
		nil,
		"application/json",
		nil,
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDoRequest_PUT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := DoRequest(
		context.Background(),
		&http.Client{},
		server.URL+"/update",
		"put",
		true,
		[]byte(`{"val":1}`),
		"application/json",
		nil,
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDoRequest_DELETE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	resp, err := DoRequest(
		context.Background(),
		&http.Client{},
		server.URL+"/delete/1",
		"delete",
		false,
		nil,
		"",
		nil,
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDoRequest_PATCH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := DoRequest(
		context.Background(),
		&http.Client{},
		server.URL+"/patch/1",
		"patch",
		true,
		[]byte(`{"name":"updated"}`),
		"application/json",
		nil,
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDoRequest_WithContextHeaders(t *testing.T) {
	var receivedContextHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContextHeader = r.Header.Get("X-Tenant-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := contexts.ToHeaderContext(context.Background(), map[string][]string{
		"X-Tenant-Id": {"tenant-xyz"},
	})

	resp, err := DoRequest(ctx, &http.Client{}, server.URL, "get", false, nil, "", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-xyz", receivedContextHeader)
}

func TestDoRequest_InvalidURL(t *testing.T) {
	_, err := DoRequest(
		context.Background(),
		&http.Client{},
		"://invalid-url",
		"get",
		false,
		nil,
		"",
		nil,
	)
	assert.Error(t, err)
}
