package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

func DoRequest(ctx context.Context, client *http.Client, finalURL, httpMethod string, withBody bool, bodyBytes []byte, bodyContentType string, effective_headers map[string]string) (_ *http.Response, rErr error) {
	ctx = trace.StartSpan(ctx, "api/DoRequest")
	defer func() { trace.EndSpan(ctx, rErr) }()

	var bodyReader io.Reader
	if withBody && len(bodyBytes) > 0 {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req, reqErr := http.NewRequestWithContext(ctx, strings.ToUpper(httpMethod), finalURL, bodyReader)
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

// DoRequestWithBody is like DoRequest but accepts an io.Reader for the request body
// instead of a fully-buffered []byte, so large payloads (e.g. a multipart body streamed
// from an io.Pipe) do not need to be materialized in memory before sending.
//
// When body's length can't be determined by net/http (the common case for io.Pipe-based
// readers, since they aren't one of the special-cased *bytes.Buffer / *bytes.Reader /
// *strings.Reader types), http.NewRequestWithContext leaves Request.ContentLength at its
// zero value; per Request.outgoingLength, a non-nil Body combined with ContentLength == 0
// is treated as "unknown length", so the Transport sends the request using chunked
// Transfer-Encoding. This also means Request.GetBody stays nil, so the Client cannot
// transparently retry the request body on redirects the way it can for the []byte-backed
// DoRequest — an inherent trade-off of streaming an unbuffered source.
func DoRequestWithBody(ctx context.Context, client *http.Client, finalURL, httpMethod string, withBody bool, body io.Reader, bodyContentType string, effective_headers map[string]string) (_ *http.Response, rErr error) {
	ctx = trace.StartSpan(ctx, "api/DoRequest")
	defer func() { trace.EndSpan(ctx, rErr) }()

	var bodyReader io.Reader
	if withBody && body != nil {
		bodyReader = body
	}
	req, reqErr := http.NewRequestWithContext(ctx, strings.ToUpper(httpMethod), finalURL, bodyReader)
	if reqErr != nil {
		return nil, reqErr
	}
	if withBody && body != nil && bodyContentType != "" {
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
