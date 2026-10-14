package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const launchdLabel = "local.clawdiator"

// serviceManager registers the service in the service manager of the system.
type serviceManager interface {
	// Install writes the definition of the service and starts the service. A running service stops first.
	Install(paths appPaths) error
	// Uninstall stops the service and removes its definition.
	Uninstall(paths appPaths) error
}

// launchdManager uses a LaunchDaemon in the system domain. The service runs as root, also without a login.
type launchdManager struct{}

func (launchdManager) Install(paths appPaths) error {
	if err := writeRootFile(paths.Plist, []byte(renderLaunchdPlist(paths)), 0o644); err != nil {
		return err
	}
	// Stop the old service first. The service does not always exist, so ignore the error.
	_, _ = runCommand("launchctl", "bootout", "system/"+launchdLabel)
	var err error
	// launchctl removes the old service asynchronously, so try again for some seconds.
	for attempt := 0; attempt < 10; attempt++ {
		if _, err = runCommand("launchctl", "bootstrap", "system", paths.Plist); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

func (launchdManager) Uninstall(paths appPaths) error {
	_, _ = runCommand("launchctl", "bootout", "system/"+launchdLabel)
	if err := os.Remove(paths.Plist); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// renderLaunchdPlist returns the LaunchDaemon. KeepAlive starts the service again after a stop or a crash.
func renderLaunchdPlist(paths appPaths) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>service</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>/dev/null</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabel, escapeXML(paths.Binary), escapeXML(filepath.Join(paths.Dir, "service-errors.log")))
}

func escapeXML(text string) string {
	var builder strings.Builder
	_ = xml.EscapeText(&builder, []byte(text))
	return builder.String()
}

// writeRootFile writes a file through a temporary file and a rename.
// A rename gives a new file. On Apple Silicon, a change in place of a signed binary makes the kernel stop it at the next start.
func writeRootFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		os.Remove(temporaryPath)
		return err
	}
	if err := chownToRoot(temporaryPath); err != nil {
		os.Remove(temporaryPath)
		return err
	}
	return os.Rename(temporaryPath, path)
}

// chownToRoot gives the file to root. Only root can do it, so the function does nothing for other users.
// The tests run as a normal user. The install command checks before that it runs as root.
func chownToRoot(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Lchown(path, 0, 0)
}

// runCommand runs a command and returns its output. The error has the output.
func runCommand(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	text := string(bytes.TrimSpace(output))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, text)
		}
		return text, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return text, nil
}
