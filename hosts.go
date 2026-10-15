package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	hostsBeginMarker = "# BEGIN clawdiator"
	hostsEndMarker   = "# END clawdiator"
	// hostsLineEndNote marks a block that the service added to a file without a line end on its last line.
	// The removal then removes the line end that the service added, so the file becomes the same as before.
	hostsLineEndNote = "The original file had no line end at its end."
)

// getHostsPath returns the path of the hosts file of the system.
func getHostsPath() string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return filepath.Join(root, "System32", "drivers", "etc", "hosts")
	}
	return "/etc/hosts"
}

// buildHostsBlock returns the lines of the clawdiator block. Each host gets an IPv4 line and an IPv6 line.
// Without the IPv6 line, a program can get an IPv6 address from DNS.
func buildHostsBlock(hosts []string, isLineEndAdded bool) []string {
	begin := hostsBeginMarker + ". The clawdiator service changes the lines up to the END line."
	if isLineEndAdded {
		begin += " " + hostsLineEndNote
	}
	lines := []string{begin}
	for _, host := range hosts {
		lines = append(lines, "0.0.0.0 "+host, ":: "+host)
	}
	return append(lines, hostsEndMarker)
}

// replaceHostsBlock removes the clawdiator block from the content of a hosts file.
// Then it adds a new block with the hosts at the end. With no hosts, it adds no block.
// The other lines and their line ends stay the same.
func replaceHostsBlock(content string, hosts []string) (string, error) {
	lineEnd := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnd = "\r\n"
	}
	var kept []string
	isInBlock, isLineEndAdded := false, false
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, hostsBeginMarker):
			if isInBlock {
				return "", errorf(msgHostsNoEnd)
			}
			isInBlock = true
			isLineEndAdded = strings.Contains(trimmed, hostsLineEndNote)
		case strings.HasPrefix(trimmed, hostsEndMarker):
			if !isInBlock {
				return "", errorf(msgHostsNoBegin)
			}
			isInBlock = false
		case !isInBlock:
			kept = append(kept, line)
		}
	}
	if isInBlock {
		return "", errorf(msgHostsNoEnd)
	}
	result := strings.Join(kept, "")
	if isLineEndAdded {
		result = strings.TrimSuffix(strings.TrimSuffix(result, "\n"), "\r")
	}
	if len(hosts) == 0 {
		return result, nil
	}
	isNoFinalLineEnd := result != "" && !strings.HasSuffix(result, "\n")
	if isNoFinalLineEnd {
		result += lineEnd
	}
	for _, line := range buildHostsBlock(hosts, isNoFinalLineEnd) {
		result += line + lineEnd
	}
	return result, nil
}

// writeHostsFile sets the clawdiator block of the hosts file. It writes only if the content changes.
// It writes a temporary file in the same folder and renames it, so a failure cannot leave half a file.
// On macOS, /etc/hosts is a link to /private/etc/hosts, so the function writes the target of the link.
func writeHostsFile(path string, hosts []string) (bool, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return false, err
	}
	updated, err := replaceHostsBlock(string(data), hosts)
	if err != nil || updated == string(data) {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".hosts.clawdiator-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(updated); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil && runtime.GOOS != "windows" {
		temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(temporary.Name(), target)
}

// getDNSFlushCommands returns the commands that flush the DNS cache of the system.
// A command can be missing, for example nscd on most Linux systems. The caller ignores such errors.
func getDNSFlushCommands(goos string) [][]string {
	switch goos {
	case "darwin":
		return [][]string{{"dscacheutil", "-flushcache"}, {"killall", "-HUP", "mDNSResponder"}}
	case "linux":
		return [][]string{{"resolvectl", "flush-caches"}, {"nscd", "-i", "hosts"}}
	case "windows":
		return [][]string{{"ipconfig", "/flushdns"}}
	}
	return nil
}

// getFileStamp returns a text that changes with each write of the file: the size and the time of the last change in nanoseconds.
// The service compares it with the stamp after its own write, so it finds a change of the hosts file by another program.
// An error gives an empty text.
func getFileStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}
