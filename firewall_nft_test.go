package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// nftListOutput is the output of "nft list table inet clawdiator" of nftables 1.1.6 after the load of renderNftRules.
const nftListOutput = `table inet clawdiator {
	chain output {
		type filter hook output priority filter; policy accept;
		ip daddr 160.79.104.0/23 drop
		ip6 daddr 2607:6bc0::/48 drop
	}

	chain forward {
		type filter hook forward priority filter; policy accept;
		ip daddr 160.79.104.0/23 drop
		ip6 daddr 2607:6bc0::/48 drop
	}
}`

// fakeNft simulates nft with the table of clawdiator. table is the output of the list, or empty if the table does not exist.
type fakeNft struct {
	table       string
	isMissing   bool
	isDeleteBad bool
	calls       []string
}

func (n *fakeNft) run(input string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	n.calls = append(n.calls, joined)
	if n.isMissing {
		return "", errNftMissing
	}
	notFound := fmt.Errorf("nft %s: exit status 1: Error: No such file or directory", joined)
	switch {
	case joined == "-f -" && input == renderNftRules():
		n.table = nftListOutput
		return "", nil
	case joined == "list table inet clawdiator" && n.table != "":
		return n.table, nil
	case joined == "list table inet clawdiator":
		return "", notFound
	case joined == "delete table inet clawdiator" && n.isDeleteBad:
		return "", errors.New("nft delete table inet clawdiator: exit status 1: Error: Operation not permitted")
	case joined == "delete table inet clawdiator" && n.table != "":
		n.table = ""
		return "", nil
	case joined == "delete table inet clawdiator":
		return "", notFound
	}
	return "", fmt.Errorf("unexpected call: nft %s", joined)
}

// The script is the same as the script of the test in an Ubuntu VM with nftables 1.1.6 on 2026-10-31.
func TestRenderNftRules(t *testing.T) {
	want := `add table inet clawdiator
delete table inet clawdiator
table inet clawdiator {
	chain output {
		type filter hook output priority 0; policy accept;
		ip daddr { 160.79.104.0/23 } drop
		ip6 daddr { 2607:6bc0::/48 } drop
	}
	chain forward {
		type filter hook forward priority 0; policy accept;
		ip daddr { 160.79.104.0/23 } drop
		ip6 daddr { 2607:6bc0::/48 } drop
	}
}
`
	if got := renderNftRules(); got != want {
		t.Errorf("renderNftRules =\n%s\nwant:\n%s", got, want)
	}
}

func TestNftFirewall(t *testing.T) {
	nft := &fakeNft{}
	rules := &nftFirewall{run: nft.run}
	if token, err := rules.Block(""); err != nil || token != "" || nft.table != nftListOutput {
		t.Fatalf("Block = %q, %v, table %q", token, err, nft.table)
	}
	nft.calls = nil
	if _, isRepaired, err := rules.Check("", false); isRepaired || err != nil || len(nft.calls) != 1 {
		t.Errorf("a check of a complete table must only read it: repaired %v, error %v, calls %q", isRepaired, err, nft.calls)
	}

	// "nft flush ruleset" removes the table. A different program can also remove 1 chain.
	forward := nftListOutput[strings.Index(nftListOutput, "\n\n\tchain forward"):strings.LastIndex(nftListOutput, "\n}")]
	lastRule := strings.LastIndex(nftListOutput, "\t\tip6 daddr")
	for name, table := range map[string]string{
		"no table":                "",
		"no chain forward":        strings.Replace(nftListOutput, forward, "", 1),
		"no IPv6 rule in 1 chain": nftListOutput[:lastRule] + nftListOutput[strings.Index(nftListOutput[lastRule:], "\n")+lastRule+1:],
	} {
		nft.table = table
		if _, isRepaired, err := rules.Check("", true); !isRepaired || err != nil || nft.table != nftListOutput {
			t.Errorf("%s: the check must load the table again: repaired %v, error %v", name, isRepaired, err)
		}
	}

	nft.isDeleteBad = true
	if err := rules.Unblock(""); err == nil {
		t.Error("Unblock must fail when the table stays")
	}
	nft.isDeleteBad = false
	if err := rules.Unblock(""); err != nil || nft.table != "" {
		t.Errorf("Unblock = %v, table %q", err, nft.table)
	}
	if err := rules.Unblock(""); err != nil {
		t.Errorf("a table that does not exist is not an error: %v", err)
	}
}

func TestNftFirewallWithoutNft(t *testing.T) {
	rules := &nftFirewall{run: (&fakeNft{isMissing: true}).run}
	if _, err := rules.Block(""); !errors.Is(err, errNftMissing) {
		t.Errorf("Block = %v", err)
	}
	if _, isRepaired, err := rules.Check("", false); isRepaired || !errors.Is(err, errNftMissing) {
		t.Errorf("Check = %v, %v", isRepaired, err)
	}
	if err := rules.Unblock(""); err != nil {
		t.Errorf("without nft, no table can exist, so Unblock = %v", err)
	}
}

// A change of the consent text of macOS needs a new consentVersion. Then the checksums also change.
func TestConsentText(t *testing.T) {
	sums := map[language]string{
		languageEnglish: "6f509b436ae55cee27489c7899a8443120c9ce48015a7e9ac8ee68034e384b3f",
		languageRussian: "7d03e4e3a5b814391c9b194f7b52ccf7cfd49651a661cc23a272d109c987f5b4",
	}
	if consentVersion != 4 {
		t.Fatal("the checksums are from consentVersion 4")
	}
	for lang, sum := range sums {
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(getConsentText(lang, "darwin")))); got != sum {
			t.Errorf("the consent text of macOS in language %d changed:\n%s", lang, getConsentText(lang, "darwin"))
		}
		linux := getConsentText(lang, "linux")
		if strings.Contains(linux, "macOS") || !strings.Contains(linux, "nftables") || strings.Contains(linux, "%!") {
			t.Errorf("the consent text of Linux in language %d:\n%s", lang, linux)
		}
	}
}
