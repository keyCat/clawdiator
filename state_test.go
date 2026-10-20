package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"clawdiator/internal/geocheck"
)

func TestServiceStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	empty, err := loadServiceState(path)
	if err != nil || empty.Heartbeat.IsZero() == false || len(empty.Events) != 0 {
		t.Fatalf("a missing file must give an empty state: %+v, %v", empty, err)
	}
	state := &serviceState{Heartbeat: testStart, IsPreBlocked: true, PreBlockSince: testStart, Country: "DE, Hesse", PublicIP: "192.0.2.1"}
	state.provider(geocheck.OpenAI).IsBlocked = true
	state.Pause = pauseState{IsOn: true, Since: testStart, User: "ivan"}
	if err := saveServiceState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadServiceState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Heartbeat.Equal(testStart) || !loaded.IsPreBlocked || !loaded.provider(geocheck.OpenAI).IsBlocked ||
		!loaded.Pause.IsOn || !loaded.Pause.Until.IsZero() || loaded.PublicIP != "192.0.2.1" {
		t.Errorf("the loaded state = %+v", loaded)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o044 != 0o044 {
			t.Errorf("all users must read state.json: %v, %v", info.Mode(), err)
		}
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temporary file must not stay: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServiceState(path); err == nil {
		t.Error("a damaged file must give an error")
	}
}
