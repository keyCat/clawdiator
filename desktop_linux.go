package main

import (
	"errors"
	"os/exec"

	"github.com/godbus/dbus/v5"
)

// showNotice shows a notice through the D-Bus service org.freedesktop.Notifications of the desktop.
func showNotice(message string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	notifications := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	return notifications.Call("org.freedesktop.Notifications.Notify", 0,
		appName, uint32(0), "", appName, message, []string{}, map[string]dbus.Variant{}, int32(-1)).Err
}

// runAsAdministrator runs the command as root after the password window of polkit.
// pkexec clears the environment, so env gives SUDO_USER to the command. Then the log shows who started a pause.
func runAsAdministrator(user string, command ...string) error {
	return exec.Command("pkexec", append([]string{"/usr/bin/env", "SUDO_USER=" + user}, command...)...).Run()
}

// terminalPrograms are the terminal programs in the order of the search. Each one gets the command after its option.
// x-terminal-emulator is the default terminal of Debian and Ubuntu.
var terminalPrograms = [][]string{
	{"x-terminal-emulator", "-e"},
	{"gnome-terminal", "--"},
	{"konsole", "-e"},
	{"xfce4-terminal", "-x"},
	{"xterm", "-e"},
}

// openTerminal opens a terminal window that runs the command. The window closes when its command ends,
// so the command waits for Enter at the end. Then the user can read the output.
func openTerminal(command string) error {
	script := command + "; printf '\\n%s' " + quoteShell(text(msgPressEnterToClose)) + "; read _"
	for _, program := range terminalPrograms {
		path, err := exec.LookPath(program[0])
		if err != nil {
			continue
		}
		if startDetached(path, append(program[1:], "sh", "-c", script)...) == nil {
			return nil
		}
	}
	return errors.New("no terminal program: x-terminal-emulator, gnome-terminal, konsole, xfce4-terminal or xterm")
}

// openLogFile opens the log in the default program of the desktop for text files.
func openLogFile(path string) error {
	return startDetached("xdg-open", path)
}

// openURL opens the address in the default browser.
func openURL(url string) error {
	return startDetached("xdg-open", url)
}

// startDetached starts a program and does not wait for its end. Some programs of xdg-open stay open until the user closes them.
func startDetached(name string, args ...string) error {
	command := exec.Command(name, args...)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
