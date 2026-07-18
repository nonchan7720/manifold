package oastomcptool

import (
	"io"
	"mime/multipart"
)

// newMultipartStreamBody starts a goroutine that populates a *multipart.Writer via
// write and pipes its output directly to the returned io.ReadCloser, so the caller can
// stream a multipart request body (which may include large file uploads, up to
// FileFetchConfig.MaxSize each) to an http.Client without first buffering the whole
// payload into memory (see CreateToolFunction / CreateToolFunctionSwagger, which
// previously built the entire body in a bytes.Buffer).
//
// write is invoked with the multipart.Writer on the goroutine; whatever it writes is
// piped to the reader as it's produced. Any error write returns is delivered to the
// reader side via CloseWithError, so a consumer such as io.Copy or http.Client.Do
// observes the same error writeMultipartValue/writeMultipartFile would have produced had
// the caller run them synchronously against a buffer. The trailing multipart boundary
// (mw.Close) is written automatically after write returns; its error is only surfaced
// when write itself succeeded, so a real field-write failure is never masked by a
// secondary "write to a now-broken pipe" error from closing the boundary.
//
// The goroutine always terminates, even if the request is abandoned mid-stream: once the
// returned io.ReadCloser is closed (by the caller's own cleanup, or because
// net/http.Transport closes a request's Body once it's done with it — on both success and
// failure), any subsequent write into the pipe (inside write's call chain, e.g. via
// writeMultipartValue's io.Copy or part.Write) returns io.ErrClosedPipe, which propagates
// back out as write's error, and the goroutine exits promptly instead of blocking on a
// pipe nobody will ever drain again.
func newMultipartStreamBody(write func(mw *multipart.Writer) error) (io.ReadCloser, string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	go func() {
		err := write(mw)
		if closeErr := mw.Close(); err == nil {
			err = closeErr
		}
		// CloseWithError(nil) behaves like Close(): pending/future pr.Read calls observe
		// io.EOF. A non-nil err surfaces from pr.Read (and therefore from whatever is
		// consuming the reader, e.g. http.Client.Do) as-is.
		_ = pw.CloseWithError(err)
	}()

	return pr, contentType
}
