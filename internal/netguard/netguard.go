// Package netguard classifies addresses reachable by agent-supplied URL fetches.
//
// Selectively copied from Magpie internal/netguard/netguard.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022. Magpie is Copyright (c) 2026
// yetone, MIT licensed; see THIRD_PARTY_NOTICES.md.
package netguard

import "net/netip"

// specialNets are ranges not covered by the netip checks in Public.
var specialNets = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "this" network
		"100.64.0.0/10",   // CGNAT, Tailscale
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking and proxy fake-IP answers
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved, broadcast
		"::/96",           // IPv4-compatible (deprecated)
		"100::/64",        // discard
		"2001::/32",       // Teredo
		"2001:2::/48",     // benchmarking
		"2001:db8::/32",   // documentation
		"fec0::/10",       // site-local (deprecated)
		"64:ff9b:1::/48",  // local-use NAT64
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// Public reports whether a is safe for an agent-supplied URL fetch. Loopback,
// private, link-local, CGNAT, unspecified, multicast, documentation, reserved,
// benchmarking, Teredo, and IPv4-mapped/NAT64/6to4 forms of those addresses
// are not public. There is deliberately no private-address bypass.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return false
	}
	for _, n := range specialNets {
		if n.Contains(a) {
			return false
		}
	}
	if a.Is6() {
		b := a.As16()
		if nat64.Contains(a) {
			return Public(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
		if b[0] == 0x20 && b[1] == 0x02 {
			return Public(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	return true
}
