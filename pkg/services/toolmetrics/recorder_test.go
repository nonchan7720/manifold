package toolmetrics

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakePublisher は成功したバッチを記録する。failures は次の N 回の Publish を
// 失敗させ、block を設定すると close されるまで Publish を止める。
type fakePublisher struct {
	mu       sync.Mutex
	batches  [][]Event
	attempts int
	failures int
	failAll  bool
	closed   bool
	block    chan struct{}
}

func (p *fakePublisher) Publish(_ context.Context, events []Event) error {
	if p.block != nil {
		<-p.block
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.failAll || p.failures > 0 {
		p.failures--
		return errors.New("queue unavailable")
	}
	p.batches = append(p.batches, append([]Event(nil), events...))
	return nil
}

func (p *fakePublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *fakePublisher) snapshot() ([][]Event, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]Event(nil), p.batches...), p.closed
}

func (p *fakePublisher) total() int {
	batches, _ := p.snapshot()
	n := 0
	for _, b := range batches {
		n += len(b)
	}
	return n
}

func testOptions() RecorderOptions {
	return RecorderOptions{
		BufferSize:     100,
		BatchSize:      3,
		FlushInterval:  time.Hour,
		PublishTimeout: time.Second,
	}
}

// captureLogs は既定の slog ロガーをバッファに書き出すものに差し替える。
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) undelivered() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), "event could not be delivered")
}

func TestRecorder_FlushesWhenBatchIsFull(t *testing.T) {
	pub := &fakePublisher{}
	r := NewRecorder(t.Context(), pub, testOptions())
	t.Cleanup(func() { _ = r.Close(context.Background()) })

	for i := range 3 {
		r.Record(Event{Tool: string(rune('a' + i))})
	}
	require.Eventually(t, func() bool { return pub.total() == 3 }, time.Second, 5*time.Millisecond)
	batches, _ := pub.snapshot()
	require.Len(t, batches, 1)
	require.Equal(t, []string{"a", "b", "c"},
		[]string{batches[0][0].Tool, batches[0][1].Tool, batches[0][2].Tool})
}

func TestRecorder_FlushesOnInterval(t *testing.T) {
	pub := &fakePublisher{}
	opts := testOptions()
	opts.FlushInterval = 10 * time.Millisecond
	r := NewRecorder(t.Context(), pub, opts)
	t.Cleanup(func() { _ = r.Close(context.Background()) })

	r.Record(Event{Tool: "only"})
	require.Eventually(t, func() bool { return pub.total() == 1 }, time.Second, 5*time.Millisecond)
}

func TestRecorder_CloseDrainsBufferAndClosesPublisher(t *testing.T) {
	pub := &fakePublisher{}
	r := NewRecorder(t.Context(), pub, testOptions())

	for range 5 {
		r.Record(Event{})
	}
	require.NoError(t, r.Close(context.Background()))

	require.Equal(t, 5, pub.total())
	_, closed := pub.snapshot()
	require.True(t, closed)
	require.NoError(t, r.Close(context.Background()), "Close is idempotent")
}

func TestRecorder_BlocksInsteadOfDroppingWhenBufferIsFull(t *testing.T) {
	pub := &fakePublisher{block: make(chan struct{})}
	opts := testOptions()
	opts.BufferSize = 2
	opts.BatchSize = 1
	r := NewRecorder(t.Context(), pub, opts)

	// 1 件目でワーカーが Publish に入ってブロックするのを待つ。
	r.Record(Event{})
	require.Eventually(t, func() bool { return len(r.ch) == 0 }, time.Second, time.Millisecond)
	// バッファ（2 件）を埋めると、3 件目の Record は空きが出るまで待つ。
	r.Record(Event{})
	r.Record(Event{})
	returned := make(chan struct{})
	go func() {
		r.Record(Event{})
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("Record must block while the buffer is full")
	case <-time.After(50 * time.Millisecond):
	}

	close(pub.block)
	<-returned
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, 4, pub.total(), "no event is dropped")
}

func TestRecorder_RetriesFailedPublishUntilSuccess(t *testing.T) {
	logs := captureLogs(t)
	pub := &fakePublisher{failures: 2}
	opts := testOptions()
	opts.BatchSize = 1
	r := NewRecorder(t.Context(), pub, opts)

	r.Record(Event{ID: "e1"})
	require.NoError(t, r.Close(context.Background()))

	batches, _ := pub.snapshot()
	require.Len(t, batches, 1)
	require.Equal(t, "e1", batches[0][0].ID)
	require.Equal(t, 3, pub.attempts)
	require.Zero(t, logs.undelivered())
}

func TestRecorder_CloseTimeoutLogsEveryUndeliveredEvent(t *testing.T) {
	logs := captureLogs(t)
	pub := &fakePublisher{failAll: true}
	opts := testOptions()
	opts.BatchSize = 2
	r := NewRecorder(t.Context(), pub, opts)

	// 2 件はワーカーの再試行中のバッチ、残り 3 件はバッファに残る。
	for range 5 {
		r.Record(Event{})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, r.Close(ctx), context.DeadlineExceeded)

	require.Zero(t, pub.total())
	require.Equal(t, 5, logs.undelivered(), "every undelivered event is logged")
	_, closed := pub.snapshot()
	require.True(t, closed)
}

func TestRecorder_RecordAfterCloseIsLogged(t *testing.T) {
	logs := captureLogs(t)
	pub := &fakePublisher{}
	r := NewRecorder(t.Context(), pub, testOptions())
	require.NoError(t, r.Close(context.Background()))

	r.Record(Event{})
	require.Equal(t, 1, logs.undelivered())
}

func TestRecorder_CloseTimeoutReleasesBlockedRecord(t *testing.T) {
	logs := captureLogs(t)
	pub := &fakePublisher{failAll: true}
	opts := testOptions()
	opts.BufferSize = 1
	opts.BatchSize = 1
	r := NewRecorder(t.Context(), pub, opts)

	r.Record(Event{}) // ワーカーが取り出して再試行し続ける
	require.Eventually(t, func() bool { return len(r.ch) == 0 }, time.Second, time.Millisecond)
	r.Record(Event{}) // バッファを埋める
	returned := make(chan struct{})
	go func() {
		r.Record(Event{}) // 空きが出ないのでブロックする
		close(returned)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, r.Close(ctx), context.DeadlineExceeded)
	<-returned
	require.Equal(t, 3, logs.undelivered())
}
