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

// SafeHTTPClient はプライベート/ループバック/リンクローカル IP への接続を dialer で拒否する
// HTTP クライアントを返す。環境変数 SKIP_SECURE_CLIENT が真の場合はフィルタ無しのクライアントを
// 返す（公式 Docker イメージは既定で SKIP_SECURE_CLIENT=true）。
//
// 運用者が設定ファイルで指定した上流 URL（OAuth の token/discovery など）向け。プライベート
// ネットワーク上の上流 AS に接続する運用を許容するため、エスケープハッチを残している。
// 呼び出し側が指定した URL には NewCallerURLClient（ツール引数のファイル URL）または
// StrictHTTPClient（CIMD の client_id URL）を使うこと。
func SafeHTTPClient() *http.Client {
	if env.SkipSecureClient() {
		return httpClient()
	}
	return NewFilteredHTTPClient(DialPolicy{BlockPrivate: true})
}

// StrictHTTPClient はプライベート/ループバック/リンクローカル IP への接続を dialer で拒否する
// HTTP クライアントを返す。SafeHTTPClient と異なり、SKIP_SECURE_CLIENT を含むいかなる環境変数
// でもフィルタを無効化できない。
//
// 完全に匿名のクライアントが指定する URL（CIMD の client_id URL）の取得に使う。
func StrictHTTPClient() *http.Client {
	return NewFilteredHTTPClient(DialPolicy{BlockPrivate: true})
}

// NewCallerURLClient はツール呼び出し側が指定した URL（ファイル入力の署名付き URL など）を
// 取得するためのクライアントを返す。
//
//   - ループバック・リンクローカル（クラウドのメタデータエンドポイント 169.254.169.254 を含む）・
//     未指定アドレス・マルチキャストなど、ファイルの取得先として正当になり得ない宛先は
//     環境変数に関係なく常に拒否する。
//   - RFC1918 / 100.64.0.0/10 / fc00::/7 などのプライベートレンジは既定で拒否するが、
//     SKIP_SECURE_CLIENT が真なら許可する。Kubernetes 内では署名付き URL がクラスタ内の
//     ClusterIP（MinIO 等）を指すことが普通で、レンジを列挙して許可するのは現実的でないため。
//   - deniedCIDRs は運用者が追加で拒否したい宛先（常に拒否）。
func NewCallerURLClient(deniedCIDRs []*net.IPNet) *http.Client {
	return NewFilteredHTTPClient(callerURLPolicy(deniedCIDRs))
}

// callerURLPolicy は NewCallerURLClient が適用する DialPolicy を返す。
func callerURLPolicy(deniedCIDRs []*net.IPNet) DialPolicy {
	return DialPolicy{
		BlockPrivate: !env.SkipSecureClient(),
		DeniedCIDRs:  deniedCIDRs,
	}
}

// DialPolicy は NewFilteredHTTPClient の dialer が接続先 IP に適用する拒否ポリシー。
// ループバック・リンクローカル・未指定・マルチキャスト等（isAlwaysBlockedIP）はポリシーに
// 関係なく常に拒否する。
type DialPolicy struct {
	// BlockPrivate が真なら RFC1918 / 100.64.0.0/10 / fc00::/7 などのプライベートレンジも拒否する。
	BlockPrivate bool
	// DeniedCIDRs に含まれる宛先は常に拒否する。
	DeniedCIDRs []*net.IPNet
}

// check は ip がポリシーで拒否されるなら理由を含むエラーを返す。
func (p DialPolicy) check(ip net.IP) error {
	if isAlwaysBlockedIP(ip) {
		return fmt.Errorf("connection to private IP %s is not allowed", ip)
	}
	if p.BlockPrivate && isPrivateRangeIP(ip) {
		return fmt.Errorf("connection to private IP %s is not allowed", ip)
	}
	for _, n := range p.DeniedCIDRs {
		if n != nil && n.Contains(ip) {
			return fmt.Errorf("connection to %s is denied by fileFetch.deniedCIDRs (%s)", ip, n)
		}
	}
	return nil
}

// NewFilteredHTTPClient は接続先 IP を policy で検査する dialer を組み込んだクライアントを生成する。
// 検査は DNS 解決後の実際の接続先 IP に対して行うため、プライベート IP に解決されるホスト名でも
// 弾ける（DNS rebinding で解決結果が変わっても接続のたびに検査する）。
func NewFilteredHTTPClient(policy DialPolicy) *http.Client {
	dialer := &net.Dialer{
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("invalid address %q: %w", address, err)
			}
			if ip := net.ParseIP(host); ip != nil {
				// IP リテラル（通常はここに来る）
				return policy.check(ip)
			}
			// ホスト名が渡された場合（エッジケース）のフォールバック
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return fmt.Errorf("DNS resolution failed for %q: %w", host, err)
			}
			for _, ipAddr := range addrs {
				if err := policy.check(ipAddr.IP); err != nil {
					return err
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

// nat64Prefix は RFC 6052 の well-known prefix 64:ff9b::/96。IPv4 アドレスを埋め込んだ
// IPv6 アドレスで IPv4 側の判定をすり抜けないよう、埋め込まれた IPv4 も検査する。
var nat64Prefix = net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}

// embeddedIPv4 は IPv4-mapped（::ffff:a.b.c.d）または NAT64（64:ff9b::a.b.c.d）形式の
// IPv6 アドレスから IPv4 アドレスを取り出す。該当しなければ nil。
func embeddedIPv4(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	if len(ip) == net.IPv6len && nat64Prefix.Contains(ip) {
		return net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	}
	return nil
}

// isAlwaysBlockedIP は、呼び出し側が指定した URL の取得先として正当になり得ない宛先かを返す:
// ループバック、リンクローカル（169.254.0.0/16 のクラウドメタデータを含む）、未指定アドレス、
// 0.0.0.0/8、マルチキャスト、ブロードキャスト。環境変数や設定では緩和しない。
func isAlwaysBlockedIP(ip net.IP) bool {
	if v4 := embeddedIPv4(ip); v4 != nil && !v4.Equal(ip) {
		if isAlwaysBlockedIP(v4) {
			return true
		}
	}
	// "0.0.0.0/8"（このネットワーク上のホストを指す予約済みレンジ）。
	// ip.IsUnspecified() は "0.0.0.0" 単体としか一致しないため、
	// "0.1.2.3" のようなレンジ内の他アドレスを見逃してしまう。
	thisNetwork := net.IPNet{IP: net.IPv4(0, 0, 0, 0), Mask: net.CIDRMask(8, 32)}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.Equal(net.IPv4bcast) {
		return true
	}
	return thisNetwork.Contains(ip)
}

// isPrivateRangeIP は RFC1918 / fc00::/7（net.IP.IsPrivate）と 100.64.0.0/10（共有アドレス空間）
// に属するかを返す。クラスタ内通信で使われるレンジで、SKIP_SECURE_CLIENT で緩和できる。
func isPrivateRangeIP(ip net.IP) bool {
	if v4 := embeddedIPv4(ip); v4 != nil && !v4.Equal(ip) {
		if isPrivateRangeIP(v4) {
			return true
		}
	}
	sharedAddressSpace := net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return ip.IsPrivate() || sharedAddressSpace.Contains(ip)
}

// isPrivateIP は IP アドレスがプライベート・リンクローカル・ループバック等の
// 予約済みレンジに属するか確認する（isAlwaysBlockedIP または isPrivateRangeIP）。
func isPrivateIP(ip net.IP) bool {
	return isAlwaysBlockedIP(ip) || isPrivateRangeIP(ip)
}
