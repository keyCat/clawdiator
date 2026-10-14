//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

// getSystemLanguage returns the first preferred language of macOS. On Linux, it returns an empty string.
func getSystemLanguage() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	output, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	return parseAppleLanguages(string(output))
}
