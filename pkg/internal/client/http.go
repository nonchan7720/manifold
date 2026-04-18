package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

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

func HTTPClient() *http.Client {
	c := &http.Client{
		Transport: Transport(),
		Timeout:   10 * time.Second,
	}
	return c
}

func MergeTransport(rt http.RoundTripper) http.RoundTripper {
	switch t := rt.(type) { //nolint: gocritic
	case *http.Transport:
		setTransportSetting(t)
	}
	return rt
}

func SafeHTTPClient() *http.Client {
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
	for _, cidr := range privateIPRanges {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// privateIPRanges はプライベート・予約済み IP レンジのリスト。
var privateIPRanges []*net.IPNet

func init() {
	privateCIDRs := []string{
		"127.0.0.0/8",    // IPv4 ループバック
		"::1/128",        // IPv6 ループバック
		"10.0.0.0/8",     // プライベート
		"172.16.0.0/12",  // プライベート
		"192.168.0.0/16", // プライベート
		"169.254.0.0/16", // IPv4 リンクローカル
		"fe80::/10",      // IPv6 リンクローカル
		"fc00::/7",       // IPv6 ユニークローカル
		"100.64.0.0/10",  // 共有アドレス空間 (RFC 6598)
		"0.0.0.0/8",      // 未指定
	}
	for _, cidr := range privateCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPRanges = append(privateIPRanges, network)
		}
	}
}
