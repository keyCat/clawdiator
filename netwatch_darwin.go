package main

import (
	"context"
	"syscall"
	"time"
)

// watchNetwork returns a channel that gets a value at once after a change of an interface or of an address.
// It reads the messages of a route socket. The socket needs no administrator rights.
// The channel has a buffer of 1, so a burst of messages gives at most 2 passes. The core waits for quiet before the full check.
// If the socket does not open, the result is nil, and the tick of the service does the work.
func watchNetwork(ctx context.Context) <-chan struct{} {
	socket, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return nil
	}
	messages := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := syscall.Read(socket, buffer)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(time.Second)
				continue
			}
			// Each message starts with its length (2 bytes), the version (1 byte) and the type (1 byte).
			if count >= 4 && isInterfaceMessage(buffer[3]) {
				signalWithoutWait(messages)
			}
		}
	}()
	return messages
}

// isInterfaceMessage tells if the route message is about an interface or an address.
// The messages about single routes come often, for example for the ARP table, so the service ignores them.
func isInterfaceMessage(kind byte) bool {
	switch kind {
	case syscall.RTM_NEWADDR, syscall.RTM_DELADDR, syscall.RTM_IFINFO, syscall.RTM_IFINFO2:
		return true
	}
	return false
}
