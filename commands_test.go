package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeManager records the calls instead of launchctl.
type fakeManager struct {
	installs, uninstalls int
	uninstallErr         error
	agentInstalls        int
	agentUninstalls      int
	hasTray              bool
	agentUserID          int
	// onInstall runs at the registration of the service, so a test can see the files at the start of the service.
	onInstall func()
}

func (m *fakeManager) InstallAgents(_ appPaths, hasTray bool, userID int) error {
	m.agentInstalls++
	m.hasTray, m.agentUserID = hasTray, userID
	return nil
}

func (m *fakeManager) UninstallAgents(appPaths, int) error {
	m.agentUninstalls++
	return nil
}

func (m *fakeManager) Install(appPaths) error {
	m.installs++
	if m.onInstall != nil {
		m.onInstall()
	}
	return nil
}

func (m *fakeManager) Uninstall(appPaths) error {
	m.uninstalls++
	return m.uninstallErr
}

// newTestInstaller returns an installer under a temporary root, with a fake binary and a fake hosts file.
func newTestInstaller(t *testing.T, answer string) (*installer, *fakeManager, *bytes.Buffer) {
	if runtime.GOOS != "darwin" {
		t.Skip("this version installs only on macOS")
	}
	root := t.TempDir()
	paths := newAppPaths(root)
	source := filepath.Join(root, "build", "clawdiator")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Hosts), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Hosts, []byte(sampleHosts), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &fakeManager{}
	var stdout bytes.Buffer
	in := &installer{
		paths:         paths,
		manager:       manager,
		executable:    func() (string, error) { return source, nil },
		isRoot:        func() bool { return true },
		flushDNS:      func() {},
		consoleUserID: func() int { return 501 },
		// The tests never use the network. A test of the local database replaces this function.
		downloadDB: func(context.Context, string) (*countryDB, error) { return nil, errOffline },
		now:        func() time.Time { return testStart },
		user:       "ivan",
		stdin:      strings.NewReader(answer),
		stdout:     &stdout,
	}
	return in, manager, &stdout
}

