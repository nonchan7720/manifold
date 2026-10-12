package mcpsrv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// promptFileReloadDelay は変更通知を受けてから読み直すまでの待ち時間。
// Kubernetes の ConfigMap の更新（..data シンボリックリンクの張り替え）は
// 1回の更新で複数のイベントを出すため、まとめて1回だけ読み直す。
const promptFileReloadDelay = 100 * time.Millisecond

// filePromptSource は prompts 定義ドキュメントをファイルから読み、変更を
// 監視して読み直す。読み直しに失敗した場合は直前の prompts を返し続ける。
type filePromptSource struct {
	server string
	path   string

	current atomic.Pointer[promptSet]
	// raw は直近に採用したドキュメント。内容が変わらないイベントを無視するため。
	raw []byte

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newFilePromptSource は path を読み込み、ファイルの監視を始める。最初の
// 読み込みに失敗した場合はエラーを返す。close で監視を止める。
func newFilePromptSource(ctx context.Context, server, path string) (*filePromptSource, error) {
	s := &filePromptSource{server: server, path: path}
	raw, set, err := s.load()
	if err != nil {
		return nil, fmt.Errorf("server %q: prompts.file: %w", server, err)
	}
	s.raw = raw
	s.current.Store(set)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("server %q: prompts.file: watch: %w", server, err)
	}
	// ファイルそのものではなくディレクトリを監視する。ConfigMap のマウントは
	// ..data シンボリックリンクの張り替えで更新され、ファイル自体への
	// イベントは発生しない。エディタの rename による保存も同様。
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("server %q: prompts.file: watch: %w", server, err)
	}

	ctx, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer watcher.Close() //nolint: errcheck
		s.watch(ctx, watcher)
	}()
	return s, nil
}

func (s *filePromptSource) load() ([]byte, *promptSet, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close() //nolint: errcheck
	raw, err := io.ReadAll(io.LimitReader(f, maxPromptDocumentSize+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > maxPromptDocumentSize {
		return nil, nil, fmt.Errorf("document exceeds %d bytes", maxPromptDocumentSize)
	}
	set, err := parsePromptDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, set, nil
}

func (s *filePromptSource) watch(ctx context.Context, watcher *fsnotify.Watcher) {
	timer := time.NewTimer(promptFileReloadDelay)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-watcher.Events:
			if !ok {
				return
			}
			// どのエントリへのイベントでも読み直す（..data・タイムスタンプ付きの
			// ディレクトリ・一時ファイルなど、更新に関わる名前は環境で異なる）。
			// 内容が変わっていなければ reload が何もしない。
			timer.Reset(promptFileReloadDelay)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.WarnContext(ctx, "prompts file watch error",
				slog.String("server", s.server), slog.Any("error", err))
		case <-timer.C:
			s.reload(ctx)
		}
	}
}

func (s *filePromptSource) reload(ctx context.Context) {
	raw, set, err := s.load()
	if err != nil {
		slog.WarnContext(ctx, "prompts file reload failed; keeping the previous prompts",
			slog.String("server", s.server), slog.String("file", s.path), slog.Any("error", err))
		return
	}
	if bytes.Equal(raw, s.raw) {
		return
	}
	s.raw = raw
	s.current.Store(set)
	slog.InfoContext(ctx, "prompts file reloaded",
		slog.String("server", s.server), slog.String("file", s.path),
		slog.Int("prompts", len(set.list)))
}

func (s *filePromptSource) prompts(context.Context) (*promptSet, error) {
	return s.current.Load(), nil
}

// close はファイルの監視を止め、監視の goroutine の終了を待つ。
func (s *filePromptSource) close() {
	s.cancel()
	s.wg.Wait()
}
