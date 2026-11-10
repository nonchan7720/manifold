package api

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// DoRequestWithBody sends body (when non-nil) regardless of httpMethod: some APIs take a
// request body on GET / DELETE, so whether to send one is the caller's decision.
//
// DoRequestWithBody accepts an io.Reader for the request body instead of a
// fully-buffered []byte, so large payloads (e.g. a multipart body streamed
// from an io.Pipe) do not need to be materialized in memory before sending.
//
// When body's length can't be determined by net/http (the common case for io.Pipe-based
// readers, since they aren't one of the special-cased *bytes.Buffer / *bytes.Reader /
// *strings.Reader types), http.NewRequestWithContext leaves Request.ContentLength at its
// zero value; per Request.outgoingLength, a non-nil Body combined with ContentLength == 0
// is treated as "unknown length", so the Transport sends the request using chunked
// Transfer-Encoding. This also means Request.GetBody stays nil, so the Client cannot
// transparently retry the request body on redirects the way it can for a []byte-backed
// body — an inherent trade-off of streaming an unbuffered source.
func DoRequestWithBody(
	ctx context.Context,
	client *http.Client,
	finalURL, httpMethod string,
	body io.Reader,
	bodyContentType string,
	effective_headers map[string]string,
) (_ *http.Response, rErr error) {
	ctx = trace.StartSpan(ctx, "api/DoRequest")
	defer func() { trace.EndSpan(ctx, rErr) }()

	req, reqErr := http.NewRequestWithContext(
		ctx,
		strings.ToUpper(httpMethod),
		finalURL,
		body,
	)
	if reqErr != nil {
		return nil, reqErr
	}
	if body != nil && bodyContentType != "" {
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
