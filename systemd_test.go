package main

import (
	"strings"
	"testing"
)

// TestRenderSystemdUnits checks the units of Linux. A path with a space, % and $ must stay 1 word of ExecStart,
// because systemd reads % as a specifier and $ as a variable. The tray starts again only after a crash.
func TestRenderSystemdUnits(t *testing.T) {
	paths := newAppPathsFor("linux", "/opt/A & B %h $HOME")
	service := renderSystemdService(paths)
	for _, want := range []string{
		`ExecStart="/opt/A & B %%h $$HOME/usr/local/lib/clawdiator/clawdiator" service`,
		"Restart=always",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("the service unit has no %q:\n%s", want, service)
		}
	}
	tray := renderSystemdUserUnit("clawdiator tray icon", []string{paths.TrayBinary, "tray"}, "on-failure")
	for _, want := range []string{
		`ExecStart="/opt/A & B %%h $$HOME/usr/local/lib/clawdiator/clawdiator-tray" "tray"`,
		"Restart=on-failure",
		"PartOf=graphical-session.target",
		"WantedBy=graphical-session.target",
	} {
		if !strings.Contains(tray, want) {
			t.Errorf("the tray unit has no %q:\n%s", want, tray)
		}
	}
	if quoted := quoteSystemd(`a"b\c`); quoted != `"a\"b\\c"` {
		t.Errorf("quoteSystemd = %s", quoted)
	}

	for _, c := range []struct{ got, want string }{
		{paths.ServiceFile, "/opt/A & B %h $HOME/etc/systemd/system/clawdiator.service"},
		{paths.AgentFile, "/opt/A & B %h $HOME/etc/systemd/user/clawdiator-agent.service"},
		{paths.TrayFile, "/opt/A & B %h $HOME/etc/systemd/user/clawdiator-tray.service"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
