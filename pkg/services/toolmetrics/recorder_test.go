package toolmetrics

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakePublisher records every batch; block, when set, stalls Publish until
// it is closed.
type fakePublisher struct {
	mu      sync.Mutex
	batches [][]Event
	closed  bool
	err     error
	block   chan struct{}
}

func (p *fakePublisher) Publish(_ context.Context, events []Event) error {
	if p.block != nil {
		<-p.block
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.batches = append(p.batches, append([]Event(nil), events...))
	return p.err
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

	// Close 後の Record は破棄され、panic しない。
	r.Record(Event{})
	require.Equal(t, 5, pub.total())
	require.NoError(t, r.Close(context.Background()), "Close is idempotent")
}

func TestRecorder_DropsWhenBufferIsFull(t *testing.T) {
	pub := &fakePublisher{block: make(chan struct{})}
	opts := testOptions()
	opts.BufferSize = 2
	opts.BatchSize = 1
	r := NewRecorder(t.Context(), pub, opts)

	// 1 件目でワーカーが Publish に入ってブロックするのを待ってから、
	// バッファ（2 件）を超えて投入する。
	r.Record(Event{})
	require.Eventually(t, func() bool { return len(r.ch) == 0 }, time.Second, time.Millisecond)
	for range 5 {
		r.Record(Event{})
	}
	require.Equal(t, uint64(3), r.dropped.Load())

	close(pub.block)
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, 3, pub.total())
}

func TestRecorder_PublishErrorDoesNotStopWorker(t *testing.T) {
	pub := &fakePublisher{err: errors.New("queue unavailable")}
	opts := testOptions()
	opts.BatchSize = 1
	r := NewRecorder(t.Context(), pub, opts)

	r.Record(Event{})
	r.Record(Event{})
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, 2, pub.total())
}

func TestRecorder_CloseHonorsContext(t *testing.T) {
	pub := &fakePublisher{block: make(chan struct{})}
	opts := testOptions()
	opts.BatchSize = 1
	r := NewRecorder(t.Context(), pub, opts)
	r.Record(Event{})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, r.Close(ctx), context.DeadlineExceeded)

	close(pub.block)
	require.NoError(t, r.Close(context.Background()))
}
