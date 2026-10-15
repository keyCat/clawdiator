package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The watch must find an atomic rename over the file, a write in place, and a folder that comes later.
func TestWatchFiles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS has the watch now")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := filepath.Join(t.TempDir(), "app")
	path := filepath.Join(dir, "state.json")
	changes := watchFiles(ctx, path)
	expectChange := func(name string) {
		t.Helper()
		select {
		case <-changes:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s must give a value", name)
		}
	}
	// The folder does not exist at the start. The watch tries again every second.
	time.Sleep(100 * time.Millisecond)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectChange("the first file")
	// Wait until the watch opened the new file again.
	time.Sleep(1200 * time.Millisecond)
	for len(changes) > 0 {
		<-changes
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
	expectChange("an atomic rename")
	time.Sleep(200 * time.Millisecond)
	for len(changes) > 0 {
		<-changes
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("3"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	expectChange("a write in place")
}
