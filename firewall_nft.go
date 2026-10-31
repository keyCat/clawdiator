package main

import (
	"errors"
	"os/exec"
	"strings"
)

// nftTable is the nftables table of the rules. The family inet covers IPv4 and IPv6.
const nftTable = "inet clawdiator"

// errNftMissing tells that the system has no program nft. Then only the hosts file blocks.
var errNftMissing = errors.New("the program nft is not installed, so the rules have no effect. Install the package nftables")

// renderNftRules returns the script for nft -f. The first 2 lines remove the old table, so 1 transaction replaces the table.
// The chain output covers the programs of this computer. The chain forward covers the routed traffic of the containers
// and the virtual machines, for example with the NAT of Docker or libvirt. A network bridge to the LAN goes past it.
// A drop is final in nftables: an accept in the table of a different firewall, for example ufw or firewalld, does not cancel it.
// The rules also stop the open connections, because nftables has no states that let packets past the rules.
func renderNftRules() string {
	var ipv4, ipv6 []string
	for _, network := range anthropicRanges {
		if strings.Contains(network, ":") {
			ipv6 = append(ipv6, network)
		} else {
			ipv4 = append(ipv4, network)
		}
	}
	drops := "\t\tip daddr { " + strings.Join(ipv4, ", ") + " } drop\n\t\tip6 daddr { " + strings.Join(ipv6, ", ") + " } drop\n"
	script := "add table " + nftTable + "\ndelete table " + nftTable + "\ntable " + nftTable + " {\n"
	for _, hook := range []string{"output", "forward"} {
		script += "\tchain " + hook + " {\n\t\ttype filter hook " + hook + " priority 0; policy accept;\n" + drops + "\t}\n"
	}
	return script + "}\n"
}

// runNft runs nft from the PATH. The PATH of a systemd service has /usr/sbin.
func runNft(input string, args ...string) (string, error) {
	path, err := exec.LookPath("nft")
	if err != nil {
		return "", errNftMissing
	}
	return runWithInput(path, input, args...)
}

// nftFirewall controls the table of clawdiator in nftables. nftables has no switch-on, so the token stays the same.
// The kernel does not keep the table after a restart, and "nft flush ruleset" removes it. Check loads it again.
type nftFirewall struct {
	run pfRunner
}

func (f *nftFirewall) Block(token string) (string, error) {
	return token, f.load()
}

// Unblock removes the table. A table that does not exist is not an error, for example after a restart of the computer.
func (f *nftFirewall) Unblock(string) error {
	if _, err := f.run("", strings.Fields("delete table "+nftTable)...); err != nil {
		// A list of the table fails only if the table does not exist.
		if _, listErr := f.run("", strings.Fields("list table "+nftTable)...); listErr == nil {
			return err
		}
	}
	return nil
}

// Check confirms that the table has all rules, and loads the table again if not. A quick check and a full check are the same.
func (f *nftFirewall) Check(token string, _ bool) (string, bool, error) {
	if output, err := f.run("", strings.Fields("list table "+nftTable)...); err == nil && hasNftRules(output) {
		return token, false, nil
	}
	if err := f.load(); err != nil {
		return token, false, err
	}
	return token, true, nil
}

func (f *nftFirewall) Place() string { return "nftables table " + nftTable }

func (f *nftFirewall) Rules() string { return renderNftRules() }

func (f *nftFirewall) load() error {
	_, err := f.run(renderNftRules(), "-f", "-")
	return err
}

// hasNftRules tells if the output of nft list table has a rule for each range in each of the 2 chains.
func hasNftRules(output string) bool {
	for _, network := range anthropicRanges {
		if strings.Count(output, network) < 2 {
			return false
		}
	}
	return true
}
