package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"geocheck"
)

const (
	// preBlockNoticesLong shows a notice only for a long pre-block, or for a pre-block that ends in a block. This is the default.
	preBlockNoticesLong = "long"
	// preBlockNoticesAll shows a notice for each pre-block and for its end.
	preBlockNoticesAll = "all"
	// defaultPreBlockNoticeSeconds is the default time after which a pre-block counts as long.
	defaultPreBlockNoticeSeconds = 20
	// heartbeatTimeout is the time without a change of the heartbeat after which the agent tells the user.
	heartbeatTimeout = 2 * time.Minute
	// agentInterval is the time between 2 reads of state.json by the agent and by the tray.
	agentInterval = 2 * time.Second
)

// agentSettings is the content of agent.json. Each user has a file. It needs no administrator rights.
type agentSettings struct {
	PreBlockNotices       string `json:"pre_block_notices"`
	PreBlockNoticeSeconds int    `json:"pre_block_notice_seconds"`
}

// withDefaults replaces the values that are missing or not correct with the default values.
func (s agentSettings) withDefaults() agentSettings {
	if s.PreBlockNotices != preBlockNoticesAll {
		s.PreBlockNotices = preBlockNoticesLong
	}
	if s.PreBlockNoticeSeconds < 5 || s.PreBlockNoticeSeconds > 300 {
		s.PreBlockNoticeSeconds = defaultPreBlockNoticeSeconds
	}
	return s
}

// getUserAppDir returns the folder of clawdiator in the home folder of the user. It holds agent.json and agent.log.
func getUserAppDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", appName), nil
	case "windows":
		if dir := os.Getenv("APPDATA"); dir != "" {
			return filepath.Join(dir, appName), nil
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName), nil
}

// loadAgentSettings reads agent.json. isFirstRun tells that the file does not exist yet:
// then the agent runs for the first time for this user.
func loadAgentSettings(path string) (settings agentSettings, isFirstRun bool) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return agentSettings{}.withDefaults(), true
	}
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	return settings.withDefaults(), false
}

