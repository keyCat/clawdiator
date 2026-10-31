package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatSizeAndDuration(t *testing.T) {
	cases := []struct{ got, want string }{
		{formatSize(languageEnglish, 512_000), "512 KB"},
		{formatSize(languageEnglish, 6_686_397), "6.7 MB"},
		{formatSize(languageRussian, 6_686_397), "6,7 МБ"},
		{formatDuration(languageEnglish, 42*time.Second), "42 s"},
		{formatDuration(languageEnglish, 110*time.Second), "1 min 50 s"},
		{formatDuration(languageRussian, 3*time.Hour+5*time.Minute), "3 ч 5 мин"},
		{formatDuration(languageEnglish, -time.Second), "0 s"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestProgressLineFitsTerminal checks the longest progress lines. A line of 80 characters or more takes 2 rows
// in a terminal of the usual width, and then the redraw cannot replace it.
func TestProgressLineFitsTerminal(t *testing.T) {
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	longest := ""
	for _, lang := range []language{languageEnglish, languageRussian} {
		for _, total := range []int64{maxArchiveSize, 999_999} {
			for _, received := range []int64{1, total / 2, total - 1, total} {
				// The first redraw comes after progressRedrawInterval, so the speed has no shorter time.
				for _, elapsed := range []time.Duration{progressRedrawInterval, time.Second, 59*time.Minute + 59*time.Second, 1000 * time.Hour} {
					p := &downloadProgress{start: start, received: received, total: total}
					line := renderBar(received, total) + p.describe(lang, start.Add(elapsed))
					if utf8.RuneCountInString(line) > utf8.RuneCountInString(longest) {
						longest = line
					}
				}
			}
		}
	}
	if count := utf8.RuneCountInString(longest); count > 79 {
		t.Errorf("the longest line has %d characters: %q", count, longest)
	}
}

// TestProgressOutputs checks the 2 kinds of outputs. A terminal gets redraws in place and 1 final line.
// A log gets 1 line every 10 seconds and the final line. After an error, the terminal keeps the last line.
func TestProgressOutputs(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	var terminal, lines bytes.Buffer
	p := newDownloadProgress(func() time.Time { return clock },
		progressOutput{writer: &terminal, lang: languageEnglish, isTerminal: true},
		progressOutput{writer: &lines, lang: languageRussian})
	p.begin(25_000_000)
	for range 25 {
		clock = clock.Add(time.Second)
		p.add(1_000_000)
	}
	p.end(nil)
	want := "10,0 МБ из 25,0 МБ · 40% · 1,0 МБ/с · ещё 15 с\n20,0 МБ из 25,0 МБ · 80% · 1,0 МБ/с · ещё 5 с\nПрограмма скачала 25,0 МБ за 25 с.\n"
	if lines.String() != want {
		t.Errorf("the log lines = %q, want %q", lines.String(), want)
	}
	if strings.Count(terminal.String(), "\r") != 26 || !strings.HasSuffix(terminal.String(), "\r"+clearLineEnd+"The tool downloaded 25.0 MB in 25 s.\n") {
		t.Errorf("the terminal must get 25 redraws and then the final line in place of the bar: %q", terminal.String())
	}
	if first := strings.Split(terminal.String(), "\r")[1]; first != "[------------------] 1.0 MB of 25.0 MB · 4%"+clearLineEnd {
		t.Errorf("in the first seconds, the line must show no speed and no time to the end: %q", first)
	}

	terminal.Reset()
	p.begin(10)
	clock = clock.Add(time.Second)
	p.add(5)
	p.end(errors.New("no data"))
	if !strings.HasSuffix(terminal.String(), clearLineEnd+"\n") || strings.Contains(terminal.String(), "downloaded") {
		t.Errorf("after an error, the terminal must end the last progress line and write no final line: %q", terminal.String())
	}
}

// TestUpdateLog checks that each line of update.log starts with the time, also a line that comes in parts.
func TestUpdateLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.log")
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	log := openUpdateLog(path, func() time.Time { return clock })
	log.writeLine("first")
	_, _ = log.Write([]byte("a part, "))
	_, _ = log.Write([]byte("the end\nsecond\n"))
	log.Close()
	data, err := os.ReadFile(path)
	stamp := clock.Format(debugTimeFormat) + " "
	if want := stamp + "first\n" + stamp + "a part, the end\n" + stamp + "second\n"; err != nil || string(data) != want {
		t.Errorf("update.log = %q, %v, want %q", data, err, want)
	}

	var missing *updateLog
	missing.writeLine("a log without a file writes nothing")
	missing.Close()
	if openUpdateLog(filepath.Join(t.TempDir(), "no-folder", "update.log"), time.Now) != nil {
		t.Error("a log in a folder that does not exist must give nil, so that the update continues")
	}
}
