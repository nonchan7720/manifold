package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
)

// AWSIAMAuth は AWS の認証情報（既定の認証情報チェーン、任意で AssumeRole）で
// リクエストに SigV4 署名を付ける。
type AWSIAMAuth struct {
	cfg *config.AWSIAM

	// creds が非 nil なら認証情報チェーンの代わりに使う（テスト用）。
	creds aws.CredentialsProvider
	now   func() time.Time

	mu       sync.Mutex
	provider aws.CredentialsProvider
}

func NewAWSIAMAuth(cfg *config.AWSIAM) *AWSIAMAuth {
	return &AWSIAMAuth{cfg: cfg, now: time.Now}
}

var _ CloudAuth = (*AWSIAMAuth)(nil)

func (a *AWSIAMAuth) credentials(ctx context.Context) (aws.CredentialsProvider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider, nil
	}
	if a.creds != nil {
		a.provider = a.creds
		return a.provider, nil
	}
	// 認証情報の更新に使う ctx はリクエストの ctx から切り離す。
	loadCtx := context.WithoutCancel(ctx)
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(a.cfg.Region)}
	if a.cfg.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(a.cfg.Profile))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(loadCtx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	provider := awsCfg.Credentials
	if a.cfg.RoleARN != "" {
		provider = stscreds.NewAssumeRoleProvider(sts.NewFromConfig(awsCfg), a.cfg.RoleARN)
	}
	// 一時認証情報は期限前に自動更新するため CredentialsCache で包む。
	a.provider = aws.NewCredentialsCache(provider)
	return a.provider, nil
}

func (a *AWSIAMAuth) Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &awsRoundTripper{base: base, auth: a, signer: v4.NewSigner()}
}

type awsRoundTripper struct {
	base   http.RoundTripper
	auth   *AWSIAMAuth
	signer *v4.Signer
}

func (rt *awsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	provider, err := rt.auth.credentials(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load aws credentials", logging.WithStackTrace(err)...)
		return errorResponse(http.StatusBadGateway, "aws credentials unavailable"), nil
	}
	creds, err := provider.Retrieve(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to retrieve aws credentials", logging.WithStackTrace(err)...)
		return errorResponse(http.StatusBadGateway, "aws credentials unavailable"), nil
	}

	// 署名はペイロードの SHA-256 を含むので、本文を読み出してから戻す。
	payload := []byte{}
	if req.Body != nil && req.Body != http.NoBody {
		payload, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body for signing: %w", err)
		}
	}
	sum := sha256.Sum256(payload)

	req = req.Clone(ctx)
	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(payload)), nil }
	req.ContentLength = int64(len(payload))
	// 下流のトークン等が残っていると署名対象に紛れ込むので除く。
	req.Header.Del("Authorization")
	if err := rt.signer.SignHTTP(
		ctx,
		creds,
		req,
		hex.EncodeToString(sum[:]),
		rt.auth.cfg.Service,
		rt.auth.cfg.Region,
		rt.auth.now(),
	); err != nil {
		slog.ErrorContext(ctx, "failed to sign request", logging.WithStackTrace(err)...)
		return errorResponse(http.StatusBadGateway, "aws request signing failed"), nil
	}
	return rt.base.RoundTrip(req)
}
