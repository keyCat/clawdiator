package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fakePF simulates pfctl: the switch-on of pf with references, the anchor of clawdiator and the main rule set.
type fakePF struct {
	isEnabled bool
	// tokens holds the references to the switch-on. pf is on while it has a reference.
	tokens    []string
	nextToken int
	// anchor is the loaded rule set of the anchor of clawdiator. isAnchorKnown is false before the first load.
	anchor         string
	isAnchorKnown  bool
	hasAppleAnchor bool
	// failOn makes each call fail that contains this text in its arguments.
	failOn string
	calls  []string
}

func newFakePF() *fakePF {
	return &fakePF{hasAppleAnchor: true}
}

func (p *fakePF) run(input string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	p.calls = append(p.calls, joined)
	if p.failOn != "" && strings.Contains(joined, p.failOn) {
		// The same form as the error of runPfctl.
		return "pfctl: Operation not permitted", fmt.Errorf("pfctl %s: exit status 1: pfctl: Operation not permitted", joined)
	}
	switch {
	case joined == "-a com.apple/clawdiator -f -":
		p.anchor, p.isAnchorKnown = input, true
		return "No ALTQ support in kernel\nALTQ related functions disabled", nil
	case joined == "-a com.apple/clawdiator -F rules":
		p.anchor = ""
		return "rules cleared", nil
	case joined == "-a com.apple/clawdiator -s rules":
		if !p.isAnchorKnown {
			return "pfctl: DIOCGETRULES: Invalid argument", errors.New("exit status 1")
		}
		var lines []string
		if strings.Contains(p.anchor, "160.79.104.0/23") {
			lines = append(lines, "block drop out quick inet from any to 160.79.104.0/23")
		}
		if strings.Contains(p.anchor, "2607:6bc0::/48") {
			lines = append(lines, "block drop out quick inet6 from any to 2607:6bc0::/48")
		}
		return strings.Join(lines, "\n"), nil
	case joined == "-s rules":
		rules := `scrub-anchor "com.apple/*" all fragment reassemble`
		if p.hasAppleAnchor {
			rules += "\n" + `anchor "com.apple/*" all`
		}
		return rules, nil
	case joined == "-s info":
		// The real pfctl writes the lines about ALTQ first, also on success.
		if p.isEnabled {
			return "No ALTQ support in kernel\nALTQ related functions disabled\nStatus: Enabled for 0 days 00:00:05           Debug: Urgent", nil
		}
		return "No ALTQ support in kernel\nALTQ related functions disabled\nStatus: Disabled for 0 days 00:10:00          Debug: Urgent", nil
	case joined == "-s References":
		lines := []string{"TOKENS:", "PID      Process Name                TOKEN                     TIMESTAMP"}
		for _, token := range p.tokens {
			lines = append(lines, fmt.Sprintf("123      pfctl                       %s         Thu Oct 15 10:00:00 2026", token))
		}
		return strings.Join(lines, "\n"), nil
	case joined == "-E":
		p.nextToken++
		token := fmt.Sprintf("%d", 1844674407370955100+p.nextToken)
		p.tokens = append(p.tokens, token)
		p.isEnabled = true
		return "No ALTQ support in kernel\npf enabled\nToken : " + token, nil
	case len(args) == 2 && args[0] == "-X":
		if !slices.Contains(p.tokens, args[1]) {
			return "pfctl: DIOCSTOPREF: Invalid argument", errors.New("exit status 1")
		}
		p.tokens = slices.DeleteFunc(p.tokens, func(token string) bool { return token == args[1] })
		if len(p.tokens) == 0 {
			p.isEnabled = false
		}
		return "pf disabled", nil
	case len(args) == 4 && args[0] == "-k":
		return "killed 1 states from 1 sources and 0 destinations", nil
	}
	return "", fmt.Errorf("the fake pfctl does not know %q", joined)
}

// countCalls returns the number of calls with the arguments.
func (p *fakePF) countCalls(joined string) int {
	count := 0
	for _, call := range p.calls {
		if call == joined {
			count++
		}
	}
	return count
}

func TestRenderPFRules(t *testing.T) {
	if got := renderPFRules(); got != "block drop out quick to { 160.79.104.0/23, 2607:6bc0::/48 }\n" {
		t.Errorf("renderPFRules = %q", got)
	}
}

