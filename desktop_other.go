//go:build !darwin

package main

import "errors"

// errNoDesktop tells that this version has no notices and no tray actions on this system. A later version adds them.
var errNoDesktop = errors.New("this version has no desktop functions on this system")

func showNotice(string) error {
	return errNoDesktop
}

func runAsAdministrator(string, ...string) error {
	return errNoDesktop
}

func openTerminal(string) error {
	return errNoDesktop
}

func openLogFile(string) error {
	return errNoDesktop
}

func openURL(string) error {
	return errNoDesktop
}
