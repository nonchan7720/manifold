package httphandler

// rfc1918Forwarders are the private IPv4 ranges trusted as /edge/pair
// rate-limit forwarders unconditionally: unlike a publicly routable range, no
// internet client can spoof a connection that appears to originate from one
// of these, so trusting them regardless of deployment topology is safe.
var rfc1918Forwarders = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

// cloudflareForwarders is a pinned snapshot of Cloudflare's published edge IP
// ranges (https://www.cloudflare.com/ips/), copied from the
// github.com/netinternet/remoteaddr version pinned in go.mod. Trusting these
// is opt-in (edge.trustCloudflare) because a range Cloudflare later reclaims
// could otherwise let its new owner spoof CF-Connecting-IP.
// TestCloudflareForwarders_StillTrustedByLibraryDefaults fails on a
// remoteaddr version bump that drops any of these, surfacing staleness in the
// bump's CI run instead of it going unnoticed.
var cloudflareForwarders = []string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}
