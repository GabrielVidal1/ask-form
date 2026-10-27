package relay

import (
	"net"
	"net/http"
	"strings"
)

// cloudflareNets are Cloudflare's edge ranges (www.cloudflare.com/ips-v4 and
// ips-v6, 2026-10-28). They change rarely; a stale list only means a visitor
// coming through a new range is counted under the edge's address.
var cloudflareNets = parseNets(`
173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18
108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17
162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22
2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32
2a06:98c0::/29 2c0f:f248::/32`)

func parseNets(s string) []*net.IPNet {
	var out []*net.IPNet
	for _, f := range strings.Fields(s) {
		if _, n, err := net.ParseCIDR(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func fromCloudflare(ip net.IP) bool {
	for _, n := range cloudflareNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// cloudflareVisitor returns the Cf-Connecting-Ip of a request that came
// through Cloudflare's proxy — only when peer (the address the proxy in front
// of the relay saw) is a Cloudflare edge, since anyone can send the header.
func cloudflareVisitor(req *http.Request, peer string) string {
	ip := net.ParseIP(peer)
	if ip == nil || !fromCloudflare(ip) {
		return ""
	}
	if v := strings.TrimSpace(req.Header.Get("Cf-Connecting-Ip")); net.ParseIP(v) != nil {
		return v
	}
	return ""
}
