//go:build !tray

package main

// trayIsBuilt tells that this binary has no tray. The tray links the Cocoa framework on macOS,
// and a root service must not link it. So the tray is in the separate binary clawdiator-tray.
const trayIsBuilt = false

// commandTray tells that this binary has no tray.
func commandTray(appPaths) error {
	return errorf(msgTrayNotBuilt)
}
