package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- SafeHTTPClient / StrictHTTPClient ---

// newLoopbackTestServer はループバックで待ち受ける httptest サーバーを返す。
func newLoopbackTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doGet(t *testing.T, c *http.Client, rawURL string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	return c.Do(req)
}

func TestStrictHTTPClient_RejectsLoopbackRegardlessOfEnv(t *testing.T) {
	// SKIP_SECURE_CLIENT（公式 Docker イメージの既定）や TEST を立てても
	// StrictHTTPClient の private IP フィルタは無効化されない。
	t.Setenv("SKIP_SECURE_CLIENT", "true")
	t.Setenv("TEST", "true")

	srv := newLoopbackTestServer(t)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(tlsSrv.Close)

	for _, u := range []string{srv.URL, tlsSrv.URL} {
		resp, err := doGet(t, StrictHTTPClient(), u)
		if resp != nil {
			resp.Body.Close() //nolint: errcheck
		}
		require.Error(t, err, "url=%s", u)
		require.Contains(t, err.Error(), "connection to private IP", "url=%s", u)
	}
}

func TestSafeHTTPClient_SkipSecureClientAllowsLoopback(t *testing.T) {
	// SafeHTTPClient は運用者が設定した上流 URL 向けのため、SKIP_SECURE_CLIENT で
	// private IP フィルタを外せる（StrictHTTPClient との違いを明示するテスト）。
	t.Setenv("SKIP_SECURE_CLIENT", "true")
	t.Setenv("TEST", "false")

	srv := newLoopbackTestServer(t)

	resp, err := doGet(t, SafeHTTPClient(), srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSafeHTTPClient_RejectsLoopbackByDefault(t *testing.T) {
	t.Setenv("SKIP_SECURE_CLIENT", "false")
	t.Setenv("TEST", "true")

	srv := newLoopbackTestServer(t)

	resp, err := doGet(t, SafeHTTPClient(), srv.URL)
	if resp != nil {
		resp.Body.Close() //nolint: errcheck
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection to private IP")
}

func TestIsPrivateIP(t *testing.T) {
	privates := []string{
		"127.0.0.1",
		"10.1.2.3",
		"192.168.0.1",
		"172.16.0.1",
		"169.254.1.1",
		"::1",
		"0.0.0.0",
		"0.1.2.3",
		"0.255.255.255",
	}
	for _, s := range privates {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		require.True(t, isPrivateIP(ip), "expected private: %s", s)
	}

	publics := []string{"8.8.8.8", "1.1.1.1", "203.0.113.1"}
	for _, s := range publics {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		require.False(t, isPrivateIP(ip), "expected public: %s", s)
	}
}

func TestDialPolicy_Check(t *testing.T) {
	_, denied, err := net.ParseCIDR("10.42.0.0/16")
	require.NoError(t, err)
	open := DialPolicy{}
	private := DialPolicy{BlockPrivate: true}
	withDenied := DialPolicy{DeniedCIDRs: []*net.IPNet{denied}}

	// 常時遮断: ポリシーに関係なく拒否される宛先。
	for _, raw := range []string{
		"127.0.0.1", "127.8.8.8", "::1",
		"169.254.169.254", "169.254.1.1", "fe80::1",
		"0.0.0.0", "0.1.2.3", "::",
		"224.0.0.1", "239.255.255.250", "ff02::1", "255.255.255.255",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe",
	} {
		ip := net.ParseIP(raw)
		require.NotNil(t, ip, raw)
		require.Error(t, open.check(ip), "always blocked even with an open policy: %s", raw)
		require.Error(t, private.check(ip), raw)
	}

	// プライベートレンジ: BlockPrivate のときだけ拒否される（SKIP_SECURE_CLIENT で緩和可能）。
	for _, raw := range []string{
		"10.0.0.1", "172.16.5.5", "192.168.1.1", "100.64.0.1", "fd00::1",
		"::ffff:10.0.0.1", "64:ff9b::a00:1",
	} {
		ip := net.ParseIP(raw)
		require.NotNil(t, ip, raw)
		require.NoError(t, open.check(ip), "private range allowed by an open policy: %s", raw)
		require.Error(t, private.check(ip), "private range blocked by BlockPrivate: %s", raw)
	}

	// 公開アドレスは許可される。
	for _, raw := range []string{"8.8.8.8", "93.184.216.34", "2001:db8::1", "::ffff:8.8.8.8"} {
		ip := net.ParseIP(raw)
		require.NoError(t, open.check(ip), raw)
		require.NoError(t, private.check(ip), raw)
	}

	// DeniedCIDRs は常に拒否される。
	require.Error(t, withDenied.check(net.ParseIP("10.42.7.7")))
	require.Contains(t, withDenied.check(net.ParseIP("10.42.7.7")).Error(), "deniedCIDRs")
	require.NoError(t, withDenied.check(net.ParseIP("10.43.0.1")))
}

func TestNewCallerURLClient_AlwaysBlocksLoopbackEvenWithSkipSecureClient(t *testing.T) {
	// 公式 Docker イメージの既定（SKIP_SECURE_CLIENT=true）でも、ツール呼び出し側が指定した
	// URL のループバック（および link-local 等）への接続は拒否される。
	t.Setenv("SKIP_SECURE_CLIENT", "true")
	t.Setenv("TEST", "true")

	srv := newLoopbackTestServer(t)
	resp, err := doGet(t, NewCallerURLClient(nil), srv.URL)
	if resp != nil {
		resp.Body.Close() //nolint: errcheck
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection to private IP")
}

func TestNewCallerURLClient_PrivateRangePolicyFollowsSkipSecureClient(t *testing.T) {
	// SKIP_SECURE_CLIENT はプライベートレンジ（クラスタ内 IP）の遮断だけを緩和する。
	t.Setenv("SKIP_SECURE_CLIENT", "true")
	require.NoError(t, callerURLPolicy(nil).check(net.ParseIP("10.0.0.1")))
	require.Error(t, callerURLPolicy(nil).check(net.ParseIP("169.254.169.254")))

	t.Setenv("SKIP_SECURE_CLIENT", "false")
	require.Error(t, callerURLPolicy(nil).check(net.ParseIP("10.0.0.1")))
}
