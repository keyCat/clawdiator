package main

import (
	"sync/atomic"
	"time"
)

// sleepBlockTimeout is the maximum wait of the system for the pre-block before a sleep.
// A missing answer delays the sleep, so the service allows the sleep after this time also without the block.
const sleepBlockTimeout = 2 * time.Second

// The messages of IOKit about the power of the system. iokit_common_msg(x) is 0xe0000000 | x.
const (
	ioMessageCanSystemSleep     = 0xe0000270
	ioMessageSystemWillSleep    = 0xe0000280
	ioMessageSystemHasPoweredOn = 0xe0000300
)

// sleepBlockTimeouts counts the answers to a sleep after the timeout, without the block. The callback runs on another thread,
// so it cannot write the log. The loop writes a log line when the count increases.
var sleepBlockTimeouts atomic.Int64

// sleepRequest asks the loop of the service to block before a sleep. The loop closes Done after it applied the block.
type sleepRequest struct {
	Done chan struct{}
}

// requestSleepBlock sends a request to the loop and waits until the loop applied the block, at most sleepBlockTimeout.
// The result tells if the block is on.
func requestSleepBlock(requests chan<- sleepRequest) bool {
	request := sleepRequest{Done: make(chan struct{})}
	timeout := time.NewTimer(sleepBlockTimeout)
	defer timeout.Stop()
	select {
	case requests <- request:
	case <-timeout.C:
		return false
	}
	select {
	case <-request.Done:
		return true
	case <-timeout.C:
		return false
	}
}

// handlePowerMessage handles 1 message of IOKit about the power of the system. allow answers a message that needs an answer.
// Before a sleep, it asks the loop for the block. The system waits for the answer up to 30 seconds,
// so the answer comes on each path: after the block, after the timeout, and after a panic.
func handlePowerMessage(messageType uint32, argument uintptr, allow func(uintptr), requests chan<- sleepRequest, wakes chan<- struct{}) {
	switch messageType {
	case ioMessageCanSystemSleep:
		allow(argument)
	case ioMessageSystemWillSleep:
		defer allow(argument)
		defer func() { _ = recover() }()
		if !requestSleepBlock(requests) {
			sleepBlockTimeouts.Add(1)
		}
	case ioMessageSystemHasPoweredOn:
		signalWithoutWait(wakes)
	}
}
