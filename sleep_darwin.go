//go:build darwin

package main

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// watchSleep registers for the messages of IOKit about sleep. The main binary has no cgo, so purego calls IOKit.
// Before a sleep, it sends a request to the loop and waits until the loop applied the pre-block. Then it allows the sleep.
// After the wake, it sends a value on the second channel. If the registration fails, the result has nil channels and the error.
// The registration runs on its own thread of the system, because the messages come through the run loop of that thread.
func watchSleep() (<-chan sleepRequest, <-chan struct{}, error) {
	requests := make(chan sleepRequest)
	wakes := make(chan struct{}, 1)
	ready := make(chan error, 1)
	go func() {
		// The run loop of CoreFoundation belongs to 1 thread of the system, so this goroutine keeps its thread.
		runtime.LockOSThread()
		run, err := registerSleepMessages(requests, wakes)
		ready <- err
		if err == nil {
			run()
		}
	}()
	if err := <-ready; err != nil {
		return nil, nil, err
	}
	return requests, wakes, nil
}

// registerSleepMessages loads IOKit and CoreFoundation and registers the callback. The result runs the run loop. It never returns.
func registerSleepMessages(requests chan<- sleepRequest, wakes chan<- struct{}) (func(), error) {
	iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	coreFoundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	var (
		ioRegisterForSystemPower           func(refcon uintptr, port *uintptr, callback uintptr, notifier *uint32) uint32
		ioNotificationPortGetRunLoopSource func(port uintptr) uintptr
		ioAllowPowerChange                 func(kernelPort uint32, notificationID uintptr) int32
		cfRunLoopGetCurrent                func() uintptr
		cfRunLoopAddSource                 func(loop, source, mode uintptr)
		cfRunLoopRun                       func()
	)
	purego.RegisterLibFunc(&ioRegisterForSystemPower, iokit, "IORegisterForSystemPower")
	purego.RegisterLibFunc(&ioNotificationPortGetRunLoopSource, iokit, "IONotificationPortGetRunLoopSource")
	purego.RegisterLibFunc(&ioAllowPowerChange, iokit, "IOAllowPowerChange")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, coreFoundation, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopAddSource, coreFoundation, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&cfRunLoopRun, coreFoundation, "CFRunLoopRun")
	// kCFRunLoopDefaultMode is a global variable. Dlsym gives its address, so read the value at the address.
	// The variable is in the memory of the system library, so the Go runtime never moves it.
	modeAddress, err := purego.Dlsym(coreFoundation, "kCFRunLoopDefaultMode")
	if err != nil {
		return nil, err
	}
	mode := *(*uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(&modeAddress)))

	var rootPort atomic.Uint32
	allow := func(argument uintptr) { ioAllowPowerChange(rootPort.Load(), argument) }
	callback := purego.NewCallback(func(refcon, service, messageType, argument uintptr) {
		// natural_t has 32 bits. The upper bits of the register can hold other data.
		handlePowerMessage(uint32(messageType), argument, allow, requests, wakes)
	})
	var port uintptr
	var notifier uint32
	root := ioRegisterForSystemPower(0, &port, callback, &notifier)
	if root == 0 {
		return nil, fmt.Errorf("IORegisterForSystemPower failed")
	}
	rootPort.Store(root)
	cfRunLoopAddSource(cfRunLoopGetCurrent(), ioNotificationPortGetRunLoopSource(port), mode)
	return cfRunLoopRun, nil
}
