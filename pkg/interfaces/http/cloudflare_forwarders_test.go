package httphandler

import (
	"testing"

	"github.com/netinternet/remoteaddr"
	"github.com/stretchr/testify/require"
)

// TestCloudflareForwarders_StillTrustedByLibraryDefaults guards against the
// stale-allowlist risk of pinning our own Cloudflare snapshot (see
// docs/design/webmcp-reverse-gateway-phase2.ja.md「Cloudflare 信頼レンジ」): if a
// netinternet/remoteaddr version bump drops or changes one of these CIDRs
// from its bundled default list, this test fails so the diff gets reviewed
// instead of Manifold silently trusting a range Cloudflare no longer owns.
func TestCloudflareForwarders_StillTrustedByLibraryDefaults(t *testing.T) {
	libraryDefaults := remoteaddr.Parse().Forwarders
	for _, cidr := range cloudflareForwarders {
		require.Contains(t, libraryDefaults, cidr,
			"cloudflareForwarders is stale; refresh it (and the docs snapshot) from "+
				"remoteaddr's bundled default list after this dependency bump")
	}
}
