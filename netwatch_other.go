//go:build !darwin && !linux

package main

import "context"

// watchNetwork returns nil on this system, so the 2-second poll of the service does the work.
// A later version adds NotifyIpInterfaceChange for Windows.
func watchNetwork(context.Context) <-chan struct{} {
	return nil
}
