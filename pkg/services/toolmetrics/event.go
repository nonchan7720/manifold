// Package toolmetrics は tools/call 1 回ごとに Event を記録し、キューサービスへ
// 非同期で送信する。キューが遅い・止まっている場合でもイベントは破棄しない。
package toolmetrics

import (
	"context"
	"time"
)

// Status は tools/call の終わり方の分類。
type Status string

const (
	// StatusSuccess: ハンドラーが isError: false の結果を返した。
	StatusSuccess Status = "success"
	// StatusToolError: ハンドラーが isError: true の結果を返した
	// （ツール自身が失敗を報告した。例: 上流の 4xx/5xx）。
	StatusToolError Status = "tool_error"
	// StatusError: ハンドラーが JSON-RPC エラーを返した
	// （認可拒否・未知のツール・バックエンドに接続できない等）。
	StatusError Status = "error"
)

// Event は tools/call 1 回分の記録。JSON にしてキューのメッセージ本文に入れる。
type Event struct {
	// ID はイベントごとに一意。再送で重複した場合に受信側で排除するのに使う。
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Server    string    `json:"server"`
	Service   string    `json:"service"`
	Tool      string    `json:"tool"`
	User      string    `json:"user,omitempty"`
	Status    Status    `json:"status"`
	// DurationMs はハンドラーチェーンでかかった時間。
	DurationMs int64 `json:"durationMs"`
	// ErrorCode は JSON-RPC のエラーコード。StatusError の場合のみ設定する。
	ErrorCode int64 `json:"errorCode,omitempty"`
	// ErrorMessage は JSON-RPC エラーのメッセージ（StatusError）または
	// 結果のテキスト（StatusToolError）。MaxErrorMessageLength バイトで切り詰める。
	ErrorMessage string `json:"errorMessage,omitempty"`
	TraceID      string `json:"traceId,omitempty"`
}

// MaxErrorMessageLength は Event.ErrorMessage の上限バイト数。大きなエラー本文で
// キューのメッセージサイズ上限（SQS はバッチあたり 256KiB）を超えないようにする。
const MaxErrorMessageLength = 1024

// Publisher はイベントのバッチをキューサービスへ送る。Publish は Recorder の
// 単一のワーカー goroutine からのみ呼ばれる。
type Publisher interface {
	Publish(ctx context.Context, events []Event) error
	Close() error
}
