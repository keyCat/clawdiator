package main

import (
	"net"
	"net/netip"
	"slices"
	"strings"
)

// ignoredInterfacePrefixes are the names of the interfaces of virtual machines, containers and Apple services.
// A change of these interfaces does not move the computer to another network.
var ignoredInterfacePrefixes = []string{"bridge", "vmnet", "vboxnet", "docker", "veth", "br-", "virbr", "awdl", "llw", "anpi"}

// networkInterface is 1 interface with its addresses, the input of buildNetworkFingerprint.
type networkInterface struct {
	Name       string
	IsUp       bool
	IsLoopback bool
	Addresses  []netip.Addr
}

// buildNetworkFingerprint returns a text that changes when the computer moves to another network.
// It uses the interfaces that are up and have global addresses: the IPv4 addresses and the IPv6 /64 prefixes.
// The prefixes ignore the temporary IPv6 addresses, because the system changes them often.
// Private and CGNAT addresses count too, so a VPN that connects or disconnects also changes the fingerprint.
func buildNetworkFingerprint(interfaces []networkInterface) string {
	var parts []string
	for _, item := range interfaces {
		if !item.IsUp || item.IsLoopback || slices.ContainsFunc(ignoredInterfacePrefixes, func(prefix string) bool { return strings.HasPrefix(item.Name, prefix) }) {
			continue
		}
		var addresses []string
		for _, address := range item.Addresses {
			address = address.Unmap()
			if !address.IsGlobalUnicast() {
				continue
			}
			if address.Is4() {
				addresses = append(addresses, address.String())
				continue
			}
			prefix, _ := address.Prefix(64)
			addresses = append(addresses, prefix.String())
		}
		if len(addresses) == 0 {
			continue
		}
		slices.Sort(addresses)
		parts = append(parts, item.Name+"="+strings.Join(slices.Compact(addresses), ","))
	}
	slices.Sort(parts)
	return strings.Join(parts, ";")
}

// getNetworkFingerprint reads the interfaces of the system. Without a network, the result is empty.
func getNetworkFingerprint() string {
	systemInterfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var interfaces []networkInterface
	for _, systemInterface := range systemInterfaces {
		item := networkInterface{
			Name:       systemInterface.Name,
			IsUp:       systemInterface.Flags&net.FlagUp != 0,
			IsLoopback: systemInterface.Flags&net.FlagLoopback != 0,
		}
		addresses, err := systemInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if network, isNetwork := address.(*net.IPNet); isNetwork {
				if parsed, isParsed := netip.AddrFromSlice(network.IP); isParsed {
					item.Addresses = append(item.Addresses, parsed)
				}
			}
		}
		interfaces = append(interfaces, item)
	}
	return buildNetworkFingerprint(interfaces)
}
