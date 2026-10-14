package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// appPaths holds the locations of the files of clawdiator.
type appPaths struct {
	// Dir is the app folder. Only an administrator can change it, because the service runs as root.
	Dir    string
	Binary string
	Config string
	State  string
	Log    string
	// DebugLog is the verbose log. The service always writes it. "status --verbose" shows its end.
	DebugLog string
	// Lock stops a second copy of the service.
	Lock string
	// SocketDir holds the check socket. All users can open it.
	SocketDir   string
	CheckSocket string
	// ControlDir holds the control socket for pause and resume. Only root can open the folder.
	ControlDir    string
	ControlSocket string
	// Plist is the LaunchDaemon. Only macOS uses it.
	Plist string
	// TrayBinary is the binary with the tray. A root service must not link the Cocoa framework, so the tray has its own binary.
	TrayBinary string
	// AgentPlist and TrayPlist are the LaunchAgents of the notice agent and of the tray. Only macOS uses them.
	AgentPlist string
	TrayPlist  string
	// Link is the link for the CLI in a folder of PATH.
	Link  string
	Hosts string
}

// trayBinaryName is the name of the binary with the tray. The release puts it next to the main binary.
const trayBinaryName = appName + "-tray"

// getAppPaths returns the locations on this computer.
func getAppPaths() appPaths {
	return newAppPaths("")
}

// newAppPaths returns the locations under the root folder. The real system uses an empty root. The tests use a temporary root.
func newAppPaths(root string) appPaths {
	var paths appPaths
	switch runtime.GOOS {
	case "windows":
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		programFiles := os.Getenv("ProgramFiles")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		paths.Dir = filepath.Join(root, programData, appName)
		paths.Binary = filepath.Join(root, programFiles, appName, appName+".exe")
		paths.SocketDir = paths.Dir
		paths.ControlDir = filepath.Join(paths.Dir, "private")
		paths.Hosts = filepath.Join(root, getHostsPath())
	case "linux":
		paths.Dir = filepath.Join(root, "/var/lib", appName)
		paths.Binary = filepath.Join(root, "/usr/local/lib", appName, appName)
		paths.SocketDir = filepath.Join(root, "/run", appName)
		paths.ControlDir = filepath.Join(paths.SocketDir, "private")
		paths.Link = filepath.Join(root, "/usr/local/bin", appName)
		paths.Hosts = filepath.Join(root, "/etc/hosts")
	default:
		paths.Dir = filepath.Join(root, "/Library/Application Support", appName)
		paths.Binary = filepath.Join(paths.Dir, "bin", appName)
		paths.SocketDir = filepath.Join(root, "/var/run", appName)
		paths.ControlDir = filepath.Join(paths.SocketDir, "private")
		paths.Plist = filepath.Join(root, "/Library/LaunchDaemons", launchdLabel+".plist")
		paths.TrayBinary = filepath.Join(paths.Dir, "bin", trayBinaryName)
		paths.AgentPlist = filepath.Join(root, "/Library/LaunchAgents", agentLabel+".plist")
		paths.TrayPlist = filepath.Join(root, "/Library/LaunchAgents", trayLabel+".plist")
		paths.Link = filepath.Join(root, "/usr/local/bin", appName)
		paths.Hosts = filepath.Join(root, "/etc/hosts")
	}
	paths.Config = filepath.Join(paths.Dir, "config.json")
	paths.State = filepath.Join(paths.Dir, "state.json")
	paths.Log = filepath.Join(paths.Dir, appName+".log")
	paths.DebugLog = filepath.Join(paths.Dir, appName+"-debug.log")
	paths.Lock = filepath.Join(paths.Dir, "service.lock")
	paths.CheckSocket = filepath.Join(paths.SocketDir, "check.sock")
	paths.ControlSocket = filepath.Join(paths.ControlDir, "control.sock")
	return paths
}