func TestInstallAndUninstall(t *testing.T) {
	in, manager, stdout := newTestInstaller(t, "yes\n")
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Type \"yes\" to agree") || manager.installs != 1 {
		t.Errorf("the install must ask for the consent and register the service:\n%s", stdout.String())
	}
	if data, err := os.ReadFile(in.paths.Binary); err != nil || string(data) != "binary" {
		t.Errorf("the binary must be in the app folder: %q, %v", data, err)
	}
	if info, _ := os.Stat(in.paths.Binary); info.Mode().Perm() != 0o755 {
		t.Errorf("the binary mode = %v", info.Mode())
	}
	cfg, err := loadConfig(in.paths.Config)
	if err != nil || cfg.Consent.Version != consentVersion || cfg.Consent.User != "ivan" || !cfg.Consent.Time.Equal(testStart) {
		t.Errorf("the config must keep the consent: %+v, %v", cfg, err)
	}
	if target, err := os.Readlink(in.paths.Link); err != nil || target != in.paths.Binary {
		t.Errorf("the link = %q, %v", target, err)
	}

	// A second install changes 1 setting and does not ask for the consent again.
	in.stdin = strings.NewReader("")
	if err := in.commandInstall([]string{"--blocked-countries", "de, af"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(in.paths.Config); !slices.Equal(cfg.BlockedCountries, []string{"DE", "AF"}) || cfg.Consent.User != "ivan" {
		t.Errorf("the second install must keep the consent and change the countries: %+v", cfg)
	}
	if err := in.commandInstall([]string{"--blocked-countries", ""}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(in.paths.Config); len(cfg.BlockedCountries) != 0 {
		t.Errorf("an empty value must remove the countries: %v", cfg.BlockedCountries)
	}

	if _, err := writeHostsFile(in.paths.Hosts, []string{"api.anthropic.com"}); err != nil {
		t.Fatal(err)
	}
	if err := in.commandUninstall(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(in.paths.Hosts); string(data) != sampleHosts {
		t.Errorf("the uninstall must remove the block:\n%s", data)
	}
	for _, path := range []string{in.paths.Dir, in.paths.Link} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s must not exist after the uninstall: %v", path, err)
		}
	}
	if manager.uninstalls != 1 {
		t.Error("the uninstall must stop the service")
	}
}

func TestInstallRefusals(t *testing.T) {
	in, manager, _ := newTestInstaller(t, "no\n")
	if err := in.commandInstall(nil); err == nil || manager.installs != 0 {
		t.Errorf("without the consent, the install must stop: %v", err)
	}
	if _, err := os.Stat(in.paths.Dir); !os.IsNotExist(err) {
		t.Errorf("without the consent, the install must create no files: %v", err)
	}
	in.isRoot = func() bool { return false }
	if err := in.commandInstall([]string{"--yes"}); err == nil {
		t.Error("the install must need root")
	}
	in.isRoot = func() bool { return true }
	if err := in.commandInstall([]string{"--yes", "--geo-min-replies", "9"}); err == nil {
		t.Error("a wrong minimum must stop the install")
	}
	if err := in.commandInstall([]string{"--yes", "--blocked-countries", "Germany"}); err == nil {
		t.Error("a wrong country code must stop the install")
	}
}

// The uninstall must continue after a failed step, so that it also removes the block.
func TestUninstallContinuesAfterFailure(t *testing.T) {
	in, manager, stdout := newTestInstaller(t, "")
	manager.uninstallErr = errors.New("launchctl failed")
	if _, err := writeHostsFile(in.paths.Hosts, []string{"chatgpt.com"}); err != nil {
		t.Fatal(err)
	}
	if err := in.commandUninstall(); err == nil {
		t.Error("a failed step must give an error")
	}
	if data, _ := os.ReadFile(in.paths.Hosts); string(data) != sampleHosts {
		t.Errorf("the block must go also after a failed step:\n%s", data)
	}
	if !strings.Contains(stdout.String(), "FAILED  stop and remove the service") {
		t.Errorf("the output must show the failed step:\n%s", stdout.String())
	}
}

func TestRemoveBlockWithoutService(t *testing.T) {
	in, _, stdout := newTestInstaller(t, "")
	if _, err := writeHostsFile(in.paths.Hosts, []string{"chatgpt.com"}); err != nil {
		t.Fatal(err)
	}
	if err := in.commandRemoveBlock(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(in.paths.Hosts); string(data) != sampleHosts || !strings.Contains(stdout.String(), "The block is removed") {
		t.Errorf("the block must go:\n%s\n%s", data, stdout.String())
	}
	in.isRoot = func() bool { return false }
	if err := in.commandRemoveBlock(); err == nil {
		t.Error("the removal must need root")
	}
}

func TestCommandStatus(t *testing.T) {
	in, _, _ := newTestInstaller(t, "")
	c := newCore(&serviceState{}, nil)
	check := mustFindCheck(t, c.tick(testStart, "wifi"))
	c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("AF", "AF"), nil)
	if err := saveServiceState(in.paths.State, c.state); err != nil {
		t.Fatal(err)
	}
	if _, err := writeHostsFile(in.paths.Hosts, getBlockedHosts(c.state.blockedProviders())); err != nil {
		t.Fatal(err)
	}
	if err := appendLog(in.paths.Log, []string{"line 1", "line 2"}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := commandStatus(in.paths, nil, testStart.Add(10*time.Second), &stdout); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{"Service: not installed", "The service works", "Country: AF", "Anthropic  BLOCKED", "OpenAI     open", "hosts are blocked", "line 2"} {
		if !strings.Contains(output, want) {
			t.Errorf("the status must contain %q:\n%s", want, output)
		}
	}
	stdout.Reset()
	if err := commandStatus(in.paths, nil, testStart.Add(time.Hour), &stdout); err != nil || !strings.Contains(stdout.String(), "the service does not work") {
		t.Errorf("an old heartbeat must show that the service does not work: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(output, "Local database: none. The checks use only the online services.") {
		t.Errorf("the status must tell that the service has no local database:\n%s", output)
	}
}

func TestCommandStatusLocalDB(t *testing.T) {
	in, _, _ := newTestInstaller(t, "")
	built := time.Date(2026, 10, 1, 1, 36, 0, 0, time.UTC)
	state := &serviceState{Heartbeat: testStart, LocalDB: localDBState{File: "dbip-country-lite-2026-10.mmdb", BuildTime: built}}
	if err := saveServiceState(in.paths.State, state); err != nil {
		t.Fatal(err)
	}
	for now, want := range map[time.Time]string{
		testStart:                       "Local database: dbip-country-lite-2026-10.mmdb, built 2026-10-01. IP Geolocation by DB-IP (https://db-ip.com).",
		built.Add(maxDBAge + time.Hour): "Local database: dbip-country-lite-2026-10.mmdb, built 2026-10-01. It is older than 60 days, so the checks do not use it.",
	} {
		var stdout bytes.Buffer
		if err := commandStatus(in.paths, nil, now, &stdout); err != nil || !strings.Contains(stdout.String(), want) {
			t.Errorf("the status at %v must contain %q: %v\n%s", now, want, err, stdout.String())
		}
	}
}

// The install downloads the file of the local database before the start of the service.
func TestInstallDownloadsLocalDB(t *testing.T) {
	in, manager, stdout := newTestInstaller(t, "yes\n")
	var months, filesAtStart []string
	data := newTestDBData(t, dbType, testStart, testDBCountries)
	in.downloadDB = func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		if err := saveDBFile(filepath.Join(in.paths.Dir, getDBFileName(month)), data); err != nil {
			return nil, err
		}
		return openCountryDB(data, month)
	}
	manager.onInstall = func() { filesAtStart = findDBFiles(in.paths.Dir) }
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(in.paths.Dir, "dbip-country-lite-2026-10.mmdb")}; !slices.Equal(filesAtStart, want) || !slices.Equal(months, []string{"2026-10"}) {
		t.Errorf("the file must be in the app folder before the start of the service: %v, %v", filesAtStart, months)
	}
	for _, want := range []string{"The tool downloads the local country database from db-ip.com.", "The local country database dbip-country-lite-2026-10.mmdb is in the app folder."} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the install must show %q:\n%s", want, stdout.String())
		}
	}

	// A second install for a change of the settings keeps the file of the current month.
	stdout.Reset()
	if err := in.commandInstall([]string{"--blocked-countries", "AF"}); err != nil {
		t.Fatal(err)
	}
	if len(months) != 1 || strings.Contains(stdout.String(), "downloads") || !strings.Contains(stdout.String(), "dbip-country-lite-2026-10.mmdb is in the app folder") {
		t.Errorf("the install must not download the file of the current month again: %v\n%s", months, stdout.String())
	}

	// On the first day of a month, DB-IP can have no file of the month yet. Then the install takes the file of the month before.
	if err := os.Remove(filesAtStart[0]); err != nil {
		t.Fatal(err)
	}
	in.now = func() time.Time { return time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC) }
	months, filesAtStart = nil, nil
	notPublished := in.downloadDB
	in.downloadDB = func(ctx context.Context, month string) (*countryDB, error) {
		if month == "2026-11" {
			months = append(months, month)
			return nil, errDBNotPublished
		}
		return notPublished(ctx, month)
	}
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(months, []string{"2026-11", "2026-10"}) || len(filesAtStart) != 1 || getMonthOfFile(filesAtStart[0]) != "2026-10" {
		t.Errorf("the install must take the file of the month before: %v, %v", months, filesAtStart)
	}

	// A failed download gives a caution. The install does not stop, and the service tries again.
	if err := os.Remove(filesAtStart[0]); err != nil {
		t.Fatal(err)
	}
	in.downloadDB = func(context.Context, string) (*countryDB, error) { return nil, errOffline }
	stdout.Reset()
	if err := in.commandInstall(nil); err != nil || !strings.Contains(stdout.String(), "CAUTION: the tool did not download the local country database: offline") || manager.installs != 4 {
		t.Errorf("a failed download must give a caution: %v\n%s", err, stdout.String())
	}
}

