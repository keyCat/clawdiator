package main

import (
	"strings"
	"testing"
)

// TestBuildTerminalScript checks that a closed Terminal runs the command in its first window, so only 1 window opens.
// An open Terminal gets a new window for the command.
func TestBuildTerminalScript(t *testing.T) {
	script := buildTerminalScript(`sudo '/Library/Application Support/clawdiator/bin/clawdiator' update`)
	for _, want := range []string{
		`set wasRunning to application "Terminal" is running`,
		"if wasRunning then\n\t\tdo script \"sudo '/Library/Application Support/clawdiator/bin/clawdiator' update\"\n",
		`do script "sudo '/Library/Application Support/clawdiator/bin/clawdiator' update" in window 1`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the script has no %q:\n%s", want, script)
		}
	}
}
