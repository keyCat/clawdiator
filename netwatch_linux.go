package main

import (
	"context"
	"syscall"
	"time"
)

// The groups of the netlink messages, from linux/rtnetlink.h. The syscall package does not define them.
const (
	rtmgrpLink       = 0x1
	rtmgrpIPv4Ifaddr = 0x10
	rtmgrpIPv6Ifaddr = 0x100
)

// watchNetwork returns a channel that gets a value soon after a change of an interface or of an address.
// It reads the messages of a netlink socket for the groups of the links and of the IPv4 and IPv6 addresses.
// If the socket does not open, the result is nil, and the 2-second poll of the service does the work.
func watchNetwork(ctx context.Context) <-chan struct{} {
	socket, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return nil
	}
	groups := uint32(rtmgrpLink | rtmgrpIPv4Ifaddr | rtmgrpIPv6Ifaddr)
	if err := syscall.Bind(socket, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: groups}); err != nil {
		syscall.Close(socket)
		return nil
	}
	messages := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, 8192)
		for {
			count, err := syscall.Read(socket, buffer)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(time.Second)
				continue
			}
			if count > 0 {
				signalWithoutWait(messages)
			}
		}
	}()
	return debounceEvents(ctx, messages, networkEventDelay)
}
