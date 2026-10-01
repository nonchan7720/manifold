package toolmetrics

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// RecorderOptions tunes a Recorder; zero values are not defaulted here
// (config.ToolMetricsConfig.WithDefaults does that), so every field must be
// positive.
type RecorderOptions struct {
	BufferSize     int
	BatchSize      int
	FlushInterval  time.Duration
	PublishTimeout time.Duration
}

// Recorder buffers events in memory and publishes them in batches from a
// single background goroutine. Record never blocks: when the buffer is full
// the event is dropped and counted, so a slow queue degrades metrics rather
// than tool-call latency.
type Recorder struct {
	pub  Publisher
	opts RecorderOptions

	ch      chan Event
	stop    chan struct{}
	done    chan struct{}
	closed  atomic.Bool
	dropped atomic.Uint64
	once    sync.Once
}

// NewRecorder starts the worker goroutine. Cancelling ctx does not stop it;
// call Close to flush and stop it.
func NewRecorder(ctx context.Context, pub Publisher, opts RecorderOptions) *Recorder {
	r := &Recorder{
		pub:  pub,
		opts: opts,
		ch:   make(chan Event, opts.BufferSize),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	// The worker keeps ctx's values (trace, logger) but not its
	// cancellation, since it must outlive ctx until Close flushes it.
	go r.run(context.WithoutCancel(ctx))
	return r
}

// Record enqueues e without blocking. It is safe to call concurrently and
// after Close (the event is then dropped).
func (r *Recorder) Record(e Event) {
	if r.closed.Load() {
		r.dropped.Add(1)
		return
	}
	select {
	case r.ch <- e:
	default:
		r.dropped.Add(1)
	}
}

// Close stops accepting events, publishes whatever is still buffered and
// closes the Publisher. It returns ctx.Err() if ctx expires before the
// worker finishes; the worker keeps draining in the background in that case.
func (r *Recorder) Close(ctx context.Context) error {
	r.once.Do(func() {
		r.closed.Store(true)
		close(r.stop)
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) run(ctx context.Context) {
	defer close(r.done)
	defer func() {
		if err := r.pub.Close(); err != nil {
			slog.WarnContext(ctx, "tool metrics: failed to close publisher",
				slog.Any("error", err))
		}
	}()

	ticker := time.NewTicker(r.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, r.opts.BatchSize)
	flush := func() {
		r.reportDropped(ctx)
		if len(batch) == 0 {
			return
		}
		r.publish(ctx, batch)
		batch = batch[:0]
	}
	add := func(e Event) {
		batch = append(batch, e)
		if len(batch) >= r.opts.BatchSize {
			flush()
		}
	}

	for {
		select {
		case e := <-r.ch:
			add(e)
		case <-ticker.C:
			flush()
		case <-r.stop:
			for {
				select {
				case e := <-r.ch:
					add(e)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *Recorder) publish(ctx context.Context, batch []Event) {
	ctx, cancel := context.WithTimeout(ctx, r.opts.PublishTimeout)
	defer cancel()
	if err := r.pub.Publish(ctx, batch); err != nil {
		slog.WarnContext(ctx, "tool metrics: failed to publish events",
			slog.Int("count", len(batch)), slog.Any("error", err))
	}
}

func (r *Recorder) reportDropped(ctx context.Context) {
	if n := r.dropped.Swap(0); n > 0 {
		slog.WarnContext(ctx, "tool metrics: events dropped because the buffer was full",
			slog.Uint64("dropped", n))
	}
}
