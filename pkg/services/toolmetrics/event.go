// Package toolmetrics records one Event per tools/call and ships them to a
// queue service asynchronously, so that a slow or unavailable queue never
// delays the tool call itself.
package toolmetrics

import (
	"context"
	"time"
)

// Status classifies how a tools/call ended.
type Status string

const (
	// StatusSuccess: the handler returned a result whose isError is false.
	StatusSuccess Status = "success"
	// StatusToolError: the handler returned a result with isError: true
	// (the tool itself reported a failure, e.g. an upstream 4xx/5xx).
	StatusToolError Status = "tool_error"
	// StatusError: the handler returned a JSON-RPC error (authz denial,
	// unknown tool, backend unreachable, ...).
	StatusError Status = "error"
)

// Event is one tools/call, serialized as JSON into the queue message body.
type Event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Server    string    `json:"server"`
	Service   string    `json:"service"`
	Tool      string    `json:"tool"`
	User      string    `json:"user,omitempty"`
	Status    Status    `json:"status"`
	// DurationMs is the wall-clock time spent in the handler chain.
	DurationMs int64 `json:"durationMs"`
	// ErrorCode is the JSON-RPC error code; set only for StatusError.
	ErrorCode int64 `json:"errorCode,omitempty"`
	// ErrorMessage is the JSON-RPC error message (StatusError) or the
	// result's text content (StatusToolError), truncated to
	// MaxErrorMessageLength bytes.
	ErrorMessage string `json:"errorMessage,omitempty"`
	TraceID      string `json:"traceId,omitempty"`
}

// MaxErrorMessageLength caps Event.ErrorMessage so a large error body does
// not blow the queue's message size limit (SQS: 256KiB per batch).
const MaxErrorMessageLength = 1024

// Publisher delivers a batch of events to a queue service. Publish is only
// ever called from the Recorder's single worker goroutine.
type Publisher interface {
	Publish(ctx context.Context, events []Event) error
	Close() error
}