func TestPFParsers(t *testing.T) {
	if got := parsePFToken("No ALTQ support in kernel\npf enabled\nToken : 18446744073709551615\n"); got != "18446744073709551615" {
		t.Errorf("parsePFToken = %q", got)
	}
	if got := parsePFToken("pf enabled"); got != "" {
		t.Errorf("an output without a token line must give no token: %q", got)
	}
	if !isPFEnabled("Status: Enabled for 0 days 00:00:05   Debug: Urgent") || isPFEnabled("Status: Disabled for 0 days") || isPFEnabled("") {
		t.Error("isPFEnabled must read the Status line")
	}
	references := "TOKENS:\nPID  Process Name  TOKEN  TIMESTAMP\n123  pfctl  42  Thu Oct 15 10:00:00 2026"
	if !hasPFReference(references, "42") || hasPFReference(references, "4") || hasPFReference(references, "123 ") {
		t.Error("hasPFReference must compare whole fields")
	}
	if hasPFAppleAnchor(`scrub-anchor "com.apple/*" all fragment reassemble`) || !hasPFAppleAnchor("x\n  anchor \"com.apple/*\" all") {
		t.Error("hasPFAppleAnchor must not count the scrub anchor")
	}
	if !hasPFRules("block drop out quick inet from any to 160.79.104.0/23\nblock drop out quick inet6 from any to 2607:6bc0::/48") ||
		hasPFRules("block drop out quick inet from any to 160.79.104.0/23") {
		t.Error("hasPFRules must find a rule for each range")
	}
}

func TestFirewallBlockAndUnblock(t *testing.T) {
	pf := newFakePF()
	firewall := &pfFirewall{run: pf.run}
	token, err := firewall.Block("")
	if err != nil || token == "" || !pf.isEnabled || !slices.Equal(pf.tokens, []string{token}) || !hasPFRules(pf.anchor) {
		t.Fatalf("Block must load the rules and turn pf on with 1 token: %q, %v, %+v", token, err, pf)
	}
	load, enable := slices.Index(pf.calls, "-a com.apple/clawdiator -f -"), slices.Index(pf.calls, "-E")
	kill := slices.Index(pf.calls, "-k 0.0.0.0/0 -k 160.79.104.0/23")
	if load < 0 || enable < load || kill < enable || pf.countCalls("-k ::/0 -k 2607:6bc0::/48") != 1 {
		t.Errorf("the order must be: load, switch-on, removal of the states: %q", pf.calls)
	}
	if err := firewall.Unblock(token); err != nil || pf.anchor != "" || len(pf.tokens) != 0 || pf.isEnabled {
		t.Errorf("Unblock must remove the rules and release the token: %v, %+v", err, pf)
	}
	flush, release := slices.Index(pf.calls, "-a com.apple/clawdiator -F rules"), slices.Index(pf.calls, "-X "+token)
	if flush < 0 || release < flush {
		t.Errorf("the rules must go before the release of the token: %q", pf.calls)
	}
	if slices.ContainsFunc(pf.calls, func(call string) bool { return strings.Contains(call, "-F all") }) {
		t.Error("the firewall must never flush all, because that can also clear the states")
	}
}

// The service holds exactly 1 token. A second block, a restart of the service and other software must not add a token.
func TestFirewallKeepsOneToken(t *testing.T) {
	pf := newFakePF()
	pf.tokens, pf.isEnabled = []string{"500"}, true
	firewall := &pfFirewall{run: pf.run}
	token, _ := firewall.Block("")
	again, _ := firewall.Block(token)
	if again != token || len(pf.tokens) != 2 || pf.countCalls("-E") != 1 {
		t.Errorf("a new process must take over the stored token: %q, %q, %v", token, again, pf.tokens)
	}
	if err := firewall.Unblock(token); err != nil || !pf.isEnabled || !slices.Equal(pf.tokens, []string{"500"}) {
		t.Errorf("pf must stay on for the reference of other software: %v, %+v", err, pf)
	}

	// After a restart of the computer, pf does not know the stored token.
	restarted := newFakePF()
	firewall.run = restarted.run
	token, err := firewall.Block("999")
	if err != nil || len(restarted.tokens) != 1 || token != restarted.tokens[0] || token == "999" {
		t.Errorf("a token from before the restart must go, and a new token must come: %q, %v, %v", token, err, restarted.tokens)
	}
	if err := firewall.Unblock("999"); err != nil || restarted.countCalls("-X 999") != 0 {
		t.Errorf("the release of an unknown token must do nothing: %v, %q", err, restarted.calls)
	}
}

