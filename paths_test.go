package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestAppPathsLayout checks the folders of the Filesystem Hierarchy Standard on Linux.
// macOS keeps the config and the logs in the app folder, the same as before.
func TestAppPathsLayout(t *testing.T) {
	linux := newAppPathsFor("linux", "/r")
	for _, c := range []struct{ got, want string }{
		{linux.Config, "/r/etc/clawdiator/config.json"},
		{linux.State, "/r/var/lib/clawdiator/state.json"},
		{linux.Log, "/r/var/log/clawdiator/clawdiator.log"},
		{linux.DebugLog, "/r/var/log/clawdiator/clawdiator-debug.log"},
		{linux.UpdateLog, "/r/var/log/clawdiator/update.log"},
		{linux.UsersDir, "/r/home"},
		{getUserAppDirName("linux"), ".config/clawdiator"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if folders := getAppFolders(linux); !slices.Equal(folders, []string{"/r/var/lib/clawdiator", "/r/etc/clawdiator", "/r/var/log/clawdiator"}) {
		t.Errorf("install and uninstall must handle the 3 folders of Linux: %v", folders)
	}

	mac := newAppPathsFor("darwin", "/r")
	if mac.Config != filepath.Join(mac.Dir, "config.json") || mac.UpdateLog != filepath.Join(mac.Dir, "update.log") {
		t.Errorf("macOS must keep the config and the logs in the app folder: %q, %q", mac.Config, mac.UpdateLog)
	}
	if folders := getAppFolders(mac); !slices.Equal(folders, []string{mac.Dir}) {
		t.Errorf("macOS has only the app folder: %v", folders)
	}
}
