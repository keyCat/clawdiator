package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// agentLogMaxSize is the size of agent.log that starts a new file.
const agentLogMaxSize = 1 << 20

// commandAgent is the start point of the notice agent. A LaunchAgent starts it at each login, as the user.
// It reads state.json every 2 seconds and shows the notices. With --show, it shows the tray icon again and stops.
func commandAgent(paths appPaths, args []string) error {
	flags := newFlagSet("agent")
	isShow := flags.Bool("show", false, text(msgFlagAgentShow))
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *isShow {
		_, err := runCommand("launchctl", "kickstart", "gui/"+strconv.Itoa(os.Getuid())+"/"+trayLabel)
		return err
	}
	userDir, err := getUserAppDir()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(userDir, "agent.json")
	logPath := filepath.Join(userDir, "agent.log")
	settings, isFirstRun := loadAgentSettings(settingsPath)
	model := newAgentModel(settings)
	logAgent(logPath, "start, process %d, first run: %v", os.Getpid(), isFirstRun)
	for {
		// The tray changes agent.json, so the agent reads it again at each pass.
		settings, _ = loadAgentSettings(settingsPath)
		model.settings = settings
		for _, next := range model.update(time.Now(), readServiceState(paths.State), isFirstRun) {
			err := showNotice(next.Text)
			logAgent(logPath, "notice %q, error: %v", next.Text, err)
		}
		if isFirstRun {
			isFirstRun = false
			if err := saveAgentSettings(settingsPath, settings); err != nil {
				logAgent(logPath, "cannot save %s: %v", settingsPath, err)
			}
		}
		time.Sleep(agentInterval)
	}
}

// logAgent writes 1 line to agent.log in the folder of the user.
func logAgent(path, format string, args ...any) {
	line := time.Now().Format(debugTimeFormat) + " " + cleanLogText(fmt.Sprintf(format, args...))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = appendLogLimit(path, []string{line}, agentLogMaxSize)
	}
}
