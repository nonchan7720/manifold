// Package toolmetrics は tools/call 1 回ごとに Event を記録し、キューサービスへ
// 非同期で送信する。キューが遅い・止まっている場合でもイベントは破棄しない。
//
// イベントのスキーマは proto/manifold/toolmetrics/v1/tool_metrics.proto で管理し、
// Event はそこから生成した型そのものを使う。
package toolmetrics

import (
	"context"

	toolmetricsv1 "github.com/nonchan7720/manifold/pkg/proto/manifold/toolmetrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Event は tools/call 1 回分の記録（toolmetricsv1.ToolCallEvent）。
type Event = toolmetricsv1.ToolCallEvent

// Status は tools/call の終わり方の分類。
type Status = toolmetricsv1.ToolCallStatus

const (
	StatusSuccess   = toolmetricsv1.ToolCallStatus_TOOL_CALL_STATUS_SUCCESS
	StatusToolError = toolmetricsv1.ToolCallStatus_TOOL_CALL_STATUS_TOOL_ERROR
	StatusError     = toolmetricsv1.ToolCallStatus_TOOL_CALL_STATUS_ERROR
)

// ContentType はキューメッセージ本文の形式（Event の protojson 表現）。
const ContentType = "application/json"

// SchemaName はキューメッセージ本文の型の完全修飾名
// （manifold.toolmetrics.v1.ToolCallEvent）。受信側はこれを見てデコードする型を選ぶ。
var SchemaName = string(proto.MessageName((*Event)(nil)))

// MaxErrorMessageLength は Event.ErrorMessage の上限バイト数。大きなエラー本文で
// キューのメッセージサイズ上限（SQS はバッチあたり 256KiB）を超えないようにする。
const MaxErrorMessageLength = 1024

// Marshal は e をキューメッセージ本文（protojson）にする。
func Marshal(e *Event) ([]byte, error) {
	return protojson.Marshal(e)
}

// Publisher はイベントのバッチをキューサービスへ送る。Publish は Recorder の
// 単一のワーカー goroutine からのみ呼ばれる。
type Publisher interface {
	Publish(ctx context.Context, events []*Event) error
	Close() error
}
