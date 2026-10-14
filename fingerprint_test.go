package main

import (
	"net/netip"
	"testing"
)

func TestBuildNetworkFingerprint(t *testing.T) {
	address := netip.MustParseAddr
	wifi := networkInterface{Name: "en0", IsUp: true, Addresses: []netip.Addr{
		address("192.168.1.20"), address("fe80::1"), address("2001:db8:1:2::aaaa"), address("2001:db8:1:2::bbbb"),
	}}
	base := buildNetworkFingerprint([]networkInterface{wifi})
	if base != "en0=192.168.1.20,2001:db8:1:2::/64" {
		t.Errorf("fingerprint = %q", base)
	}
	rotated := wifi
	rotated.Addresses = []netip.Addr{address("192.168.1.20"), address("2001:db8:1:2::cccc")}
	if got := buildNetworkFingerprint([]networkInterface{rotated}); got != base {
		t.Errorf("a new temporary IPv6 address must not change the fingerprint: %q", got)
	}
	ignored := []networkInterface{
		wifi,
		{Name: "lo0", IsUp: true, IsLoopback: true, Addresses: []netip.Addr{address("127.0.0.1")}},
		{Name: "bridge100", IsUp: true, Addresses: []netip.Addr{address("192.168.64.1")}},
		{Name: "en5", IsUp: false, Addresses: []netip.Addr{address("10.0.0.5")}},
		{Name: "utun3", IsUp: true, Addresses: []netip.Addr{address("fe80::2")}},
	}
	if got := buildNetworkFingerprint(ignored); got != base {
		t.Errorf("loopback, bridges, stopped interfaces and link-local addresses must not count: %q", got)
	}
	vpn := append(ignored, networkInterface{Name: "utun4", IsUp: true, Addresses: []netip.Addr{address("100.101.102.103")}})
	if got := buildNetworkFingerprint(vpn); got == base {
		t.Error("a VPN with a CGNAT address must change the fingerprint")
	}
	moved := wifi
	moved.Addresses = []netip.Addr{address("10.10.0.7")}
	if got := buildNetworkFingerprint([]networkInterface{moved}); got == base {
		t.Error("a new IPv4 address must change the fingerprint")
	}
	if got := buildNetworkFingerprint(nil); got != "" {
		t.Errorf("without a network, the fingerprint must be empty: %q", got)
	}
}
