package main

import (
	"encoding/xml"
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
