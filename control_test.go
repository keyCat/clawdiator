//go:build !windows

package main

import (
	"context"
	"os"
	"testing"
)

// newSocketPaths returns paths under a short folder. A Unix socket path must be shorter than 104 bytes on macOS.
func newSocketPaths(t *testing.T) appPaths {
	root, err := os.MkdirTemp("/tmp", "cd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return newAppPaths(root)
}

func TestSockets(t *testing.T) {
	paths := newSocketPaths(t)
	if err := os.MkdirAll(paths.SocketDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// An old socket file from a crash must not stop the start.
	if err := os.WriteFile(paths.CheckSocket, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkListener, controlListener, err := listenSockets(paths)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(paths.CheckSocket); info.Mode().Perm() != 0o666 {
		t.Errorf("all users must open the check socket: %v", info.Mode())
	}
	if info, _ := os.Stat(paths.ControlDir); info.Mode().Perm() != 0o700 {
		t.Errorf("only root must open the folder of the control socket: %v", info.Mode())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan request)
	go serveCommands(ctx, checkListener, []string{socketCheck}, requests)
	go serveCommands(ctx, controlListener, []string{socketPause, socketResume}, requests)
	go func() {
		for {
			select {
			case next := <-requests:
				next.Reply <- commandResponse{Code: codeOK + ":" + next.Command + ":" + next.User}
			case <-ctx.Done():
				return
			}
		}
	}()

	cases := []struct {
		socket, command, user, want string
	}{
		{paths.CheckSocket, socketCheck, "", "ok:check:"},
		{paths.CheckSocket, socketPause, "", codeNotAllowed},
		{paths.ControlSocket, socketPause, "ivan", "ok:pause:ivan"},
		{paths.ControlSocket, socketCheck, "", codeNotAllowed},
	}
	for _, testCase := range cases {
		response, err := sendCommand(testCase.socket, commandRequest{Command: testCase.command, User: testCase.user})
		if err != nil || response.Code != testCase.want {
			t.Errorf("%s on %s = %+v, %v, want %q", testCase.command, testCase.socket, response, err, testCase.want)
		}
	}
	cancel()
	if _, err := sendCommand(paths.CheckSocket+".missing", commandRequest{Command: socketCheck}); err == nil {
		t.Error("a missing socket must give an error")
	}
}