func TestParsePauseDuration(t *testing.T) {
	cases := map[string]time.Duration{"30m": 30 * time.Minute, "4h": 4 * time.Hour, "3d": 72 * time.Hour, "1h30m": 90 * time.Minute}
	for value, want := range cases {
		if got, err := parsePauseDuration(value); err != nil || got != want {
			t.Errorf("parsePauseDuration(%q) = %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "10s", "0d", "400d", "-1h", "soon"} {
		if _, err := parsePauseDuration(value); err == nil {
			t.Errorf("parsePauseDuration(%q) must fail", value)
		}
	}
}

func TestPauseCommandChecks(t *testing.T) {
	paths := newAppPaths(t.TempDir())
	var stdout bytes.Buffer
	if err := commandPause(paths, []string{"--for", "1h"}, testStart, false, &stdout); err == nil {
		t.Error("a pause must need root")
	}
	if err := commandPause(paths, []string{"--for", "1h", "--forever"}, testStart, true, &stdout); err == nil {
		t.Error("--for and --forever together must fail")
	}
	if err := commandPause(paths, nil, testStart, true, &stdout); err == nil {
		t.Error("a pause without a time must fail")
	}
	if err := commandCheck(paths, &stdout); err == nil || !strings.Contains(err.Error(), "cannot reach the service") {
		t.Errorf("a check without the service must give a clear error: %v", err)
	}
}

func TestCommandStatusShowsLastFailure(t *testing.T) {
	in, _, _ := newTestInstaller(t, "")
	state := &serviceState{Heartbeat: testStart, IsPreBlocked: true, PreBlockSince: testStart}
	state.LastFailure = checkFailure{Time: testStart, Error: "2 of 4 location services replied (ipinfo.io: HTTP 429)"}
	if err := saveServiceState(in.paths.State, state); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := commandStatus(in.paths, nil, testStart.Add(time.Second), &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "The last full check failed at") || !strings.Contains(stdout.String(), "ipinfo.io: HTTP 429") {
		t.Errorf("the status must show the last failure:\n%s", stdout.String())
	}
}

func TestCommandStatusVerbose(t *testing.T) {
	in, _, _ := newTestInstaller(t, "")
	if err := saveServiceState(in.paths.State, &serviceState{Heartbeat: testStart}); err != nil {
		t.Fatal(err)
	}
	if err := appendLog(in.paths.Log, []string{"event line"}); err != nil {
		t.Fatal(err)
	}
	if err := appendLogLimit(in.paths.DebugLog, []string{"detail 1", "detail 2"}, maxDebugLogSize); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args          []string
		want, notWant string
	}{
		{nil, "event line", "detail"},
		{[]string{"--verbose"}, "detail 1", "event line"},
		{[]string{"--verbose", "--lines", "1"}, "detail 2", "detail 1"},
	}
	for _, testCase := range cases {
		var stdout bytes.Buffer
		if err := commandStatus(in.paths, testCase.args, testStart, &stdout); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), testCase.want) || strings.Contains(stdout.String(), testCase.notWant) {
			t.Errorf("status %v must show %q and not %q:\n%s", testCase.args, testCase.want, testCase.notWant, stdout.String())
		}
	}
}

func TestInstallTrayAndAgents(t *testing.T) {
	in, manager, stdout := newTestInstaller(t, "yes\n")
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	if manager.agentInstalls != 1 || manager.hasTray || manager.agentUserID != 501 || !strings.Contains(stdout.String(), "has no tray icon") {
		t.Errorf("without the tray binary, the install must start only the notices: %+v\n%s", manager, stdout.String())
	}
	source, _ := in.executable()
	if err := os.WriteFile(filepath.Join(filepath.Dir(source), trayBinaryName), []byte("tray"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(in.paths.TrayBinary); err != nil || string(data) != "tray" || !manager.hasTray {
		t.Errorf("the install must copy the tray binary and start the tray: %q, %v, %+v", data, err, manager)
	}
	if err := in.commandUninstall(); err != nil {
		t.Fatal(err)
	}
	if manager.agentUninstalls != 1 {
		t.Error("the uninstall must stop the agents")
	}
}
