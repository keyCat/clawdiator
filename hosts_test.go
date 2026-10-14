package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"geocheck"
)

const sampleHosts = "##\n# Host Database\n##\n127.0.0.1\tlocalhost\n255.255.255.255\tbroadcasthost\n::1             localhost\n"

var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func TestHostListsAreCorrect(t *testing.T) {
	seen := map[string]bool{}
	for _, provider := range geocheck.Providers() {
		if len(providerHosts[provider]) == 0 {
			t.Errorf("%s has no hosts", provider)
		}
		for _, host := range providerHosts[provider] {
			if !hostnamePattern.MatchString(host) {
				t.Errorf("%q is not a lowercase hostname", host)
			}
			if seen[host] {
				t.Errorf("%q is in the lists 2 times", host)
			}
			seen[host] = true
		}
	}
	for _, mustBlock := range []string{"api.anthropic.com", "claude.ai", "platform.claude.com", "downloads.claude.ai", "api.openai.com", "chatgpt.com", "auth.openai.com"} {
		if !seen[mustBlock] {
			t.Errorf("the lists must have %q", mustBlock)
		}
	}
	if got := getBlockedHosts([]geocheck.Provider{geocheck.OpenAI}); !slices.Equal(got, providerHosts[geocheck.OpenAI]) {
		t.Errorf("getBlockedHosts(openai) = %v", got)
	}
	if got := getBlockedHosts(nil); len(got) != 0 {
		t.Errorf("no provider must give no host: %v", got)
	}
}

func TestReplaceHostsBlock(t *testing.T) {
	blocked, err := replaceHostsBlock(sampleHosts, []string{"api.anthropic.com"})
	if err != nil {
		t.Fatal(err)
	}
	wantBlock := hostsBeginMarker + ". The clawdiator service changes the lines up to the END line.\n0.0.0.0 api.anthropic.com\n:: api.anthropic.com\n" + hostsEndMarker + "\n"
	if blocked != sampleHosts+wantBlock {
		t.Errorf("the block must come at the end, after the other lines:\n%s", blocked)
	}
	again, err := replaceHostsBlock(blocked, []string{"api.anthropic.com"})
	if err != nil || again != blocked {
		t.Errorf("the same hosts must give the same content: %v\n%s", err, again)
	}
	changed, err := replaceHostsBlock(blocked, []string{"api.openai.com"})
	if err != nil || strings.Contains(changed, "anthropic") || strings.Count(changed, hostsBeginMarker) != 1 {
		t.Errorf("a new block must replace the old block: %v\n%s", err, changed)
	}
	removed, err := replaceHostsBlock(changed, nil)
	if err != nil || removed != sampleHosts {
		t.Errorf("the removal must give the original content: %v\n%q", err, removed)
	}
}

func TestReplaceHostsBlockKeepsLineEnds(t *testing.T) {
	windows := strings.ReplaceAll(sampleHosts, "\n", "\r\n")
	blocked, err := replaceHostsBlock(windows, []string{"chatgpt.com"})
	if err != nil || strings.Contains(strings.ReplaceAll(blocked, "\r\n", ""), "\n") {
		t.Errorf("a file with CRLF must get CRLF lines: %v %q", err, blocked)
	}
	if removed, _ := replaceHostsBlock(blocked, nil); removed != windows {
		t.Errorf("the removal must give the original CRLF content: %q", removed)
	}
}

// The uninstallation must give the original file, also without a line end at its end.
func TestReplaceHostsBlockRestoresMissingFinalLineEnd(t *testing.T) {
	for _, original := range []string{strings.TrimSuffix(sampleHosts, "\n"), strings.TrimSuffix(strings.ReplaceAll(sampleHosts, "\n", "\r\n"), "\r\n"), ""} {
		blocked, err := replaceHostsBlock(original, []string{"chatgpt.com"})
		if err != nil {
			t.Fatal(err)
		}
		if original != "" && !strings.Contains(blocked, "localhost\n"+hostsBeginMarker) && !strings.Contains(blocked, "localhost\r\n"+hostsBeginMarker) {
			t.Errorf("the block must start on a new line: %q", blocked)
		}
		again, _ := replaceHostsBlock(blocked, []string{"chatgpt.com", "api.openai.com"})
		if removed, _ := replaceHostsBlock(again, nil); removed != original {
			t.Errorf("the removal must give the original content %q, got %q", original, removed)
		}
	}
}

