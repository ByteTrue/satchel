package netguard

import (
	"net/netip"
	"testing"
)

func TestPublic(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1":         false,
		"10.1.2.3":          false,
		"172.16.0.1":        false,
		"192.168.1.1":       false,
		"169.254.169.254":   false,
		"100.64.0.1":        false,
		"0.0.0.0":           false,
		"224.0.0.1":         false,
		"192.0.2.1":         false,
		"198.51.100.1":      false,
		"203.0.113.1":       false,
		"240.0.0.1":         false,
		"198.18.0.1":        false,
		"::1":               false,
		"::ffff:127.0.0.1":  false,
		"::ffff:198.18.0.7": false,
		"fc00::1":           false,
		"fe80::1":           false,
		"2001::1":           false,
		"2001:2::1":         false,
		"2001:2:1::1":       true,
		"2001:db8::1":       false,
		"64:ff9b::7f00:1":   false,
		"2002:c612:7::":     false,
		"8.8.8.8":           true,
		"93.184.216.34":     true,
		"198.17.255.255":    true,
		"198.20.0.0":        true,
		"2606:4700::1111":   true,
		"64:ff9b::808:808":  true,
		"::ffff:8.8.8.8":    true,
	} {
		if got := Public(netip.MustParseAddr(address)); got != want {
			t.Errorf("Public(%s) = %v, want %v", address, got, want)
		}
	}
	if Public(netip.Addr{}) {
		t.Error("an invalid address must not be public")
	}
}
