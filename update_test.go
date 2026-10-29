package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.1.2", "v0.1.1", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.1", "v0.1.1", false},
		{"v0.1.0", "v0.1.1", false},
		{"v0.1.2", "dev", true},
		{"latest", "v0.1.1", false},
		{"v0.1.2-rc1", "v0.1.1", false},
		{"", "v0.1.1", false},
	}
	for _, testCase := range cases {
		if got := isNewerVersion(testCase.latest, testCase.current); got != testCase.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", testCase.latest, testCase.current, got, testCase.want)
		}
	}
}

// TestFetchLatestVersion checks that only a correct tag from the redirect of GitHub comes out.
func TestFetchLatestVersion(t *testing.T) {
	const tagURL = "https://github.com/keyCat/clawdiator/releases/tag/"
	locations := map[string]string{
		"/good":   tagURL + "v1.2.3",
		"/no-tag": "https://github.com/keyCat/clawdiator/releases",
		"/path":   tagURL + "v1.2.3/../../x",
		"/word":   tagURL + "latest",
		"/query":  tagURL + "v1.2.3?x=1",
		"/ok":     tagURL + "v1.2.3",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(r.URL.Path, "/releases/latest")
		w.Header().Set("Location", locations[name])
		if name == "/ok" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	if tag, err := fetchLatestVersion(context.Background(), server.URL+"/good"); err != nil || tag != "v1.2.3" {
		t.Errorf("the tag = %q, %v", tag, err)
	}
	for _, name := range []string{"/no-tag", "/path", "/word", "/query", "/ok"} {
		if tag, err := fetchLatestVersion(context.Background(), server.URL+name); err == nil {
			t.Errorf("%s must give an error, not the tag %q", name, tag)
		}
	}
}

type testArchiveEntry struct {
	name, body, link string
}

func buildTestArchive(t *testing.T, entries ...testArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if entry.link != "" {
			header = &tar.Header{Name: entry.name, Linkname: entry.link, Mode: 0o777, Typeflag: tar.TypeSymlink}
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestDownloadRelease checks the checksum and the entries of the archive before the binaries reach the disk.
func TestDownloadRelease(t *testing.T) {
	const archiveName = "clawdiator-darwin-arm64.tar.gz"
	files := map[string][]byte{}
	addRelease := func(tag string, archive []byte, sumOf []byte) {
		sum := sha256.Sum256(sumOf)
		files["/releases/download/"+tag+"/"+archiveName] = archive
		files["/releases/download/"+tag+"/SHA256SUMS"] = []byte(hex.EncodeToString(sum[:]) + "  " + archiveName + "\n")
	}
	good := buildTestArchive(t,
		testArchiveEntry{name: "clawdiator", body: "new main"},
		testArchiveEntry{name: "clawdiator-tray", body: "new tray"},
		testArchiveEntry{name: "README.md", body: "readme"})
	addRelease("v1.0.0", good, good)
	addRelease("v2.0.0", good, []byte("other data"))
	link := buildTestArchive(t, testArchiveEntry{name: "clawdiator", link: "/bin/sh"})
	addRelease("v3.0.0", link, link)
	noMain := buildTestArchive(t, testArchiveEntry{name: "clawdiator-tray", body: "new tray"})
	addRelease("v4.0.0", noMain, noMain)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, isFound := files[r.URL.Path]
		if !isFound {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	dir := t.TempDir()
	binary, err := downloadRelease(context.Background(), server.URL, "v1.0.0", "arm64", dir)
	if err != nil || binary != filepath.Join(dir, "clawdiator") {
		t.Fatalf("the download = %q, %v", binary, err)
	}
	for name, want := range map[string]string{"clawdiator": "new main", "clawdiator-tray": "new tray"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v", name, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("only the 2 binaries must come out of the archive: %v", err)
	}
	for _, tag := range []string{"v2.0.0", "v3.0.0", "v4.0.0", "v1.0.0/../../x", "v9.0.0"} {
		dir := t.TempDir()
		if _, err := downloadRelease(context.Background(), server.URL, tag, "arm64", dir); err == nil {
			t.Errorf("%s must give an error", tag)
		}
		if entries, _ := os.ReadDir(dir); tag == "v2.0.0" && len(entries) != 0 {
			t.Errorf("an archive with a wrong checksum must write nothing: %v", entries)
		}
	}
}

// TestRealRelease downloads the newest release from GitHub. Set CLAWDIATOR_NETWORK_TEST=1 to run it.
func TestRealRelease(t *testing.T) {
	if os.Getenv("CLAWDIATOR_NETWORK_TEST") != "1" {
		t.Skip("set CLAWDIATOR_NETWORK_TEST=1 to download the newest release from GitHub")
	}
	tag, err := fetchLatestVersion(context.Background(), releasesURL)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := downloadRelease(context.Background(), releasesURL, tag, runtime.GOARCH, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := readUpdateInfo(context.Background(), binary)
	t.Logf("the newest release is %s. Its update-info: %+v, %v", tag, info, err)
}

// TestDriverUpdatesItself checks the decisions of the update check. Without the setting, the service only records
// the new version. With the setting, it installs the version. A new consent text stops it with 1 event and no new downloads.
func TestDriverUpdatesItself(t *testing.T) {
	td := newTestDriver(t)
	td.currentVersion = "v0.1.1"
	setAutoUpdate := func(isAuto bool) {
		t.Helper()
		if err := saveConfig(td.paths.Config, &Config{AutoUpdate: isAuto, Consent: consent{Version: 4}}); err != nil {
			t.Fatal(err)
		}
	}
	setAutoUpdate(false)
	latest, latestCalls, prepareCalls := "v0.1.2", 0, 0
	info := updateInfo{Binary: "/update/clawdiator", Version: "v0.1.2", Consent: 4}
	var started []string
	td.update = updater{
		clean:   func() {},
		latest:  func(context.Context) (string, error) { latestCalls++; return latest, nil },
		prepare: func(context.Context, string) (updateInfo, error) { prepareCalls++; return info, nil },
		start:   func(binary string) error { started = append(started, binary); return nil },
	}
	td.step()
	if latestCalls != 0 {
		t.Fatalf("the first check must wait %v after the start", updateFirstCheckDelay)
	}
	td.clock = td.clock.Add(updateFirstCheckDelay)
	td.step()
	if update := td.core.state.Update; latestCalls != 1 || prepareCalls != 0 || len(started) != 0 || update.Latest != "v0.1.2" || update.IsAuto {
		t.Fatalf("without automatic updates, the check must only record the new version: checks %d, downloads %d, starts %v, %+v",
			latestCalls, prepareCalls, started, update)
	}

	setAutoUpdate(true)
	td.clock = td.clock.Add(updateCheckInterval)
	td.step()
	if !slices.Equal(started, []string{"/update/clawdiator"}) || !td.core.state.Update.IsAuto {
		t.Fatalf("with automatic updates, a newer version must start the install: %v, %+v", started, td.core.state.Update)
	}
	if log, _ := os.ReadFile(td.paths.Log); !strings.Contains(string(log), "all update the service installs version v0.1.2 automatically") {
		t.Errorf("the log must tell about the update before the install stops the service:\n%s", log)
	}

	// A new consent text stops the automatic update. The log gets 1 event, and the service does not download the tag again.
	info.Consent = 5
	for range 2 {
		td.clock = td.clock.Add(updateCheckInterval)
		td.step()
	}
	needed := 0
	for _, change := range td.core.state.Events {
		if change.Kind == eventUpdateNeeded && change.Version == "v0.1.2" {
			needed++
		}
	}
	if len(started) != 1 || prepareCalls != 2 || needed != 1 || td.core.state.Update.ConsentTag != "v0.1.2" {
		t.Errorf("a new consent: starts %v, downloads %d, events %d, state %+v", started, prepareCalls, needed, td.core.state.Update)
	}

	// The newest version needs no download. A local build does not check.
	latest = "v0.1.1"
	td.clock = td.clock.Add(updateCheckInterval)
	td.step()
	td.currentVersion = "dev"
	td.clock = td.clock.Add(updateCheckInterval)
	td.step()
	if prepareCalls != 2 || latestCalls != 5 {
		t.Errorf("the newest version and a local build must download nothing: downloads %d, checks %d", prepareCalls, latestCalls)
	}
}

// TestSetAutoUpdate checks the admin command that turns the automatic updates on and off.
func TestSetAutoUpdate(t *testing.T) {
	in, _, _ := newTestInstaller(t, "yes\n")
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(in.paths.Config); cfg.AutoUpdate {
		t.Fatal("a new installation must have the automatic updates off")
	}
	for _, value := range []string{"on", "off"} {
		if err := in.commandUpdate([]string{"--auto-install", value}); err != nil {
			t.Fatal(err)
		}
		if cfg, err := loadConfig(in.paths.Config); err != nil || cfg.AutoUpdate != (value == "on") || cfg.Consent.Version != consentVersion {
			t.Errorf("--auto-install %s: %+v, %v", value, cfg, err)
		}
	}
	if err := in.commandUpdate([]string{"--auto-install", "yes"}); err == nil {
		t.Error("--auto-install must take only on or off")
	}
	in.isRoot = func() bool { return false }
	if err := in.commandUpdate([]string{"--auto-install", "on"}); err == nil {
		t.Error("--auto-install must need root, because the service installs as root")
	}
}

// TestDriverForcedUpdateCheck checks that a user can start the update check at once, with the limit of the check command,
// and that the result is in state.json at once for the waiting client.
func TestDriverForcedUpdateCheck(t *testing.T) {
	td := newTestDriver(t)
	td.currentVersion = "v0.1.1"
	checks := 0
	td.update.latest = func(context.Context) (string, error) { checks++; return "", errOffline }
	td.step()
	if response := td.handleRequest(commandRequest{Command: socketUpdateCheck}); response.Code != codeOK {
		t.Fatalf("the first forced check = %s", response.Code)
	}
	td.drainProbes()
	saved, err := loadServiceState(td.paths.State)
	if err != nil || checks != 1 || saved.Update.Error != "offline" || !saved.Update.CheckTime.Equal(td.clock) {
		t.Fatalf("a forced check must run before the first delay and save its result at once: checks %d, %+v, %v", checks, saved.Update, err)
	}
	if response := td.handleRequest(commandRequest{Command: socketUpdateCheck}); response.Code != codeTooSoon || checks != 1 {
		t.Errorf("a second forced check within %v = %s, checks %d", userCheckInterval, response.Code, checks)
	}
	td.clock = td.clock.Add(userCheckInterval)
	td.handleRequest(commandRequest{Command: socketUpdateCheck})
	td.drainProbes()
	if checks != 2 {
		t.Errorf("a forced check after %v must run: checks %d", userCheckInterval, checks)
	}
	td.currentVersion = "dev"
	td.clock = td.clock.Add(userCheckInterval)
	if response := td.handleRequest(commandRequest{Command: socketUpdateCheck}); response.Code != codeLocalBuild {
		t.Errorf("a local build must answer %s, not %s", codeLocalBuild, response.Code)
	}
}

func TestDescribeUpdateResult(t *testing.T) {
	if _, err := describeUpdateResult(updateState{Error: "offline"}); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("a failed check must give its error: %v", err)
	}
	for update, want := range map[updateState]string{
		{}:                               text(msgUpdateNewest, version),
		{Latest: "v9.0.0", IsAuto: true}: text(msgUpdateInstalling, "v9.0.0"),
		{Latest: "v9.0.0"}:               text(msgNoticeUpdateAvailable, "v9.0.0", websiteURL),
		{Latest: "v9.0.0", IsAuto: true, ConsentTag: "v9.0.0"}: text(msgNoticeUpdateAvailable, "v9.0.0", websiteURL),
	} {
		if got, err := describeUpdateResult(update); err != nil || got != want {
			t.Errorf("%+v = %q, %v, want %q", update, got, err, want)
		}
	}
}

// TestInstallUnattendedNeedsConsent checks that the automatic update changes nothing when the consent text is new.
func TestInstallUnattendedNeedsConsent(t *testing.T) {
	in, manager, stdout := newTestInstaller(t, "yes\n")
	if err := in.commandInstall([]string{"--unattended"}); err == nil || manager.installs != 0 || strings.Contains(stdout.String(), "Type \"yes\"") {
		t.Fatalf("--unattended without the consent must stop before any change: %v, installs %d\n%s", err, manager.installs, stdout.String())
	}
	if _, err := os.Stat(in.paths.Binary); !os.IsNotExist(err) {
		t.Errorf("--unattended without the consent must not copy the binary: %v", err)
	}
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	in.stdin = strings.NewReader("")
	if err := in.commandInstall([]string{"--unattended"}); err != nil || manager.installs != 2 {
		t.Errorf("--unattended with the current consent must install without a question: %v, installs %d", err, manager.installs)
	}
}

// TestUpdateTexts checks the notices, the log lines and the tray item about a new version.
func TestUpdateTexts(t *testing.T) {
	for _, testCase := range []struct {
		lastVersion string
		isFirstRun  bool
		want        bool
	}{
		{"v0.0.1", false, true},
		{version, false, false},
		{"", false, false},
		{"v0.0.1", true, false},
	} {
		model := newAgentModel(agentSettings{Version: testCase.lastVersion})
		notices := model.update(testStart, &serviceState{Heartbeat: testStart}, testCase.isFirstRun)
		isNotice := slices.Contains(notices, notice{text(msgNoticeUpdated, version)})
		if isNotice != testCase.want {
			t.Errorf("the last version %q, first run %v: the update notice %v, want %v: %v", testCase.lastVersion, testCase.isFirstRun, isNotice, testCase.want, notices)
		}
	}
	// A waiting version gives a notice at once and again after updateReminderInterval. A newer tag gives a notice at once.
	// With automatic updates, only a failed install gives a notice.
	model := newAgentModel(agentSettings{})
	now := testStart
	for index, step := range []struct {
		update updateState
		after  time.Duration
		want   bool
	}{
		{updateState{Latest: "v99.0.0"}, 0, true},
		{updateState{Latest: "v99.0.0"}, time.Hour, false},
		{updateState{Latest: "v99.0.0"}, updateReminderInterval, true},
		{updateState{Latest: "v99.1.0"}, time.Minute, true},
		{updateState{Latest: "v99.2.0", IsAuto: true}, time.Minute, false},
		{updateState{Latest: "v99.2.0", IsAuto: true, Error: "offline"}, time.Minute, true},
	} {
		now = now.Add(step.after)
		notices := model.update(now, &serviceState{Heartbeat: now, Update: step.update}, false)
		reminder := notice{text(msgNoticeUpdateAvailable, step.update.Latest, websiteURL)}
		if slices.Contains(notices, reminder) != step.want {
			t.Errorf("step %d, %+v: the reminder must be %v: %v", index, step.update, step.want, notices)
		}
	}
	for _, change := range []event{{Kind: eventUpdate, Version: "v0.2.0"}, {Kind: eventUpdateNeeded, Version: "v0.2.0"}} {
		if lines := formatEventLog(change); len(lines) != 1 || !strings.Contains(lines[0], "v0.2.0") {
			t.Errorf("the event %s must give 1 log line with the version: %v", change.Kind, lines)
		}
	}
	for update, want := range map[updateState]trayView{
		{Latest: "v99.0.0"}:               {Update: text(msgTrayUpdate, "v99.0.0"), HasBadge: true},
		{Latest: "v99.0.0", IsAuto: true}: {Update: text(msgTrayUpdate, "v99.0.0")},
		{}:                                {},
	} {
		if view := buildTrayView(&serviceState{Update: update}, false); view.Update != want.Update || view.HasBadge != want.HasBadge {
			t.Errorf("%+v: the tray item %q and the dot %v, want %q and %v", update, view.Update, view.HasBadge, want.Update, want.HasBadge)
		}
	}
}
