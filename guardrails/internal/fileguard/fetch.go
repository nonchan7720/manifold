package fileguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
)

// maxFetchRedirects は URL の取得でたどるリダイレクトの最大数。
const maxFetchRedirects = 5

// errFetchTooLarge は、取得したファイルが上限を超えたことを表す。
var errFetchTooLarge = errors.New("fetch: the file exceeds the size limit")

// リダイレクトを止めた理由。
var (
	errFetchTooManyRedirects = errors.New("too many redirects")
	errFetchRedirectDenied   = errors.New("redirected to a host that is not allowed")
)

// allowedHost は URL の取得を許可するホスト1件。
type allowedHost struct {
	// name は小文字のホスト名。wildcard なら、そのサブドメインすべて（"example.com"
	// は "a.example.com" に一致し、"example.com" 自身には一致しない）。
	name     string
	wildcard bool
	// port が空なら、スキームの既定のポート（https は 443、http は 80）だけを許可する。
	port string
}

// Fetcher は、URL で渡されたファイルを、許可したホストからだけ取得する。
//
// 呼び出し側が指定した URL を取得すると SSRF の入口になるので、運用者が許可した
// ホスト名（と、その既定のポート、または指定したポート）に限る。リダイレクト先も
// 同じ許可リストで確かめる。許可したホストがどのアドレスに解決されるかは確かめない
// （クラスタ内のストレージなど、内部のホストを許可できるようにするため）。許可する
// ホストは、その名前解決も含めて信頼できるものに限ること。
type Fetcher struct {
	hosts      []allowedHost
	maxBytes   int64
	httpClient *http.Client
}

// NewFetcher は hosts（"files.example.com"、"*.example.com"、"minio.internal:9000"）
// からの取得だけを許可する Fetcher を返す。maxBytes は取得するファイルの上限、
// timeout は1回の取得（リダイレクトを含む）の上限。
func NewFetcher(hosts []string, maxBytes int64, timeout time.Duration) (*Fetcher, error) {
	f := &Fetcher{maxBytes: maxBytes}
	for _, raw := range hosts {
		host, err := parseAllowedHost(raw)
		if err != nil {
			return nil, err
		}
		f.hosts = append(f.hosts, host)
	}
	f.httpClient = &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxFetchRedirects {
				return errFetchTooManyRedirects
			}
			if !f.Allowed(req.URL) {
				return errFetchRedirectDenied
			}
			return nil
		},
	}
	return f, nil
}

func parseAllowedHost(raw string) (allowedHost, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	host := allowedHost{name: s}
	// "[::1]:8080" や "host:8080" のポートを分ける。ポートの無い IPv6 は括弧を外す。
	if name, port, err := net.SplitHostPort(s); err == nil {
		host.name, host.port = name, port
	} else {
		host.name = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	}
	if rest, ok := strings.CutPrefix(host.name, "*."); ok {
		host.name, host.wildcard = rest, true
	}
	// ホスト名に ":" を許すのは IPv6 のアドレスだけ（"https://host" のような URL を
	// ホスト名 "https" として受け付けない）。ポートは数字だけ。
	validName := host.name != "" && !strings.ContainsAny(host.name, "*/@ ") &&
		(!strings.Contains(host.name, ":") || net.ParseIP(host.name) != nil)
	validPort := strings.Trim(host.port, "0123456789") == ""
	if !validName || !validPort {
		return allowedHost{}, fmt.Errorf(
			"allowed host %q must be a host name, optionally with a leading *. and a :port", raw,
		)
	}
	return host, nil
}

// Allowed は u が許可したホストの http(s) の URL かを返す。
func (f *Fetcher) Allowed(u *url.URL) bool {
	if f == nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	name := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	for _, host := range f.hosts {
		wantPort := host.port
		if wantPort == "" {
			wantPort = "443"
			if u.Scheme == "http" {
				wantPort = "80"
			}
		}
		if port != wantPort {
			continue
		}
		if host.wildcard && strings.HasSuffix(name, "."+host.name) {
			return true
		}
		if !host.wildcard && name == host.name {
			return true
		}
	}
	return false
}

// Fetch は rawURL のファイルを取得する。許可していないホストなら ok は false で、
// 何も取得しない。上限を超えたら errFetchTooLarge を返す。
//
// URL は署名付きのトークンを含みうるので、エラー文には出さない。
func (f *Fetcher) Fetch(
	ctx context.Context,
	rawURL string,
) (_ extract.SourceFile, ok bool, _ error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !f.Allowed(u) {
		return extract.SourceFile{}, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return extract.SourceFile{}, true, errors.New("fetch: build request")
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return extract.SourceFile{}, true, fetchFailure(u.Hostname(), err)
	}
	defer resp.Body.Close() //nolint: errcheck
	if resp.StatusCode != http.StatusOK {
		return extract.SourceFile{}, true, fmt.Errorf(
			"fetch: %s returned status %d", u.Hostname(), resp.StatusCode,
		)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return extract.SourceFile{}, true, fetchFailure(u.Hostname(), err)
	}
	if int64(len(data)) > f.maxBytes {
		return extract.SourceFile{}, true, errFetchTooLarge
	}
	return extract.SourceFile{
		MIMEType: extract.NormalizeMIME(resp.Header.Get("Content-Type"), data), Data: data,
	}, true, nil
}

// fetchFailure は取得の失敗 err を、ホスト名と失敗の種類だけのエラーにする。
//
// err の文言はそのまま使わない。*url.Error は URL を含み、リダイレクト先の解析に
// 失敗したときは内側のエラーにも Location の値（署名付きのトークンを含みうる）が
// 入るため。
func fetchFailure(host string, err error) error {
	kind := extract.RequestFailure(err)
	switch {
	case errors.Is(err, errFetchRedirectDenied):
		kind = errFetchRedirectDenied.Error()
	case errors.Is(err, errFetchTooManyRedirects):
		kind = errFetchTooManyRedirects.Error()
	}
	return fmt.Errorf("fetch: %s: %s", host, kind)
}
