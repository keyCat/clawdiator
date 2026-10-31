package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeTerminal writes a program that saves its name and its arguments, 1 on each line, to the file "args" in its folder.
func writeFakeTerminal(t *testing.T, dir, name string) {
	t.Helper()
	// printf, not echo: the echo of dash changes the characters "\n" in an argument to a new line.
	script := "#!/bin/sh\nprintf '%s\\n' \"${0##*/}\" \"$@\" > \"" + filepath.Join(dir, "args") + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFakeArgs(t *testing.T, dir string) []string {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(filepath.Join(dir, "args")); err == nil && strings.HasSuffix(string(data), "\n") {
			return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
	}
	t.Fatal("the terminal program did not start")
	return nil
}

// TestOpenTerminal checks the order of the search, the option of each program, and the wait for Enter at the end.
func TestOpenTerminal(t *testing.T) {
	dir := t.TempDir()
	writeFakeTerminal(t, dir, "xterm")
	writeFakeTerminal(t, dir, "konsole")
	t.Setenv("PATH", dir)
	if err := openTerminal("sudo '/usr/local/lib/clawdiator/clawdiator' update"); err != nil {
		t.Fatal(err)
	}
	args := readFakeArgs(t, dir)
	if len(args) != 5 || args[0] != "konsole" || args[1] != "-e" || args[2] != "sh" || args[3] != "-c" {
		t.Fatalf("konsole must come before xterm, with -e sh -c: %q", args)
	}
	if !strings.HasPrefix(args[4], "sudo '/usr/local/lib/clawdiator/clawdiator' update; ") || !strings.HasSuffix(args[4], "; read _") {
		t.Errorf("the command must run first and then wait for Enter: %q", args[4])
	}

	t.Setenv("PATH", t.TempDir())
	if err := openTerminal("clawdiator"); err == nil {
		t.Error("without a terminal program, openTerminal must give an error")
	}
}
