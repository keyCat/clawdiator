package main

// signalWithoutWait sends a value to a channel with a buffer, if the buffer is empty. It never waits.
func signalWithoutWait(channel chan<- struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
