package main

import (
	"context"
	"time"
)

// networkEventDelay is the wait after the last network message. A change of the network, for example a VPN
// that connects, gives several messages in a short time. The service reads the fingerprint 1 time after them.
const networkEventDelay = 300 * time.Millisecond

// debounceEvents sends 1 value when the input stays quiet for the delay after a value.
// The output has a buffer of 1, so a slow reader gets 1 value for many changes.
func debounceEvents(ctx context.Context, input <-chan struct{}, delay time.Duration) <-chan struct{} {
	output := make(chan struct{}, 1)
	go func() {
		timer := time.NewTimer(delay)
		timer.Stop()
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case _, isOpen := <-input:
				if !isOpen {
					return
				}
				timer.Reset(delay)
			case <-timer.C:
				select {
				case output <- struct{}{}:
				default:
				}
			}
		}
	}()
	return output
}

// signalWithoutWait sends a value to a channel with a buffer, if the buffer is empty. It never waits.
func signalWithoutWait(channel chan<- struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
