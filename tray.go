//go:build tray

package main

import (
	"context"
	"path/filepath"
	"time"

	"clawdiator/internal/geocheck"
	"fyne.io/systray"
)

// trayIsBuilt tells that this binary has the tray. The release builds clawdiator-tray with the tray tag and cgo.
const trayIsBuilt = true

// pauseChoices are the durations of the "Pause" submenu. An empty value means a pause with no end.
var pauseChoices = []struct {
	label messageKey
	value string
}{
	{msgTrayPause30m, "30m"}, {msgTrayPause1h, "1h"}, {msgTrayPause4h, "4h"},
	{msgTrayPause1d, "1d"}, {msgTrayPause3d, "3d"}, {msgTrayPauseForever, ""},
}

// commandTray is the start point of the tray. A LaunchAgent starts it at each login, as the user.
// "Hide the icon" stops this process with exit code 0. The LaunchAgent then does not start it again until the next login.
// The notices continue, because the notice agent is a separate process.
func commandTray(paths appPaths) error {
	systray.Run(func() { startTray(paths) }, func() {})
	return nil
}

func startTray(paths appPaths) {
	userDir, _ := getUserAppDir()
	settingsPath := filepath.Join(userDir, "agent.json")

	systray.SetIcon(buildTrayIcon(trayGreen, false))
	countryItem := systray.AddMenuItem("", "")
	countryItem.Disable()
	var providerItems []*systray.MenuItem
	for range geocheck.Providers() {
		item := systray.AddMenuItem("", "")
		item.Disable()
		providerItems = append(providerItems, item)
	}
	lastCheckItem := systray.AddMenuItem("", "")
	lastCheckItem.Disable()
	systray.AddSeparator()
	checkItem := systray.AddMenuItem(text(msgTrayCheckNow), "")
	pauseItem := systray.AddMenuItem(text(msgTrayPause), "")
	for _, choice := range pauseChoices {
		item := pauseItem.AddSubMenuItem(text(choice.label), "")
		value := choice.value
		go onClick(item, func() { pauseFromTray(paths, value) })
	}
	resumeItem := systray.AddMenuItem(text(msgTrayResume), "")
	noticesItem := systray.AddMenuItem(text(msgTrayNotices), "")
	longItem := noticesItem.AddSubMenuItem(text(msgTrayNoticesLong), "")
	allItem := noticesItem.AddSubMenuItem(text(msgTrayNoticesAll), "")
	systray.AddSeparator()
	logItem := systray.AddMenuItem(text(msgTrayLog), "")
	showLogItem := logItem.AddSubMenuItem(text(msgTrayShowLog), "")
	showVerboseLogItem := logItem.AddSubMenuItem(text(msgTrayShowVerboseLog), "")
	hideItem := systray.AddMenuItem(text(msgTrayHide), "")
	systray.AddSeparator()
	updateItem := systray.AddMenuItem("", "")
	updateItem.Hide()
	websiteItem := systray.AddMenuItem(text(msgTrayWebsite), "")
	websiteItem.Hide()
	checkUpdateItem := systray.AddMenuItem(text(msgTrayCheckUpdate), "")
	autoUpdateItem := systray.AddMenuItem(text(msgTrayAutoUpdate), "")
	systray.AddMenuItem(appName+" "+version, "").Disable()

	go onClick(checkItem, func() {
		if _, err := sendCommand(paths.CheckSocket, commandRequest{Command: socketCheck}); err != nil {
			_ = showNotice(text(msgServiceNotReachable, err))
		}
	})
	go onClick(resumeItem, func() { _ = runAsAdministrator(getUserName(), paths.Binary, "resume") })
	setNotices := func(mode string) {
		settings, _ := loadAgentSettings(settingsPath)
		settings.PreBlockNotices = mode
		_ = saveAgentSettings(settingsPath, settings)
	}
	go onClick(longItem, func() { setNotices(preBlockNoticesLong) })
	go onClick(allItem, func() { setNotices(preBlockNoticesAll) })
	go onClick(showLogItem, func() { _ = openLogFile(paths.Log) })
	go onClick(showVerboseLogItem, func() { _ = openLogFile(paths.DebugLog) })
	// The update needs the password and can show a new consent text, so it runs in Terminal.
	go onClick(updateItem, func() { _ = openTerminal("sudo " + quoteShell(paths.Binary) + " update") })
	go onClick(websiteItem, func() { _ = openURL(websiteURL) })
	go onClick(autoUpdateItem, func() {
		value := "on"
		if cfg, err := loadConfig(paths.Config); err == nil && cfg.AutoUpdate {
			value = "off"
		}
		_ = runAsAdministrator(getUserName(), paths.Binary, "update", "--auto-install", value)
	})
	go onClick(checkUpdateItem, func() {
		result, err := checkForUpdate(paths)
		if err != nil {
			result = err.Error()
		}
		_ = showNotice(result)
	})
	go onClick(hideItem, systray.Quit)

	go func() {
		var watcher heartbeatWatcher
		lastColor, lastBadge := trayColor(-1), false
		// The watch changes the icon soon after each save of the service. The poll every 2 seconds is the backup.
		changes := watchFiles(context.Background(), paths.State)
		for {
			state := readServiceState(paths.State)
			heartbeat := time.Time{}
			if state != nil {
				heartbeat = state.Heartbeat
			}
			view := buildTrayView(state, watcher.isServiceDown(time.Now(), heartbeat))
			if view.Color != lastColor || view.HasBadge != lastBadge {
				systray.SetIcon(buildTrayIcon(view.Color, view.HasBadge))
				lastColor, lastBadge = view.Color, view.HasBadge
			}
			systray.SetTooltip(view.Tooltip)
			countryItem.SetTitle(view.Country)
			for index, item := range providerItems {
				if index < len(view.Providers) {
					item.SetTitle(view.Providers[index])
				}
			}
			lastCheckItem.SetTitle(view.LastCheck)
			if view.IsPaused {
				pauseItem.Hide()
				resumeItem.Show()
			} else {
				pauseItem.Show()
				resumeItem.Hide()
			}
			if view.Update != "" {
				updateItem.SetTitle(view.Update)
				updateItem.Show()
				websiteItem.Show()
			} else {
				updateItem.Hide()
				websiteItem.Hide()
			}
			cfg, err := loadConfig(paths.Config)
			setChecked(autoUpdateItem, err == nil && cfg.AutoUpdate)
			settings, _ := loadAgentSettings(settingsPath)
			setChecked(longItem, settings.PreBlockNotices == preBlockNoticesLong)
			setChecked(allItem, settings.PreBlockNotices == preBlockNoticesAll)
			select {
			case <-changes:
			case <-time.After(agentInterval):
			}
		}
	}()
}

// pauseFromTray starts a pause through the CLI as root. An empty value means a pause with no end.
func pauseFromTray(paths appPaths, value string) {
	args := []string{paths.Binary, "pause", "--forever"}
	if value != "" {
		args = []string{paths.Binary, "pause", "--for", value}
	}
	_ = runAsAdministrator(getUserName(), args...)
}

// onClick runs the action for each click on the item.
func onClick(item *systray.MenuItem, action func()) {
	for range item.ClickedCh {
		action()
	}
}

func setChecked(item *systray.MenuItem, isChecked bool) {
	if isChecked {
		item.Check()
	} else {
		item.Uncheck()
	}
}
