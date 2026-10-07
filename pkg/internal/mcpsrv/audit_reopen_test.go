package mcpsrv

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestReopenableFile_Reopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	rotated := filepath.Join(dir, "audit.jsonl.1")

	f, err := openReopenableFile(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	_, err = f.Write([]byte("before\n"))
	require.NoError(t, err)
	require.NoError(t, os.Rename(path, rotated))
	require.NoError(t, f.Reopen())
	_, err = f.Write([]byte("after\n"))
	require.NoError(t, err)

	old, err := os.ReadFile(rotated)
	require.NoError(t, err)
	require.Equal(t, "before\n", string(old))
	cur, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "after\n", string(cur))
}

func TestAuditLogger_ReopenOnSIGHUP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := NewAuditLogger(config.AuditConfig{Enabled: true, Output: path}, config.AuthzHeaders{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	l.ReopenOnSIGHUP(t.Context())

	require.NoError(t, os.Rename(path, path+".1"))
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGHUP))
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestAuditLogger_ReopenOnSIGHUP_NoopWithoutFile(t *testing.T) {
	var nilLogger *AuditLogger
	nilLogger.ReopenOnSIGHUP(t.Context())
	l := newAuditLoggerTo(&reopenableFile{}, config.AuthzHeaders{}, false)
	l.ReopenOnSIGHUP(t.Context())
}
