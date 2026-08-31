package identity

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
)

// headerHashHKDFInfo domain-separates the HMAC key derived from
// gateway.encryptKey for a header-hash profile from other uses of the same
// key material (e.g. AES token encryption), and folds in the profile name so
// two profiles sharing encryptKey never derive the same HMAC key.
const headerHashHKDFInfo = "manifold/identity/header-hash/v1/"

// headerResolver derives an identityKey from a fixed request header
// (source: header). When hmacKey is set, the header value is HMAC'd instead
// of used raw.
type headerResolver struct {
	profile string
	header  string
	hmacKey []byte
}

func newHeaderResolver(
	profileName string,
	p *config.IdentityProfile,
	encryptKey []byte,
) (*headerResolver, error) {
	r := &headerResolver{profile: profileName, header: p.Header}
	if p.Hash {
		// Defense in depth: gateway config validates encryptKey is 32 bytes,
		// but a resolver must not silently derive a weak/empty key if that
		// validation is ever bypassed (e.g. a future caller of NewResolver).
		if len(encryptKey) != 32 {
			return nil, fmt.Errorf(
				"identity: profile %q: hash requires a 32-byte encryptKey, got %d bytes",
				profileName, len(encryptKey),
			)
		}
		key, err := hkdf.Key(
			sha256.New, encryptKey, nil, headerHashHKDFInfo+profileName, sha256.Size,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"identity: profile %q: derive header hash key: %w", profileName, err,
			)
		}
		r.hmacKey = key
	}
	return r, nil
}

func (r *headerResolver) Resolve(
	ctx context.Context,
	req *http.Request,
) (_ domainedge.IdentityKey, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/headerResolver/Resolve")
	defer func() { trace.EndSpan(ctx, rErr) }()

	v := req.Header.Get(r.header)
	if v == "" {
		return "", ErrUnauthenticated
	}
	if r.hmacKey != nil {
		mac := hmac.New(sha256.New, r.hmacKey)
		_, _ = mac.Write([]byte(v)) // hash.Hash.Write never returns an error
		v = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	return encodeIdentityKey(r.profile, v), nil
}
