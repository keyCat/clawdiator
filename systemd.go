package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

const (
	// systemdUnit is the system unit of the service.
	systemdUnit = appName + ".service"
	// agentUnit and trayUnit are the global user units of the notice agent and of the tray. They run in the session of each user.
	agentUnit = appName + "-agent.service"
	trayUnit  = appName + "-tray.service"
	// updateUnit is the transient system unit of the automatic update. The install in it stops the service,
	// so the service cannot run the install as its own child process.
	updateUnit = appName + "-update.service"
)

// systemdManager uses a systemd system unit for the service. The service runs as root, also without a login.
// The agents are global user units that start with the graphical session of each user.
type systemdManager struct{}

func (systemdManager) Install(paths appPaths) error {
	if err := writeRootFile(paths.ServiceFile, []byte(renderSystemdService(paths)), 0o644); err != nil {
		return err
	}
	// restart stops a running service first and starts the new binary.
	for _, args := range [][]string{{"daemon-reload"}, {"enable", systemdUnit}, {"restart", systemdUnit}} {
		if _, err := runCommand("systemctl", args...); err != nil {
			return err
		}
	}
	return nil
}

func (systemdManager) Uninstall(paths appPaths) error {
	// systemctl stops the unit before it returns. The service does not always exist, so ignore the errors.
	_, _ = runCommand("systemctl", "disable", "--now", systemdUnit)
	_, _ = runCommand("systemctl", "stop", updateUnit)
	if err := os.Remove(paths.ServiceFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = runCommand("systemctl", "daemon-reload")
	return nil
}

// errNoGraphicalSession tells install that the agents start only at the next login.
var errNoGraphicalSession = errors.New("no graphical session is active")

func (systemdManager) InstallAgents(paths appPaths, hasTray bool, userID int) error {
	agent := renderSystemdUserUnit("clawdiator notices", []string{paths.Binary, "agent"}, "always")
	if err := writeRootFile(paths.AgentFile, []byte(agent), 0o644); err != nil {
		return err
	}
	units := []string{agentUnit}
	if hasTray {
		// The tray stops with exit code 0 for "Hide the icon", so it starts again only after a crash.
		tray := renderSystemdUserUnit("clawdiator tray icon", []string{paths.TrayBinary, "tray"}, "on-failure")
		if err := writeRootFile(paths.TrayFile, []byte(tray), 0o644); err != nil {
			return err
		}
		units = append(units, trayUnit)
	} else {
		_, _ = runCommand("systemctl", "--global", "disable", trayUnit)
		if err := os.Remove(paths.TrayFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := runCommand("systemctl", append([]string{"--global", "enable"}, units...)...); err != nil {
		return err
	}
	machine, isSession := getUserMachine(userID)
	if !isSession {
		return errNoGraphicalSession
	}
	_, _ = runCommand("systemctl", "--user", machine, "daemon-reload")
	// Outside a graphical session, for example over SSH, the tray has no place for the icon. The units start at the next login.
	if _, err := runCommand("systemctl", "--user", machine, "is-active", "--quiet", "graphical-session.target"); err != nil {
		return errNoGraphicalSession
	}
	_, err := runCommand("systemctl", append([]string{"--user", machine, "restart"}, units...)...)
	return err
}

func (systemdManager) UninstallAgents(paths appPaths, userID int) error {
	if machine, isSession := getUserMachine(userID); isSession {
		_, _ = runCommand("systemctl", "--user", machine, "stop", trayUnit, agentUnit)
	}
	_, _ = runCommand("systemctl", "--global", "disable", trayUnit, agentUnit)
	var errs []error
	for _, file := range []string{paths.TrayFile, paths.AgentFile} {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// getUserMachine returns the option of systemctl for the user manager of the user ID. A user ID of 0 or less has no session.
func getUserMachine(userID int) (string, bool) {
	if userID <= 0 {
		return "", false
	}
	account, err := user.LookupId(strconv.Itoa(userID))
	if err != nil {
		return "", false
	}
	return "--machine=" + account.Username + "@", true
}

// renderSystemdService returns the system unit. Restart=always starts the service again after a stop or a crash.
func renderSystemdService(paths appPaths) string {
	return fmt.Sprintf(`[Unit]
Description=clawdiator: blocks Anthropic and OpenAI in an unsupported country

[Service]
ExecStart=%s service
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`, quoteSystemd(paths.Binary))
}

// renderSystemdUserUnit returns a user unit that runs with the graphical session. The desktop starts the target
// after the login and stops it at the logout. restart is a value of Restart=, for example "always" or "on-failure".
func renderSystemdUserUnit(description string, args []string, restart string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = quoteSystemd(arg)
	}
	return fmt.Sprintf(`[Unit]
Description=%s
PartOf=graphical-session.target
After=graphical-session.target

[Service]
ExecStart=%s
Restart=%s
RestartSec=10

[Install]
WantedBy=graphical-session.target
`, description, strings.Join(quoted, " "), restart)
}

// quoteSystemd returns 1 word of ExecStart. systemd reads % as a specifier and $ as a variable, so they get a second sign.
func quoteSystemd(word string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(word) + `"`
}
