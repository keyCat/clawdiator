package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// anthropicRanges are the published ranges of the addresses where Anthropic receives requests.
// Source: https://docs.claude.com/en/api/ip-addresses. The ranges are from anthropicRangesDate.
var anthropicRanges = []string{"160.79.104.0/23", "2607:6bc0::/48"}

const (
	anthropicRangesDate = "2026-10-14"
	// pfAnchor is the anchor of the rules. /etc/pf.conf of macOS has the line anchor "com.apple/*",
	// so pf evaluates this anchor without a change of /etc/pf.conf.
	pfAnchor = "com.apple/clawdiator"
	// pfAppleAnchorRule is the line of the main rule set that makes pf evaluate the anchors of Apple.
	pfAppleAnchorRule = `anchor "com.apple/*"`
	// firewallCheckInterval is the time between 2 quick checks of the rules during a block. A quick check runs 2 short pfctl commands.
	firewallCheckInterval = time.Second
	// firewallFullCheckInterval is the time between 2 full checks. A full check also reads the token and the main rule set.
	firewallFullCheckInterval = 5 * time.Second
	// firewallRetryInterval is the time between 2 tries of a failed removal of the rules.
	firewallRetryInterval = 30 * time.Second
)

// errFirewallNoEffect tells that pf does not evaluate the anchor of clawdiator. The service cannot repair this,
// because it does not change the main rule set.
var errFirewallNoEffect = errors.New(`the main rule set of pf has no line ` + pfAppleAnchorRule + `, so the rules have no effect`)

// renderPFRules returns the rules of the anchor. pf makes 1 rule for IPv4 and 1 rule for IPv6 from the list.
// The rule drops the packets and sends nothing. The test of 2026-10-16 showed that "block return" on macOS sends a reset
// with the address of Anthropic as the source into the network interface, for example into a VPN. The app did not get the reset,
// so "return" gave no benefit.
func renderPFRules() string {
	return "block drop out quick to { " + strings.Join(anthropicRanges, ", ") + " }\n"
}

// pfRunner runs pfctl with the arguments and the input. It returns the output and an error for an exit code that is not 0.
type pfRunner func(input string, args ...string) (string, error)

// runPfctl runs /sbin/pfctl. pfctl writes warnings to its error output also on success, so only the exit code tells a failure.
func runPfctl(input string, args ...string) (string, error) {
	command := exec.Command("/sbin/pfctl", args...)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	text := string(bytes.TrimSpace(output))
	if err != nil {
		return text, fmt.Errorf("pfctl %s: %w: %s", strings.Join(args, " "), err, cleanLogText(text))
	}
	return text, nil
}

// pfFirewall controls the rules of clawdiator in pf. The caller keeps the token of the switch-on of pf.
// The token is 1 reference to the switch-on. pf stays on while other software holds a reference.
// Only the goroutine of the loop uses it.
type pfFirewall struct {
	run pfRunner
	// appleAnchorErr is the last result of the check of the anchor of Apple. A quick check does not read the main rule set,
	// so it returns this result. Then the error does not come and go between a quick check and a full check.
	appleAnchorErr error
}

// newFirewall returns the firewall of the system. Only macOS has firewall rules now. On other systems, the result is nil.
func newFirewall() *pfFirewall {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return &pfFirewall{run: runPfctl}
}

// Block loads the rules, turns pf on and removes the states of the open connections to the ranges.
// In this order, pf is never on with an empty anchor of clawdiator. The result is the token to keep.
// The error is errFirewallNoEffect if pf does not evaluate the anchor. The token is valid also then.
func (f *pfFirewall) Block(token string) (string, error) {
	if err := f.load(); err != nil {
		return token, err
	}
	token, err := f.enable(token)
	if err != nil {
		return token, err
	}
	if err := f.killStates(); err != nil {
		return token, err
	}
	return token, f.checkAppleAnchor()
}

// checkAppleAnchor reads the main rule set and keeps the result for the quick checks.
func (f *pfFirewall) checkAppleAnchor() error {
	output, err := f.run("", "-s", "rules")
	switch {
	case err != nil:
		return err
	case !hasPFAppleAnchor(output):
		f.appleAnchorErr = errFirewallNoEffect
	default:
		f.appleAnchorErr = nil
	}
	return f.appleAnchorErr
}

// Unblock removes the rules and releases the token. A token that pf does not know is not an error,
// for example a token from before a restart of the computer. An anchor that does not exist is not an error either.
func (f *pfFirewall) Unblock(token string) error {
	f.appleAnchorErr = nil
	if _, err := f.run("", "-a", pfAnchor, "-F", "rules"); err != nil {
		// The removal fails only if the rules are still in pf. pfctl can fail for an anchor that does not exist.
		if output, showErr := f.run("", "-a", pfAnchor, "-s", "rules"); showErr == nil && hasPFRules(output) {
			return err
		}
	}
	return f.release(token)
}

