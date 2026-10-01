package toolmetrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// 送信失敗時の再試行間隔。initialRetryInterval から始めて倍々にし、
// maxRetryInterval で頭打ちにする。
const (
	initialRetryInterval = 500 * time.Millisecond
	maxRetryInterval     = 30 * time.Second
)

// RecorderOptions は Recorder の動作設定。ここではゼロ値を補完しない
// （config.ToolMetricsConfig.WithDefaults が補完する）ため、全フィールドに正の値が必要。
type RecorderOptions struct {
	BufferSize     int
	BatchSize      int
	FlushInterval  time.Duration
	PublishTimeout time.Duration
}

// Recorder はイベントをメモリ上にバッファし、バックグラウンドの単一の
// goroutine からバッチで送信する。イベントは記録なので 1 件も破棄しない。
//
//   - バッファが満杯の間、Record はワーカーが空きを作るまで待つ
//     （取りこぼす代わりにツール呼び出し側を待たせる）。
//   - 送信に失敗したバッチは成功するまで指数バックオフで再試行する。そのため
//     配送は at-least-once になり、受信側は Event.ID で重複を排除する。
//   - Close が送り切る前に打ち切られた場合、未送信のイベントはすべて全文を
//     error ログに出力する（logUndelivered）。ログから復元できる。
type Recorder struct {
	pub  Publisher
	opts RecorderOptions

	ch chan *Event
	// mu は closed と Record の inflight.Add を排他し、closed の確認を通過した
	// Record をすべて Close が待てるようにする。
	mu       sync.RWMutex
	closed   bool
	inflight sync.WaitGroup
	// stop: 以降 Record は ch に送らない。ワーカーは ch を空にして終了する。
	stop chan struct{}
	// abort: Close の期限切れ。再試行をやめ、残りをログに出す。
	abort chan struct{}
	done  chan struct{}

	closeOnce sync.Once
	abortOnce sync.Once
}

// NewRecorder はワーカー goroutine を起動する。ctx をキャンセルしても止まらない。
// 残りを送り切って止めるには Close を呼ぶ。
func NewRecorder(ctx context.Context, pub Publisher, opts RecorderOptions) *Recorder {
	r := &Recorder{
		pub:   pub,
		opts:  opts,
		ch:    make(chan *Event, opts.BufferSize),
		stop:  make(chan struct{}),
		abort: make(chan struct{}),
		done:  make(chan struct{}),
	}
	// ワーカーは Close で送り切るまで ctx より長く動くため、ctx の値
	// （トレース・ロガー）だけを引き継ぎ、キャンセルは引き継がない。
	go r.run(context.WithoutCancel(ctx))
	return r
}

// Record は e をワーカーへ渡す。バッファが満杯の間は待つ。並行に呼んでよい。
// Close の開始後、または Close が待つのを打ち切った後は、キューに積む代わりに
// e を error ログに出力する。
func (r *Recorder) Record(e *Event) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		logUndelivered(context.Background(), e)
		return
	}
	r.inflight.Add(1)
	r.mu.RUnlock()
	defer r.inflight.Done()

	select {
	case r.ch <- e:
	case <-r.abort:
		logUndelivered(context.Background(), e)
	}
}

// Close はイベントの受け付けを止め、積まれたイベントがすべて送信されて
// Publisher が閉じられるまで待つ。先に ctx が期限切れになった場合は再試行を
// やめ、未送信のイベントをすべて error ログに出力してから ctx.Err() を返す。
// 何度呼んでもよい。
func (r *Recorder) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		go func() {
			// closed の確認を通過した Record が満杯のバッファで待っている
			// 可能性がある。ワーカーは取り出し続けるので、それらも積まれる。
			r.inflight.Wait()
			close(r.stop)
		}()
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		r.abortOnce.Do(func() { close(r.abort) })
		<-r.done
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

	// Close が打ち切った（abort）ら実行中の Publish も中断させ、shutdownTimeout を
	// 超えて待たないようにする。run の終了時に cancel するので監視 goroutine は残らない。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-r.abort:
			cancel()
		case <-ctx.Done():
		}
	}()

	ticker := time.NewTicker(r.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]*Event, 0, r.opts.BatchSize)
	// flush は batch を送信する。Close が再試行を打ち切った場合は false を
	// 返す（batch はログに出力済み。ワーカーは終了処理に入る）。
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		ok := r.publish(ctx, batch)
		if !ok {
			logUndelivered(ctx, batch...)
		}
		batch = batch[:0]
		return ok
	}
	add := func(e *Event) bool {
		batch = append(batch, e)
		if len(batch) >= r.opts.BatchSize {
			return flush()
		}
		return true
	}

	for {
		select {
		case e := <-r.ch:
			if !add(e) {
				r.drainToLog(ctx)
				return
			}
		case <-ticker.C:
			if !flush() {
				r.drainToLog(ctx)
				return
			}
		case <-r.stop:
			for {
				select {
				case e := <-r.ch:
					if !add(e) {
						r.drainToLog(ctx)
						return
					}
				default:
					flush()
					return
				}
			}
		case <-r.abort:
			logUndelivered(ctx, batch...)
			r.drainToLog(ctx)
			return
		}
	}
}

// publish は pub.Publish を成功する（true）か Close に打ち切られる（false）まで
// 指数バックオフで再試行する。
func (r *Recorder) publish(ctx context.Context, batch []*Event) bool {
	wait := initialRetryInterval
	for attempt := 1; ; attempt++ {
		pubCtx, cancel := context.WithTimeout(ctx, r.opts.PublishTimeout)
		err := r.pub.Publish(pubCtx, batch)
		cancel()
		if err == nil {
			return true
		}
		slog.WarnContext(ctx, "tool metrics: failed to publish events; retrying",
			slog.Int("count", len(batch)), slog.Int("attempt", attempt),
			slog.Duration("retry_in", wait), slog.Any("error", err))

		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-r.abort:
			timer.Stop()
			return false
		}
		wait = min(wait*2, maxRetryInterval)
	}
}

// drainToLog はバッファに残っているイベントをすべてログに出力する。abort 後
// にのみ呼ばれる。
func (r *Recorder) drainToLog(ctx context.Context) {
	// abort の時点で満杯のバッファを待っていた Record は、ch に空きができると
	// select で ch への送信を選ぶことがある。ワーカーが先に終わるとそのイベントが
	// 失われるため、Record がすべて終わるのを待ってから排出する。abort は
	// closed = true の後にしか起きないので、待っている間に inflight は増えない。
	r.inflight.Wait()
	for {
		select {
		case e := <-r.ch:
			logUndelivered(ctx, e)
		default:
			return
		}
	}
}

// logUndelivered はキューに送れなかったイベントの最後の受け皿。イベントの
// 全文を error ログに出力し、そこから復元できるようにする。
func logUndelivered(ctx context.Context, events ...*Event) {
	for _, e := range events {
		body, err := Marshal(e)
		if err != nil {
			// 生成された型の Marshal は通常失敗しない。念のため ID だけでも残す。
			slog.ErrorContext(ctx, "tool metrics: event could not be delivered to the queue",
				slog.String("event_id", e.GetId()), slog.Any("error", err))
			continue
		}
		// protojson の本文をそのまま埋め込み、キューの本文と同じ形で復元できるようにする。
		slog.ErrorContext(ctx, "tool metrics: event could not be delivered to the queue",
			slog.Any("event", json.RawMessage(body)))
	}
}
