package main

import (
	"os"
	"strconv"
	"strings"
)

// getConsoleUserID returns the user ID of the active graphical session. sudo gives the user who started the command.
// The automatic update runs without sudo, so it asks logind. A negative value means that no graphical session is active.
func getConsoleUserID() int {
	if userID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && userID > 0 {
		return userID
	}
	sessions, err := runCommand("loginctl", "list-sessions", "--no-legend")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(sessions, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		properties, err := runCommand("loginctl", "show-session", fields[0], "--property=Type", "--property=Active", "--property=User")
		if err != nil {
			continue
		}
		if userID, isGraphical := parseGraphicalSession(properties); isGraphical {
			return userID
		}
	}
	return -1
}

// parseGraphicalSession reads the output of "loginctl show-session" with the properties Type, Active and User.
// It returns the user of an active session of X11 or Wayland.
func parseGraphicalSession(properties string) (int, bool) {
	values := map[string]string{}
	for _, line := range strings.Split(properties, "\n") {
		if name, value, isFound := strings.Cut(strings.TrimSpace(line), "="); isFound {
			values[name] = value
		}
	}
	userID, err := strconv.Atoi(values["User"])
	isGraphical := values["Type"] == "x11" || values["Type"] == "wayland"
	return userID, err == nil && userID > 0 && isGraphical && values["Active"] == "yes"
}
