//go:build darwin

package main

import (
	"slices"
	"testing"
	"time"

	"github.com/ebitengine/purego"
)

// The real callback of purego must work when C calls it, also when the Go function waits for the loop.
// The upper 32 bits of the type of the message can hold other data, so the callback must ignore them.
func TestPuregoCallbackWaitsForLoop(t *testing.T) {
	answers := make(chan uintptr, 4)
	requests := make(chan sleepRequest)
	wakes := make(chan struct{}, 1)
	callback := purego.NewCallback(func(refcon, service, messageType, argument uintptr) {
		handlePowerMessage(uint32(messageType), argument, func(value uintptr) { answers <- value }, requests, wakes)
	})
	var call func(refcon, service, messageType, argument uintptr)
	purego.RegisterFunc(&call, callback)

	loopDone := make(chan time.Time, 1)
	go func() {
		request := <-requests
		time.Sleep(100 * time.Millisecond)
		loopDone <- time.Now()
		close(request.Done)
	}()
	call(0, 0, 0xdeadbeef_e0000280, 42)
	answered := time.Now()
	select {
	case value := <-answers:
		if value != 42 {
			t.Errorf("the answer must carry the argument: %d", value)
		}
	default:
		t.Fatal("the callback must answer the system")
	}
	if done := <-loopDone; answered.Before(done) {
		t.Error("the answer must come after the block of the loop")
	}
	call(0, 0, ioMessageSystemHasPoweredOn, 0)
	if len(wakes) != 1 || len(answers) != 0 {
		t.Errorf("a wake must send 1 value and need no answer: %d, %v", len(wakes), slices.Collect(func(yield func(uintptr) bool) {}))
	}
}
