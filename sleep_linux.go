package main

import (
	"os"

	"github.com/godbus/dbus/v5"
)

// watchSleep listens to the signal PrepareForSleep of logind on the system bus. A delay lock of logind holds the sleep
// until the loop applied the pre-block. logind waits for the lock at most InhibitDelayMaxSec, by default 5 seconds.
// If the system bus or logind is missing, the result has nil channels and the error. Then the tick finds a wake from the gap.
func watchSleep() (<-chan sleepRequest, <-chan struct{}, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, nil, err
	}
	err = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.login1.Manager"), dbus.WithMatchMember("PrepareForSleep"))
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	manager := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1")
	lock, err := takeSleepLock(manager)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	requests, wakes := make(chan sleepRequest), make(chan struct{}, 1)
	go func() {
		for signal := range signals {
			if signal.Name != "org.freedesktop.login1.Manager.PrepareForSleep" || len(signal.Body) != 1 {
				continue
			}
			isSleep, isBool := signal.Body[0].(bool)
			if !isBool {
				continue
			}
			release := func() {
				if lock != nil {
					lock.Close()
					lock = nil
				}
			}
			take := func() {
				if lock == nil {
					lock, _ = takeSleepLock(manager)
				}
			}
			handlePrepareForSleep(isSleep, requests, wakes, release, take)
		}
	}()
	return requests, wakes, nil
}

// takeSleepLock gets a delay lock of logind for sleep. The lock is a file descriptor: its close releases the lock.
func takeSleepLock(manager dbus.BusObject) (*os.File, error) {
	var descriptor dbus.UnixFD
	err := manager.Call("org.freedesktop.login1.Manager.Inhibit", 0, "sleep", appName, "the pre-block before a sleep", "delay").Store(&descriptor)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(descriptor), "sleep-lock"), nil
}
