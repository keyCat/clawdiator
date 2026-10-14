//go:build !windows

package main

import (
	"os"
	"syscall"
)

// getConsoleUserID returns the user ID of the user at the screen. The owner of /dev/console is this user.
// The value 0 means the login window: then no user session runs. A negative value means an error.
func getConsoleUserID() int {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return -1
	}
	if stat, isStat := info.Sys().(*syscall.Stat_t); isStat {
		return int(stat.Uid)
	}
	return -1
}