func TestReplaceHostsBlockRefusesDamagedBlock(t *testing.T) {
	for _, damaged := range []string{
		sampleHosts + hostsBeginMarker + "\n0.0.0.0 api.anthropic.com\n",
		sampleHosts + hostsEndMarker + "\n",
		sampleHosts + hostsBeginMarker + "\n" + hostsBeginMarker + "\n" + hostsEndMarker + "\n",
	} {
		if _, err := replaceHostsBlock(damaged, nil); err == nil {
			t.Errorf("a damaged block must give an error:\n%s", damaged)
		}
	}
}

func TestWriteHostsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "private-hosts")
	if err := os.WriteFile(target, []byte(sampleHosts), 0o644); err != nil {
		t.Fatal(err)
	}
	path := target
	if runtime.GOOS != "windows" {
		// The same as /etc/hosts on macOS: a link to the real file.
		path = filepath.Join(dir, "hosts")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	isChanged, err := writeHostsFile(path, []string{"api.anthropic.com"})
	if err != nil || !isChanged {
		t.Fatalf("the first write must change the file: %v, %v", isChanged, err)
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "0.0.0.0 api.anthropic.com") {
		t.Errorf("the target of the link must get the block:\n%s", data)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link must stay a link: %v", err)
		}
		if info, _ := os.Stat(target); info.Mode().Perm() != 0o644 {
			t.Errorf("the mode must stay 0644: %v", info.Mode())
		}
	}
	if isChanged, err := writeHostsFile(path, []string{"api.anthropic.com"}); err != nil || isChanged {
		t.Errorf("the same block must not write the file again: %v, %v", isChanged, err)
	}
	if _, err := writeHostsFile(path, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != sampleHosts {
		t.Errorf("the removal must give the original file:\n%s", data)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".hosts.clawdiator-*")); len(leftovers) != 0 {
		t.Errorf("no temporary file must stay: %v", leftovers)
	}
}

func TestDNSFlushCommands(t *testing.T) {
	if got := getDNSFlushCommands("darwin"); len(got) != 2 || got[1][2] != "mDNSResponder" {
		t.Errorf("macOS = %v", got)
	}
	if got := getDNSFlushCommands("windows"); len(got) != 1 || strings.Join(got[0], " ") != "ipconfig /flushdns" {
		t.Errorf("Windows = %v", got)
	}
	if got := getDNSFlushCommands("linux"); len(got) != 2 || got[0][0] != "resolvectl" {
		t.Errorf("Linux = %v", got)
	}
}

func TestCommandHostsDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte(sampleHosts), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := commandHosts(path, []string{"--dry-run", "--providers", "anthropic"}, &stdout, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "0.0.0.0 api.anthropic.com") || strings.Contains(stdout.String(), "api.openai.com") {
		t.Errorf("the dry run must show the Anthropic block only:\n%s", stdout.String())
	}
	if data, _ := os.ReadFile(path); string(data) != sampleHosts {
		t.Error("the dry run must not change the file")
	}
	stdout.Reset()
	if err := commandHosts(path, []string{"--dry-run", "--providers", ""}, &stdout, nil); err != nil || !strings.Contains(stdout.String(), "already has this content") {
		t.Errorf("no providers and no block must give no change: %v\n%s", err, stdout.String())
	}
	if err := commandHosts(path, []string{"--providers", "anthropic"}, &stdout, nil); err == nil {
		t.Error("without --dry-run, the command must fail")
	}
	if err := commandHosts(path, []string{"--dry-run", "--providers", "google"}, &stdout, nil); err == nil {
		t.Error("an unknown provider must fail")
	}
	isRemoveCalled := false
	removeBlock := func() error { isRemoveCalled = true; return nil }
	if err := commandHosts(path, []string{"--remove"}, &stdout, removeBlock); err != nil || !isRemoveCalled {
		t.Errorf("--remove must call the removal of the block: %v, %v", isRemoveCalled, err)
	}
}
