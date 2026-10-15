package main

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"geocheck"
)

// A message about a change of the network must start a pass at once, without a wait for the next tick.
func TestNetworkMessageStartsPassAtOnce(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	check := td.nextCheck()
	check.reply <- newResult("DE", "DE", "DE")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	if len(td.core.state.blockedProviders()) != 0 {
		t.Fatal("the setup must give no block")
	}
	writes := make(chan []string, 8)
	td.writeHosts = func(hosts []string) (bool, error) {
		writes <- hosts
		return true, nil
	}
	changes := make(chan struct{}, 1)
	stopped := make(chan struct{})
	go func() {
		// The ticks channel is nil, so only the network message can start a pass.
		td.run(loopInputs{networkChanges: changes})
		close(stopped)
	}()
	td.fingerprint = "hotel"
	changes <- struct{}{}
	select {
	case hosts := <-writes:
		// The pre-block blocks both providers. Do not read the state here, because the loop runs in another goroutine.
		if len(hosts) != len(getBlockedHosts(geocheck.Providers())) {
			t.Errorf("the pass must write the pre-block: %d hosts", len(hosts))
		}
	case <-time.After(time.Second):
		t.Fatal("a network message must start a pass at once")
	}
	// A second message without a change of the network writes no log line.
	changes <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	td.stop()
	<-stopped
	log, _ := os.ReadFile(td.paths.DebugLog)
	if count := strings.Count(string(log), "a message of the system changed the network: the pass with the block took"); count != 1 {
		t.Errorf("only the message with a change of the network must write a log line, not %d:\n%s", count, log)
	}
}

func TestWatchNetworkOpens(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("only macOS and Linux have the watcher now")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if watchNetwork(ctx) == nil {
		t.Error("the watcher must open its socket without administrator rights")
	}
}
