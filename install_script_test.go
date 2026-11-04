package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInstallScript runs install.sh with a release in a local folder. The archive has a fake clawdiator,
// and a fake sudo runs the command. The fake clawdiator writes its arguments to a file.
func TestInstallScript(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("install.sh is for macOS and Linux")
	}
	stage, bin, release := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestScript(t, filepath.Join(stage, "clawdiator"), `printf '%s\n' "$@" > "$CLAWDIATOR_TEST_ARGS"`)
	writeTestScript(t, filepath.Join(bin, "sudo"), `exec "$@"`)
	archive := fmt.Sprintf("clawdiator-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if output, err := exec.Command("tar", "-czf", filepath.Join(release, archive), "-C", stage, "clawdiator").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(release, archive))
	if err != nil {
		t.Fatal(err)
	}
	correct := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), archive)

	run := func(sums string) (string, string, error) {
		if err := os.WriteFile(filepath.Join(release, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
			t.Fatal(err)
		}
		argsFile := filepath.Join(t.TempDir(), "args")
		command := exec.Command("sh", "install.sh", "--yes")
		command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "CLAWDIATOR_RELEASE_URL=file://"+release, "CLAWDIATOR_TEST_ARGS="+argsFile)
		output, err := command.CombinedOutput()
		args, _ := os.ReadFile(argsFile)
		return string(output), string(args), err
	}

	if output, args, err := run("0000  other.tar.gz\n" + correct); err != nil || args != "install\n--yes\n" {
		t.Errorf("install.sh must run clawdiator install with the options: %v, args %q\n%s", err, args, output)
	}
	wrong := strings.Repeat("0", 64) + "  " + archive + "\n"
	for name, sums := range map[string]string{"a wrong checksum": wrong, "no line for the archive": "0000  other.tar.gz\n"} {
		output, args, err := run(sums)
		if err == nil || args != "" || !strings.Contains(output, "error: ") {
			t.Errorf("%s must stop install.sh before clawdiator: %v, args %q\n%s", name, err, args, output)
		}
	}
}

func writeTestScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
