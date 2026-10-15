//go:build !darwin

package main

import "errors"

// watchSleep has no messages about sleep on this system yet. The tick finds a wake from the gap between 2 passes.
func watchSleep() (<-chan sleepRequest, <-chan struct{}, error) {
	return nil, nil, errors.New("no messages about sleep on this system")
}
