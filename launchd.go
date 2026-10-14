package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	launchdLabel = "local.clawdiator"
	// agentLabel is the LaunchAgent of the notice agent. It runs in the session of each user.
	agentLabel = "local.clawdiator.agent"
	// trayLabel is the LaunchAgent of the tray. It runs in the session of each user.
	trayLabel = "local.clawdiator.tray"
)

// serviceManager registers the service in the service manager of the system.
type serviceManager interface {
	// Install writes the definition of the service and starts the service. A running service stops first.
	Install(paths appPaths) error
	// Uninstall stops the service and removes its definition.
	Uninstall(paths appPaths) error
	// InstallAgents writes the definitions of the notice agent and of the tray. It starts them in the session of the user ID.
	// Without the tray binary, it writes only the notice agent. A user ID of 0 or less starts nothing:
	// then the agents start at the next login.
	InstallAgents(paths appPaths, hasTray bool, userID int) error
	// UninstallAgents stops the agents in the session of the user ID and removes their definitions.
	UninstallAgents(paths appPaths, userID int) error
}

// launchdManager uses a LaunchDaemon in the system domain. The service runs as root, also without a login.
type launchdManager struct{}

func (launchdManager) Install(paths appPaths) error {
	if err := writeRootFile(paths.Plist, []byte(renderLaunchdPlist(paths)), 0o644); err != nil {
		return err
	}
	// Stop the old service first. The service does not always exist, so ignore the error.
	_, _ = runCommand("launchctl", "bootout", "system/"+launchdLabel)
	return bootstrapLaunchd("system", paths.Plist)
}

// bootstrapLaunchd loads the definition into the domain. launchctl removes an old job asynchronously,
// so the function tries again for some seconds.
func bootstrapLaunchd(domain, plist string) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if _, err = runCommand("launchctl", "bootstrap", domain, plist); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

func (launchdManager) InstallAgents(paths appPaths, hasTray bool, userID int) error {
	if err := writeRootFile(paths.AgentPlist, []byte(renderAgentPlist(agentLabel, []string{paths.Binary, "agent"}, true)), 0o644); err != nil {
		return err
	}
	jobs := map[string]string{agentLabel: paths.AgentPlist}
	if hasTray {
		if err := writeRootFile(paths.TrayPlist, []byte(renderAgentPlist(trayLabel, []string{paths.TrayBinary, "tray"}, false)), 0o644); err != nil {
			return err
		}
		jobs[trayLabel] = paths.TrayPlist
	} else if err := os.Remove(paths.TrayPlist); err != nil && !os.IsNotExist(err) {
		return err
	}
	if userID <= 0 {
		return nil
	}
	domain := fmt.Sprintf("gui/%d", userID)
	var errs []error
	for label, plist := range jobs {
		_, _ = runCommand("launchctl", "bootout", domain+"/"+label)
		if err := bootstrapLaunchd(domain, plist); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (launchdManager) UninstallAgents(paths appPaths, userID int) error {
	if userID > 0 {
		for _, label := range []string{trayLabel, agentLabel} {
			_, _ = runCommand("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", userID, label))
		}
	}
	var errs []error
	for _, plist := range []string{paths.TrayPlist, paths.AgentPlist} {
		if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// renderAgentPlist returns a LaunchAgent for the session of a user at the screen (Aqua).
// With isAlwaysAlive, launchd starts the job again after each stop. Without it, launchd starts the job again only after a crash.
// So the tray can stop with exit code 0 for "Hide the icon", and it starts again at the next login.
func renderAgentPlist(label string, args []string, isAlwaysAlive bool) string {
	var arguments strings.Builder
	for _, arg := range args {
		arguments.WriteString("\t\t<string>" + escapeXML(arg) + "</string>\n")
	}
	keepAlive := "<true/>"
	if !isAlwaysAlive {
		keepAlive = "<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	%s
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>/dev/null</string>
	<key>StandardErrorPath</key>
	<string>/dev/null</string>
</dict>
</plist>
`, label, arguments.String(), keepAlive)
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
