package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"slices"
	"time"
)

// The commands of the sockets. All users can send check and update_check. Only root can send pause and resume.
const (
	socketCheck       = "check"
	socketUpdateCheck = "update_check"
	socketPause       = "pause"
	socketResume      = "resume"
)

// The codes of the answers.
const (
	codeOK             = "ok"
	codeTooSoon        = "too_soon"
	codeNotAllowed     = "not_allowed"
	codeNoPause        = "no_pause"
	codeBadRequest     = "bad_request"
	codeServiceTimeout = "timeout"
	// codeLocalBuild tells that the service is a local build, so it does not update itself.
	codeLocalBuild = "local_build"
)

// commandTimeout is the time limit of 1 command on a socket.
const commandTimeout = 10 * time.Second

// commandRequest is 1 line of JSON that a client sends on a socket.
type commandRequest struct {
	Command string `json:"command"`
	// Until is the end of a pause. A zero value with IsForever means a pause with no end.
	Until     time.Time `json:"until"`
	IsForever bool      `json:"forever,omitempty"`
	User      string    `json:"user,omitempty"`
}

// commandResponse is the answer of the service.
type commandResponse struct {
	Code string `json:"code"`
}

// request is a command that the socket server gives to the loop of the service. The loop answers on Reply.
type request struct {
	commandRequest
	Reply chan commandResponse
}

// listenSockets opens the check socket and the control socket. The rights of the folders protect the control socket:
// only root can open its folder. The function creates the folders at each start, because macOS clears /var/run at a restart.
func listenSockets(paths appPaths) (checkListener, controlListener net.Listener, err error) {
	if err := makeFolder(paths.SocketDir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := makeFolder(paths.ControlDir, 0o700); err != nil {
		return nil, nil, err
	}
	checkListener, err = listenUnix(paths.CheckSocket, 0o666)
	if err != nil {
		return nil, nil, err
	}
	controlListener, err = listenUnix(paths.ControlSocket, 0o600)
	if err != nil {
		checkListener.Close()
		return nil, nil, err
	}
	return checkListener, controlListener, nil
}

// makeFolder creates the folder and sets its rights, also when the folder exists.
func makeFolder(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// listenUnix removes an old socket file, opens the socket and sets the rights. The umask does not apply to Chmod.
func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// serveCommands accepts connections until the context ends. Each connection sends 1 request and gets 1 answer.
// The server gives only the allowed commands to the loop.
func serveCommands(ctx context.Context, listener net.Listener, allowed []string, requests chan<- request) {
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// A permanent error, for example too many open files, must not keep the processor busy.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go answerCommand(ctx, connection, allowed, requests)
	}
}

func answerCommand(ctx context.Context, connection net.Conn, allowed []string, requests chan<- request) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(commandTimeout))
	var received commandRequest
	response := commandResponse{Code: codeBadRequest}
	if err := json.NewDecoder(connection).Decode(&received); err == nil {
		response = forwardCommand(ctx, received, allowed, requests)
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func forwardCommand(ctx context.Context, received commandRequest, allowed []string, requests chan<- request) commandResponse {
	if !slices.Contains(allowed, received.Command) {
		return commandResponse{Code: codeNotAllowed}
	}
	next := request{commandRequest: received, Reply: make(chan commandResponse, 1)}
	timeout := time.After(commandTimeout / 2)
	select {
	case requests <- next:
	case <-timeout:
		return commandResponse{Code: codeServiceTimeout}
	case <-ctx.Done():
		return commandResponse{Code: codeServiceTimeout}
	}
	select {
	case response := <-next.Reply:
		return response
	case <-timeout:
		return commandResponse{Code: codeServiceTimeout}
	}
}

// sendCommand sends 1 command to the socket of the service and returns the answer.
func sendCommand(socketPath string, command commandRequest) (commandResponse, error) {
	connection, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return commandResponse{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(commandTimeout))
	if err := json.NewEncoder(connection).Encode(command); err != nil {
		return commandResponse{}, err
	}
	var response commandResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return commandResponse{}, err
	}
	return response, nil
}
