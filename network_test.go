//go:build !windows

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestServiceWithRealNetwork runs the real service loop for 20 seconds with the real location services.
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runService(ctx, paths) }()

	time.Sleep(time.Second)
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
		t.Errorf("a full check must pass in 20 seconds: %+v", state)
	}
	// A fast network can end the pre-block in 1 second, so the log is the proof of the pre-block.
	if !strings.Contains(string(log), "all pre-block the network changed") || !strings.Contains(string(log), "all pre-block-end") {
		t.Errorf("the log must show the start and the end of the pre-block:\n%s", log)
	}
	if count, _ := countBlockedHosts(paths.Hosts); count != len(getBlockedHosts(state.blockedProviders())) {
		t.Errorf("the hosts file must match the blocked providers %v:\n%s", state.blockedProviders(), data)
	}
}
