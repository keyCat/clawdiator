package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// appPaths holds the locations of the files of clawdiator.
type appPaths struct {
	// Dir is the app folder with the state, the local database and the downloads of the updates.
	// Only an administrator can change it, because the service runs as root.
	Dir string
	// ConfigDir holds config.json, and LogDir holds the logs. On macOS and Windows, both are the app folder.
	// Linux uses the folders of the Filesystem Hierarchy Standard: /etc for the config and /var/log for the logs.
	ConfigDir string
	LogDir    string
	Binary    string
	Config    string
	State     string
	Log       string
	// DebugLog is the verbose log. The service always writes it. "status --verbose" shows its end.
	DebugLog string
	// UpdateLog has the output of the automatic update and the steps of the manual update.
	UpdateLog string
	// Lock stops a second copy of the service.
	Lock string
	// SocketDir holds the check socket. All users can open it.
	SocketDir   string
	CheckSocket string
	// ControlDir holds the control socket for pause and resume. Only root can open the folder.
	ControlDir    string
	ControlSocket string
	// ServiceFile is the definition of the service: a LaunchDaemon on macOS, a systemd system unit on Linux.
	ServiceFile string
	// TrayBinary is the binary with the tray. A root service must not link the Cocoa framework, so the tray has its own binary.
	TrayBinary string
	// AgentFile and TrayFile are the definitions of the notice agent and of the tray in the session of each user:
	// LaunchAgents on macOS, global systemd user units on Linux.
	AgentFile string
	TrayFile  string
	// Link is the link for the CLI in a folder of PATH.
	Link  string
	Hosts string
	// UsersDir holds the home folders of the users. uninstall removes the folder of clawdiator in each home folder.
	UsersDir string
}

// trayBinaryName is the name of the binary with the tray. The release puts it next to the main binary.
const trayBinaryName = appName + "-tray"

// getAppPaths returns the locations on this computer.
func getAppPaths() appPaths {
	return newAppPaths("")
}

// newAppPaths returns the locations under the root folder. The real system uses an empty root. The tests use a temporary root.
func newAppPaths(root string) appPaths {
	return newAppPathsFor(runtime.GOOS, root)
}

// newAppPathsFor returns the locations of the system goos. The tests use it to check the locations of other systems.
func newAppPathsFor(goos, root string) appPaths {
	var paths appPaths
	switch goos {
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
		paths.ConfigDir = filepath.Join(root, "/etc", appName)
		paths.LogDir = filepath.Join(root, "/var/log", appName)
		paths.Binary = filepath.Join(root, "/usr/local/lib", appName, appName)
		paths.TrayBinary = filepath.Join(root, "/usr/local/lib", appName, trayBinaryName)
		paths.SocketDir = filepath.Join(root, "/run", appName)
		paths.ControlDir = filepath.Join(paths.SocketDir, "private")
		paths.ServiceFile = filepath.Join(root, "/etc/systemd/system", systemdUnit)
		paths.AgentFile = filepath.Join(root, "/etc/systemd/user", agentUnit)
		paths.TrayFile = filepath.Join(root, "/etc/systemd/user", trayUnit)
		paths.Link = filepath.Join(root, "/usr/local/bin", appName)
		paths.Hosts = filepath.Join(root, "/etc/hosts")
		paths.UsersDir = filepath.Join(root, "/home")
	default:
		paths.Dir = filepath.Join(root, "/Library/Application Support", appName)
		paths.Binary = filepath.Join(paths.Dir, "bin", appName)
		paths.SocketDir = filepath.Join(root, "/var/run", appName)
		paths.ControlDir = filepath.Join(paths.SocketDir, "private")
		paths.ServiceFile = filepath.Join(root, "/Library/LaunchDaemons", launchdLabel+".plist")
		paths.TrayBinary = filepath.Join(paths.Dir, "bin", trayBinaryName)
		paths.AgentFile = filepath.Join(root, "/Library/LaunchAgents", agentLabel+".plist")
		paths.TrayFile = filepath.Join(root, "/Library/LaunchAgents", trayLabel+".plist")
		paths.Link = filepath.Join(root, "/usr/local/bin", appName)
		paths.Hosts = filepath.Join(root, "/etc/hosts")
		paths.UsersDir = filepath.Join(root, "/Users")
	}
	if paths.ConfigDir == "" {
		paths.ConfigDir = paths.Dir
	}
	if paths.LogDir == "" {
		paths.LogDir = paths.Dir
	}
	paths.Config = filepath.Join(paths.ConfigDir, "config.json")
	paths.State = filepath.Join(paths.Dir, "state.json")
	paths.Log = filepath.Join(paths.LogDir, appName+".log")
	paths.DebugLog = filepath.Join(paths.LogDir, appName+"-debug.log")
	paths.UpdateLog = filepath.Join(paths.LogDir, "update.log")
	paths.Lock = filepath.Join(paths.Dir, "service.lock")
	paths.CheckSocket = filepath.Join(paths.SocketDir, "check.sock")
	paths.ControlSocket = filepath.Join(paths.ControlDir, "control.sock")
	return paths
}
