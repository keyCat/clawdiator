package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"geocheck"
)

func TestFormatEventLog(t *testing.T) {
	block := event{Time: testStart, Kind: eventBlock, Providers: geocheck.Providers(), Reasons: map[geocheck.Provider]string{
		geocheck.Anthropic: "country CN is not supported by Anthropic\n(fake line)",
		geocheck.OpenAI:    "country CN is not supported by OpenAI",
	}}
	lines := formatEventLog(block)
	if len(lines) != 2 || lines[0] != "2026-10-14T09:00:00Z anthropic blocked country CN is not supported by Anthropic (fake line)" ||
		!strings.HasPrefix(lines[1], "2026-10-14T09:00:00Z openai blocked") {
		t.Errorf("block lines = %q", lines)
	}
	cases := map[eventKind]string{
		eventPreBlock:    "all pre-block",
		eventPreBlockEnd: "all pre-block-end",
		eventPauseEnd:    "all resumed",
		eventError:       "service error disk full",
	}
	for kind, want := range cases {
		if got := formatEventLog(event{Time: testStart, Kind: kind, Error: "disk full"}); len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("%s = %q", kind, got)
		}
	}
	pause := formatEventLog(event{Time: testStart, Kind: eventPause, Until: testStart.Add(time.Hour), User: "ivan"})
	if pause[0] != "2026-10-14T09:00:00Z all paused until 2026-10-14T10:00:00Z by user ivan" {
		t.Errorf("pause = %q", pause)
	}
	if got := formatEventLog(event{Time: testStart, Kind: eventPause}); !strings.Contains(got[0], "with no end") {
		t.Errorf("a pause with no end = %q", got)
	}
}

func TestAppendLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clawdiator.log")
	if err := os.WriteFile(path, make([]byte, maxLogSize), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendLog(path, []string{"new line"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new line\n" {
		t.Errorf("a full log must move to .1: %q", data)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != maxLogSize {
		t.Errorf("the old log must be in .1: %v", err)
	}
}

func TestAppendLogLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clawdiator-debug.log")
	for index := 0; index < 3; index++ {
		if err := appendLogLimit(path, []string{"0123456789"}, 15); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	if string(current) != "0123456789\n" || string(old) != "0123456789\n0123456789\n" {
		t.Errorf("the log must move to .1 at its own limit: %q, %q", current, old)
	}
}

func TestBuildTrayIcon(t *testing.T) {
	for state, want := range trayColors {
		img, err := png.Decode(bytes.NewReader(buildTrayIcon(state)))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != trayIconSize || img.Bounds().Dy() != trayIconSize {
			t.Errorf("the icon size = %v", img.Bounds())
		}
		if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
			t.Errorf("state %d: the corner must be transparent", state)
		}
		red, green, blue, _ := img.At(8, 16).RGBA()
		if uint8(red>>8) != want.R || uint8(green>>8) != want.G || uint8(blue>>8) != want.B {
			t.Errorf("state %d: the circle must have the color %v", state, want)
		}
		red, green, blue, _ = img.At(trayIconSize/2, 12).RGBA()
		isWhite := red == 0xFFFF && green == 0xFFFF && blue == 0xFFFF
		if isWhite != (state == trayAlert) {
			t.Errorf("state %d: only the alert icon has the white mark", state)
		}
	}
}
