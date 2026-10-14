//go:build !windows

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestServiceWithRealNetwork runs the real service loop for 25 seconds with the real location services.
// The service also downloads the local database from db-ip.com. Then the test asks for a full check that uses it.
// All files, also the hosts file, are in a temporary folder, so the test does not change the system.
// The test sends requests to the location services, so it runs only with CLAWDIATOR_NETWORK_TEST=1.
func TestServiceWithRealNetwork(t *testing.T) {
	if os.Getenv("CLAWDIATOR_NETWORK_TEST") != "1" {
		t.Skip("set CLAWDIATOR_NETWORK_TEST=1 to send real requests to the location services")
	}
	paths := newSocketPaths(t)
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(strings.TrimSuffix(paths.Hosts, "/hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Hosts, []byte(sampleHosts), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runService(ctx, paths) }()

	// The check command must come after the download. Only 1 check command in 30 seconds is possible.
	for start := time.Now(); time.Since(start) < 15*time.Second; time.Sleep(200 * time.Millisecond) {
		if debugLog, _ := os.ReadFile(paths.DebugLog); strings.Contains(string(debugLog), "dbip the download of") {
			break
		}
	}
	if response, err := sendCommand(paths.CheckSocket, commandRequest{Command: socketCheck}); err != nil || response.Code != codeOK {
		t.Errorf("the check socket = %+v, %v", response, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	state, err := loadServiceState(paths.State)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(paths.Hosts)
	log, _ := os.ReadFile(paths.Log)
	t.Logf("country: %q, pre-block: %v, blocked: %v", state.Country, state.IsPreBlocked, state.blockedProviders())
	t.Logf("log:\n%s", log)
	debugLog, _ := os.ReadFile(paths.DebugLog)
	t.Logf("verbose log:\n%s", debugLog)
	if state.LastFullCheck.IsZero() || state.IsPreBlocked {
		t.Errorf("a full check must pass in 25 seconds: %+v", state)
	}
	if state.LocalDB.File != getDBFileName(getDBMonth(time.Now())) || !strings.Contains(string(debugLog), "; dbip: ") {
		t.Errorf("the service must download the local database and use it in the full check: %+v", state.LocalDB)
	}
	if files := findDBFiles(paths.Dir); len(files) != 1 {
		t.Errorf("the app folder must have 1 file of the local database: %v", files)
	}
	// A fast network can end the pre-block in 1 second, so the log is the proof of the pre-block.
	if !strings.Contains(string(log), "all pre-block the network changed") || !strings.Contains(string(log), "all pre-block-end") {
		t.Errorf("the log must show the start and the end of the pre-block:\n%s", log)
	}
	if count, _ := countBlockedHosts(paths.Hosts); count != len(getBlockedHosts(state.blockedProviders())) {
		t.Errorf("the hosts file must match the blocked providers %v:\n%s", state.blockedProviders(), data)
	}
}
