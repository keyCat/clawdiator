package main

import (
	"encoding/xml"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestRenderLaunchdPlist(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the LaunchDaemon is only for macOS")
	}
	paths := newAppPaths("/Volumes/A & B")
	plist := renderLaunchdPlist(paths)
	for _, want := range []string{
		"<string>local.clawdiator</string>",
		"<string>/Volumes/A &amp; B/Library/Application Support/clawdiator/bin/clawdiator</string>",
		"<string>service</string>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>RunAtLoad</key>\n\t<true/>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("the plist must contain %q:\n%s", want, plist)
		}
	}
	var document struct{}
	if err := xml.Unmarshal([]byte(plist), &document); err != nil {
		t.Errorf("the plist must be correct XML: %v", err)
	}
}

func TestRenderAgentPlist(t *testing.T) {
	agent := renderAgentPlist(agentLabel, []string{"/Library/Application Support/clawdiator/bin/clawdiator", "agent"}, true)
	tray := renderAgentPlist(trayLabel, []string{"/Library/Application Support/clawdiator/bin/clawdiator-tray", "tray"}, false)
	for _, plist := range []string{agent, tray} {
		var document struct{}
		if err := xml.Unmarshal([]byte(plist), &document); err != nil {
			t.Errorf("the plist must be correct XML: %v\n%s", err, plist)
		}
		if !strings.Contains(plist, "<key>LimitLoadToSessionType</key>\n\t<string>Aqua</string>") {
			t.Errorf("an agent must run only in the session of a user at the screen:\n%s", plist)
		}
	}
	if !strings.Contains(agent, "<key>KeepAlive</key>\n\t<true/>") || !strings.Contains(agent, "<string>agent</string>") {
		t.Errorf("launchd must start the notice agent again after each stop:\n%s", agent)
	}
	if !strings.Contains(tray, "<key>SuccessfulExit</key>\n\t\t<false/>") || !strings.Contains(tray, "<string>tray</string>") {
		t.Errorf("launchd must start the tray again only after a crash:\n%s", tray)
	}
}

// InstallAgents with no user session writes the files, but runs no launchctl. So the test can call it.
func TestLaunchdInstallAgentsWritesPlists(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the LaunchAgents are only for macOS")
	}
	paths := newAppPaths(t.TempDir())
	if err := (launchdManager{}).InstallAgents(paths, true, 0); err != nil {
		t.Fatal(err)
	}
	agent, _ := os.ReadFile(paths.AgentPlist)
	tray, _ := os.ReadFile(paths.TrayPlist)
	if !strings.Contains(string(agent), "<key>KeepAlive</key>\n\t<true/>") || !strings.Contains(string(agent), paths.Binary) {
		t.Errorf("the notice agent plist = %s", agent)
	}
	if !strings.Contains(string(tray), "<key>SuccessfulExit</key>\n\t\t<false/>") || !strings.Contains(string(tray), paths.TrayBinary) {
		t.Errorf("the tray plist = %s", tray)
	}
	if err := (launchdManager{}).InstallAgents(paths, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.TrayPlist); !os.IsNotExist(err) {
		t.Errorf("without the tray binary, the tray plist must go: %v", err)
	}
	if err := (launchdManager{}).UninstallAgents(paths, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.AgentPlist); !os.IsNotExist(err) {
		t.Errorf("the uninstall must remove the agent plist: %v", err)
	}
}