// Check confirms that the rules are in the anchor and that pf is on. It repairs both. The result is the token to keep,
// and if the check repaired something. The error is errFirewallNoEffect if pf does not evaluate the anchor.
//
// A quick check runs 2 commands: it reads the rules of the anchor and the status of pf.
// A full check also confirms that pf knows the token, and it reads the anchor of Apple in the main rule set.
// A quick check returns the last result of the check of the anchor of Apple.
func (f *pfFirewall) Check(token string, isFull bool) (string, bool, error) {
	isRepaired := false
	if output, err := f.run("", "-a", pfAnchor, "-s", "rules"); err != nil || !hasPFRules(output) {
		// pfctl fails for an anchor that does not exist. The load makes the anchor again.
		if err := f.load(); err != nil {
			return token, isRepaired, err
		}
		isRepaired = true
	}
	isEnabled, err := f.isEnabled()
	if err != nil {
		return token, isRepaired, err
	}
	if !isEnabled || (isFull && !f.hasReference(token)) {
		if token, err = f.enable(token); err != nil {
			return token, isRepaired, err
		}
		isRepaired = true
	}
	if isRepaired {
		if err := f.killStates(); err != nil {
			return token, isRepaired, err
		}
	}
	if isFull || isRepaired {
		return token, isRepaired, f.checkAppleAnchor()
	}
	return token, isRepaired, f.appleAnchorErr
}

func (f *pfFirewall) load() error {
	_, err := f.run(renderPFRules(), "-a", pfAnchor, "-f", "-")
	return err
}

// enable makes sure that pf is on with exactly 1 token of clawdiator. If pf knows the token and pf is on, it keeps the token.
// If pf knows the token but pf is off, for example after pfctl -d, it releases the token and takes a new one.
func (f *pfFirewall) enable(token string) (string, error) {
	isEnabled, err := f.isEnabled()
	if err != nil {
		return token, err
	}
	if f.hasReference(token) {
		if isEnabled {
			return token, nil
		}
		if err := f.release(token); err != nil {
			return token, err
		}
	}
	output, err := f.run("", "-E")
	if err != nil {
		return "", err
	}
	newToken := parsePFToken(output)
	if newToken == "" {
		return "", fmt.Errorf("pfctl -E gave no token: %s", cleanLogText(output))
	}
	return newToken, nil
}

// release releases the token, if pf knows it.
func (f *pfFirewall) release(token string) error {
	if !f.hasReference(token) {
		return nil
	}
	_, err := f.run("", "-X", token)
	return err
}

// hasReference tells if pf knows the token. An empty token or a failed read gives false.
func (f *pfFirewall) hasReference(token string) bool {
	if token == "" {
		return false
	}
	output, err := f.run("", "-s", "References")
	return err == nil && hasPFReference(output, token)
}

func (f *pfFirewall) isEnabled() (bool, error) {
	output, err := f.run("", "-s", "info")
	if err != nil {
		return false, err
	}
	return isPFEnabled(output), nil
}

// killStates removes the states of the connections to the ranges. Then the rule also stops the open connections.
func (f *pfFirewall) killStates() error {
	for _, network := range anthropicRanges {
		everyAddress := "0.0.0.0/0"
		if strings.Contains(network, ":") {
			everyAddress = "::/0"
		}
		if _, err := f.run("", "-k", everyAddress, "-k", network); err != nil {
			return err
		}
	}
	return nil
}

// hasPFAppleAnchor tells if the main rule set has a filter anchor for the anchors of Apple.
// The line must start with the anchor: the line `scrub-anchor "com.apple/*"` also contains its text, but it does not filter.
func hasPFAppleAnchor(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), pfAppleAnchorRule) {
			return true
		}
	}
	return false
}

// parsePFToken reads the token from the output of pfctl -E, for example from the line "Token : 1234567890".
func parsePFToken(output string) string {
	for _, line := range strings.Split(output, "\n") {
		name, value, isFound := strings.Cut(line, ":")
		if isFound && strings.TrimSpace(name) == "Token" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// hasPFReference tells if the output of pfctl -s References lists the token as a separate field.
func hasPFReference(output, token string) bool {
	for _, line := range strings.Split(output, "\n") {
		for _, field := range strings.Fields(line) {
			if field == token {
				return true
			}
		}
	}
	return false
}

// isPFEnabled reads the line "Status: Enabled" of pfctl -s info.
func isPFEnabled(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if value, isFound := strings.CutPrefix(strings.TrimSpace(line), "Status:"); isFound {
			return strings.HasPrefix(strings.TrimSpace(value), "Enabled")
		}
	}
	return false
}

// hasPFRules tells if the output of pfctl -s rules for the anchor contains a rule for each range.
func hasPFRules(output string) bool {
	for _, network := range anthropicRanges {
		if !strings.Contains(output, network) {
			return false
		}
	}
	return true
}
