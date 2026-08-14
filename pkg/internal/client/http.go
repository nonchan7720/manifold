package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/nonchan7720/manifold/pkg/internal/env"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var (
	customTransport = http.DefaultTransport.(*http.Transport).Clone() //nolint: errcheck,forcetypeassert
)

func init() {
	setTransportSetting(customTransport)
}

func setTransportSetting(t *http.Transport) {
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 100
	t.IdleConnTimeout = 45 * time.Second
}

func CustomTransport() *http.Transport {
	return customTransport.Clone()
}

func OTELTransport() *otelhttp.Transport {
	return otelhttp.NewTransport(CustomTransport())
}

func Transport() http.RoundTripper {
	return OTELTransport()
}

func httpClient() *http.Client {
	c := &http.Client{
		Transport: Transport(),
		Timeout:   10 * time.Second,
	}
	return c
}

func HTTPClient() *http.Client {
	if env.IsLocalOrCIOrTest() {
		return httpClient()
	}
	return SafeHTTPClient()
}

func SafeHTTPClient() *http.Client {
	if env.SkipSecureClient() {
		return httpClient()
	}
	return StrictSafeHTTPClient()
}

// StrictSafeHTTPClient は SKIP_SECURE_CLIENT の設定にかかわらず、常にプライベート IP への
// 接続を拒否するクライアントを返す。SKIP_SECURE_CLIENT は k8s クラスター内の
// バックエンド通信向けの逃げ道であり、クライアント提示の URL を取得する経路
// （CIMD ドキュメント取得など）には適用すべきでないため、そうした経路ではこちらを使う。
func StrictSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("invalid address %q: %w", address, err)
			}
			if ip := net.ParseIP(host); ip != nil {
				// IP リテラル（通常はここに来る）
				if isPrivateIP(ip) {
					return fmt.Errorf("connection to private IP %s is not allowed", ip)
				}
			} else {
				// ホスト名が渡された場合（エッジケース）のフォールバック
				addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return fmt.Errorf("DNS resolution failed for %q: %w", host, err)
				}
				for _, ipAddr := range addrs {
					if isPrivateIP(ipAddr.IP) {
						return fmt.Errorf("connection to private IP %s is not allowed", ipAddr.IP)
					}
				}
			}
			return nil
		},
	}
	customTransport := CustomTransport()
	customTransport.DialContext = dialer.DialContext
	transport := otelhttp.NewTransport(customTransport)
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}

// isPrivateIP は IP アドレスがプライベート・リンクローカル・ループバック等の
// 予約済みレンジに属するか確認する。
func isPrivateIP(ip net.IP) bool {
	var sharedAddressSpace = net.IPNet{
		IP:   net.IPv4(100, 64, 0, 0),
		Mask: net.CIDRMask(10, 32),
	}
	// "0.0.0.0/8"（このネットワーク上のホストを指す予約済みレンジ）。
	// ip.IsUnspecified() は "0.0.0.0" 単体としか一致しないため、
	// "0.1.2.3" のようなレンジ内の他アドレスを見逃してしまう。
	var thisNetwork = net.IPNet{
		IP:   net.IPv4(0, 0, 0, 0),
		Mask: net.CIDRMask(8, 32),
	}

	// 標準メソッドでカバーできる範囲をチェック
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	if sharedAddressSpace.Contains(ip) {
		return true
	}
	if thisNetwork.Contains(ip) {
		return true
	}
	return false
}