func TestFirewallCheckRepairs(t *testing.T) {
	pf := newFakePF()
	firewall := &pfFirewall{run: pf.run}
	token, _ := firewall.Block("")
	if again, isRepaired, err := firewall.Check(token, true); again != token || isRepaired || err != nil {
		t.Errorf("a check without a problem must repair nothing: %q, %v, %v", again, isRepaired, err)
	}

	// Other software loads a new rule set and removes the rules.
	pf.anchor = ""
	kills := pf.countCalls("-k 0.0.0.0/0 -k 160.79.104.0/23")
	if again, isRepaired, err := firewall.Check(token, true); again != token || !isRepaired || err != nil || !hasPFRules(pf.anchor) {
		t.Errorf("the check must load the rules again: %q, %v, %v", again, isRepaired, err)
	}
	if pf.countCalls("-k 0.0.0.0/0 -k 160.79.104.0/23") != kills+1 {
		t.Error("after a repair, the check must also remove the states")
	}

	// pfctl -d turns pf off, but pf keeps the reference.
	pf.isEnabled = false
	token, isRepaired, err := firewall.Check(token, true)
	if !isRepaired || err != nil || !pf.isEnabled || !slices.Equal(pf.tokens, []string{token}) {
		t.Errorf("the check must turn pf on again with exactly 1 token: %v, %v, %+v", isRepaired, err, pf)
	}

	// pfctl -d turns pf off, and pf forgets the references.
	pf.isEnabled, pf.tokens = false, nil
	token, isRepaired, err = firewall.Check(token, true)
	if !isRepaired || err != nil || !pf.isEnabled || !slices.Equal(pf.tokens, []string{token}) {
		t.Errorf("the check must take a new token: %v, %v, %+v", isRepaired, err, pf)
	}
}

func TestFirewallWithoutAppleAnchor(t *testing.T) {
	pf := newFakePF()
	pf.hasAppleAnchor = false
	firewall := &pfFirewall{run: pf.run}
	token, err := firewall.Block("")
	if !errors.Is(err, errFirewallNoEffect) || token == "" || !hasPFRules(pf.anchor) {
		t.Errorf("without the anchor of Apple, the rules have no effect, but the token must stay: %q, %v", token, err)
	}
	if _, _, err := firewall.Check(token, true); !errors.Is(err, errFirewallNoEffect) {
		t.Errorf("the check must find the missing anchor of Apple: %v", err)
	}
}

func TestFirewallErrors(t *testing.T) {
	pf := newFakePF()
	pf.failOn = "-E"
	firewall := &pfFirewall{run: pf.run}
	if token, err := firewall.Block(""); err == nil || token != "" {
		t.Errorf("a failed switch-on must give an error and no token: %q, %v", token, err)
	}

	// pfctl can fail for an anchor that does not exist. Without rules, that is not an error.
	empty := newFakePF()
	empty.failOn = "-F rules"
	firewall.run = empty.run
	if err := firewall.Unblock(""); err != nil {
		t.Errorf("an anchor that does not exist must not give an error: %v", err)
	}
	empty.isAnchorKnown, empty.anchor = true, renderPFRules()
	if err := firewall.Unblock(""); err == nil {
		t.Error("a failed removal of rules that stay must give an error")
	}
}

// A quick check runs 2 commands. It keeps the result of the last check of the anchor of Apple,
// so the error does not come and go between the quick checks and the full checks.
func TestFirewallQuickCheck(t *testing.T) {
	pf := newFakePF()
	pf.hasAppleAnchor = false
	firewall := &pfFirewall{run: pf.run}
	token, err := firewall.Block("")
	if !errors.Is(err, errFirewallNoEffect) {
		t.Fatalf("the block must find the missing anchor of Apple: %v", err)
	}
	pf.calls = nil
	if _, isRepaired, err := firewall.Check(token, false); isRepaired || !errors.Is(err, errFirewallNoEffect) {
		t.Errorf("a quick check must keep the error of the anchor of Apple: %v, %v", isRepaired, err)
	}
	if !slices.Equal(pf.calls, []string{"-a com.apple/clawdiator -s rules", "-s info"}) {
		t.Errorf("a quick check must run only 2 commands: %q", pf.calls)
	}
	pf.hasAppleAnchor = true
	if _, _, err := firewall.Check(token, false); !errors.Is(err, errFirewallNoEffect) {
		t.Errorf("only a full check reads the main rule set again: %v", err)
	}
	if _, _, err := firewall.Check(token, true); err != nil {
		t.Errorf("a full check must find the anchor of Apple again: %v", err)
	}
	if _, _, err := firewall.Check(token, false); err != nil {
		t.Errorf("after a full check without an error, a quick check must give no error: %v", err)
	}

	// Other software releases the token of clawdiator, but pf stays on. Only a full check finds this.
	pf.tokens = nil
	if again, _, _ := firewall.Check(token, false); again != token || len(pf.tokens) != 0 {
		t.Errorf("a quick check must not read the token: %v", pf.tokens)
	}
	if again, isRepaired, _ := firewall.Check(token, true); !isRepaired || len(pf.tokens) != 1 || again != pf.tokens[0] {
		t.Errorf("a full check must take a new token: %v, %v", isRepaired, pf.tokens)
	}
}