// saveAgentSettings writes agent.json through a temporary file.
func saveAgentSettings(path string, settings agentSettings) error {
	data, err := json.MarshalIndent(settings.withDefaults(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// heartbeatWatcher tells if the service does not work. It measures the time on its own clock,
// from the time when it first saw the current value of the heartbeat. After a sleep of 8 hours, the heartbeat is 8 hours old,
// and the agent can read it before the service writes a new value. So the age of the value is not a correct signal.
type heartbeatWatcher struct {
	value  time.Time
	seenAt time.Time
}

// isServiceDown takes the heartbeat of the last read of state.json. A zero heartbeat means that the read failed.
func (w *heartbeatWatcher) isServiceDown(now, heartbeat time.Time) bool {
	if w.seenAt.IsZero() || !heartbeat.Equal(w.value) {
		w.value, w.seenAt = heartbeat, now
		return false
	}
	return now.Sub(w.seenAt) >= heartbeatTimeout
}

// notice is 1 notice for the user.
type notice struct {
	Text string
}

// agentModel decides which notices the agent shows. It reads no clock and shows nothing itself, so the tests can drive it.
type agentModel struct {
	settings  agentSettings
	isStarted bool
	lastSeq   int64
	// notifiedBlocked holds the providers that the user knows as blocked, from a block notice or from the login notice.
	// An unblock notice comes only for these providers.
	notifiedBlocked     map[geocheck.Provider]bool
	isPreBlockNotified  bool
	heartbeat           heartbeatWatcher
	isServiceDownWarned bool
}

func newAgentModel(settings agentSettings) *agentModel {
	return &agentModel{settings: settings.withDefaults(), notifiedBlocked: map[geocheck.Provider]bool{}}
}

// update takes the last read of state.json and returns the notices to show. A nil state means that the read failed.
// isFirstRun tells that the agent runs for the first time for this user. Then the first notice tells that clawdiator works.
func (m *agentModel) update(now time.Time, state *serviceState, isFirstRun bool) []notice {
	var notices []notice
	heartbeat := time.Time{}
	if state != nil {
		heartbeat = state.Heartbeat
	}
	if m.heartbeat.isServiceDown(now, heartbeat) {
		if !m.isServiceDownWarned {
			m.isServiceDownWarned = true
			notices = append(notices, notice{text(msgNoticeServiceDown)})
		}
	} else if m.isServiceDownWarned {
		m.isServiceDownWarned = false
		notices = append(notices, notice{text(msgNoticeServiceBack)})
	}
	if state == nil {
		return notices
	}
	if !m.isStarted {
		m.isStarted = true
		m.lastSeq = state.LastEventSeq
		return append(notices, m.getLoginNotices(state, isFirstRun)...)
	}
	if state.LastEventSeq < m.lastSeq {
		// A new installation starts the sequence again. Then all events in the file are new.
		m.lastSeq = 0
	}
	for _, change := range state.Events {
		if change.Seq <= m.lastSeq {
			continue
		}
		m.lastSeq = change.Seq
		notices = append(notices, m.getEventNotices(change)...)
	}
	// With the setting "long", a pre-block gets a notice only after some seconds. A short pre-block after a wake gets none.
	isLong := now.Sub(state.PreBlockSince) >= time.Duration(m.settings.PreBlockNoticeSeconds)*time.Second
	if state.IsPreBlocked && !state.Pause.IsOn && !m.isPreBlockNotified && m.settings.PreBlockNotices == preBlockNoticesLong && isLong {
		m.isPreBlockNotified = true
		notices = append(notices, notice{text(msgNoticePreBlock, joinProviderNames(geocheck.Providers()))})
	}
	return notices
}

// getLoginNotices returns the notice at the start of the agent: the first run, or a block or a pause that is on.
func (m *agentModel) getLoginNotices(state *serviceState, isFirstRun bool) []notice {
	var blocked []geocheck.Provider
	for _, provider := range geocheck.Providers() {
		if state.provider(provider).IsBlocked {
			blocked = append(blocked, provider)
			m.notifiedBlocked[provider] = true
		}
	}
	var notices []notice
	if isFirstRun {
		if state.Country != "" {
			notices = append(notices, notice{text(msgNoticeWelcome, state.Country)})
		} else {
			notices = append(notices, notice{text(msgNoticeWelcomeNoCountry)})
		}
	}
	switch {
	case state.Pause.IsOn:
		notices = append(notices, getPauseNotice(state.Pause.Until))
	case len(blocked) > 0:
		first := state.provider(blocked[0])
		notices = append(notices, getBlockNotice(blocked, first.Country, first.Verdict))
	}
	return notices
}

// getEventNotices returns the notices for 1 event of the service.
func (m *agentModel) getEventNotices(change event) []notice {
	switch change.Kind {
	case eventBlock:
		for _, provider := range change.Providers {
			m.notifiedBlocked[provider] = true
		}
		return []notice{getBlockNotice(change.Providers, change.Country, change.Verdict)}
	case eventUnblock:
		var available []geocheck.Provider
		for _, provider := range change.Providers {
			if m.notifiedBlocked[provider] {
				available = append(available, provider)
				delete(m.notifiedBlocked, provider)
			}
		}
		if len(available) > 0 {
			return []notice{getAvailableNotice(available, change.Country)}
		}
	case eventPreBlock:
		if m.settings.PreBlockNotices == preBlockNoticesAll {
			m.isPreBlockNotified = true
			return []notice{{text(msgNoticePreBlock, joinProviderNames(geocheck.Providers()))}}
		}
	case eventPreBlockEnd:
		if !m.isPreBlockNotified {
			return nil
		}
		m.isPreBlockNotified = false
		// A pre-block that ends in a block already has a block notice. Only the providers that are not blocked are available again.
		var available []geocheck.Provider
		for _, provider := range geocheck.Providers() {
			if !m.notifiedBlocked[provider] {
				available = append(available, provider)
			}
		}
		if len(available) > 0 {
			return []notice{getAvailableNotice(available, "")}
		}
	case eventPause:
		m.isPreBlockNotified = false
		return []notice{getPauseNotice(change.Until)}
	case eventPauseEnd:
		return []notice{{text(msgNoticePauseEnd)}}
	case eventPauseBlock:
		return []notice{{text(msgNoticePauseBlock, joinProviderNames(change.Providers), change.Country)}}
	case eventError:
		return []notice{{text(msgNoticeServiceError, change.Error)}}
	}
	return nil
}

func getBlockNotice(providers []geocheck.Provider, country, verdict string) notice {
	names := joinProviderNames(providers)
	isMany := len(providers) > 1
	if verdict == geocheck.LookupFailed.String() || country == "" {
		return notice{text(choose(isMany, msgNoticeBlockedUnknownMany, msgNoticeBlockedUnknownOne), names)}
	}
	return notice{text(choose(isMany, msgNoticeBlockedMany, msgNoticeBlockedOne), names, country)}
}

func getAvailableNotice(providers []geocheck.Provider, country string) notice {
	names := joinProviderNames(providers)
	isMany := len(providers) > 1
	if country == "" {
		return notice{text(choose(isMany, msgNoticeAvailableMany, msgNoticeAvailableOne), names)}
	}
	return notice{text(choose(isMany, msgNoticeAvailableCountryMany, msgNoticeAvailableCountryOne), names, country)}
}

func getPauseNotice(until time.Time) notice {
	if until.IsZero() {
		return notice{text(msgNoticePausedForever)}
	}
	return notice{text(msgNoticePausedUntil, until.Local().Format("15:04, 2006-01-02"))}
}

func choose(isMany bool, many, one messageKey) messageKey {
	if isMany {
		return many
	}
	return one
}

// joinProviderNames returns the names for a sentence, for example "Anthropic and OpenAI".
func joinProviderNames(providers []geocheck.Provider) string {
	var names []string
	for _, provider := range providers {
		names = append(names, provider.DisplayName())
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + text(msgAnd) + names[len(names)-1]
}

// trayColor is the state that the color of the tray icon shows.
type trayColor int

const (
	trayGreen trayColor = iota
	trayYellow
	trayRed
	trayGrey
	trayAlert
)

// trayView is what the tray icon and its menu show.
type trayView struct {
	Color     trayColor
	Tooltip   string
	Country   string
	Providers []string
	LastCheck string
	IsPaused  bool
}

// buildTrayView returns the view for the state. A nil state, or a service that does not work, gives the alert color.
func buildTrayView(state *serviceState, isServiceDown bool) trayView {
	if state == nil || isServiceDown {
		return trayView{Color: trayAlert, Tooltip: text(msgNoticeServiceDown), Country: text(msgTrayCountryUnknown)}
	}
	view := trayView{IsPaused: state.Pause.IsOn, Country: text(msgTrayCountry, state.Country)}
	if state.Country == "" {
		view.Country = text(msgTrayCountryUnknown)
	}
	blocked := state.blockedProviders()
	for _, provider := range geocheck.Providers() {
		word := text(msgStatusOpen)
		if slices.Contains(blocked, provider) {
			word = text(msgStatusBlocked)
		}
		view.Providers = append(view.Providers, provider.DisplayName()+": "+word)
	}
	if !state.LastFullCheck.IsZero() {
		view.LastCheck = text(msgTrayLastCheck, state.LastFullCheck.Local().Format("15:04"))
	}
	isVerdictBlocked := false
	for _, provider := range geocheck.Providers() {
		isVerdictBlocked = isVerdictBlocked || state.provider(provider).IsBlocked
	}
	switch {
	case state.Pause.IsOn:
		view.Color, view.Tooltip = trayGrey, text(msgTrayTooltipPaused)
	case isVerdictBlocked:
		view.Color, view.Tooltip = trayRed, text(msgTrayTooltipBlocked)
	case state.IsPreBlocked:
		view.Color, view.Tooltip = trayYellow, text(msgTrayTooltipPreBlocked)
	default:
		view.Color, view.Tooltip = trayGreen, text(msgTrayTooltipOpen)
	}
	return view
}

// readServiceState reads state.json for the agent. A missing or damaged file gives nil.
func readServiceState(path string) *serviceState {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	state := &serviceState{}
	if json.Unmarshal(data, state) != nil {
		return nil
	}
	return state
}
