package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

func DoRequest(ctx context.Context, client *http.Client, finalURL, httpMethod string, withBody bool, bodyBytes []byte, bodyContentType string, effective_headers map[string]string) (*http.Response, error) {
	var bodyReader io.Reader
	if withBody && len(bodyBytes) > 0 {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req, reqErr := http.NewRequest(strings.ToUpper(httpMethod), finalURL, bodyReader)
	if reqErr != nil {
		return nil, reqErr
	}
	if withBody && len(bodyBytes) > 0 && bodyContentType != "" {
		req.Header.Set("Content-Type", bodyContentType)
	}
	for k, v := range effective_headers {
		req.Header.Set(k, v)
	}
	requestHeader := contexts.FromHeaderContext(ctx)
	for k, values := range requestHeader {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	return client.Do(req)
}
