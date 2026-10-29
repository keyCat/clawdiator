package main

import (
	"os/exec"
	"strings"
)

// showNotice shows a notice in the Notification Center. The notice comes from Script Editor,
// because osascript shows it. A test on macOS 26.7.1 showed the notice.
func showNotice(message string) error {
	script := "display notification " + quoteAppleScript(message) + " with title " + quoteAppleScript(appName)
	return exec.Command("osascript", "-e", script).Run()
}

// runAsAdministrator runs the command as root after the password window of macOS.
// SUDO_USER gives the name of the user to the command, so that the log shows who started a pause.
func runAsAdministrator(user string, command ...string) error {
	var quoted []string
	for _, part := range command {
		quoted = append(quoted, quoteShell(part))
	}
	shell := "SUDO_USER=" + quoteShell(user) + " " + strings.Join(quoted, " ")
	script := "do shell script " + quoteAppleScript(shell) + " with administrator privileges"
	return exec.Command("osascript", "-e", script).Run()
}

// openTerminal opens a Terminal window that runs the command.
func openTerminal(command string) error {
	script := "tell application \"Terminal\"\nactivate\ndo script " + quoteAppleScript(command) + "\nend tell"
	return exec.Command("osascript", "-e", script).Run()
}

// openLogFile opens the log in the Console app.
func openLogFile(path string) error {
	return exec.Command("open", "-a", "Console", path).Run()
}

// openURL opens the address in the default browser.
func openURL(url string) error {
	return exec.Command("open", url).Run()
}
