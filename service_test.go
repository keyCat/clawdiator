package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"geocheck"
)

// fakeNetwork replaces the lookups of the driver. Probes reply at once. Each full check waits for the test.
type fakeNetwork struct {
	probe geocheck.Location
	// checks gets 1 started full check at a time. The test answers on its reply channel.
	checks chan *fakeCheck
}

type fakeCheck struct {
	ctx     context.Context
	options geocheck.Options
	reply   chan geocheck.Result
	// fail makes the full check fail with the error.
	fail chan error
}

func (n *fakeNetwork) lookup(ctx context.Context, options geocheck.Options) (geocheck.Result, error) {
	if len(options.Services) == 1 && options.MinReplies == 1 {
		return geocheck.Result{Locations: []geocheck.Location{n.probe}}, nil
	}
	check := &fakeCheck{ctx: ctx, options: options, reply: make(chan geocheck.Result, 1), fail: make(chan error, 1)}
	n.checks <- check
	select {
	case result := <-check.reply:
		return result, nil
	case err := <-check.fail:
		return geocheck.Result{}, err
	case <-ctx.Done():
		return geocheck.Result{}, ctx.Err()
	}
}

// testDriver is a driver with a fake clock, a fake network and a fake hosts file.
type testDriver struct {
	*serviceDriver
	t           *testing.T
	clock       time.Time
	fingerprint string
	network     *fakeNetwork
	hostsWrites [][]string
	hostsErr    error
	flushes     int
	// checkResults keeps the results of full checks that arrived during a step.
	checkResults []checkDone
	// stop ends the context of the driver, so that a running loop stops.
	stop context.CancelFunc
}

func newTestDriver(t *testing.T) *testDriver {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	options, err := getGeoOptions(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	td := &testDriver{t: t, clock: testStart, fingerprint: "wifi", network: &fakeNetwork{probe: germany, checks: make(chan *fakeCheck, 4)}}
	td.stop = cancel
	td.serviceDriver = newServiceDriver(ctx, newCore(&serviceState{}, nil), newAppPaths(t.TempDir()), options)
	td.now = func() time.Time { return td.clock }
	td.lookup = td.network.lookup
	td.getFingerprint = func() string { return td.fingerprint }
	td.writeHosts = func(hosts []string) (bool, error) {
		if td.hostsErr != nil {
			return false, td.hostsErr
		}
		isChanged := len(td.hostsWrites) == 0 || !slices.Equal(td.hostsWrites[len(td.hostsWrites)-1], hosts)
		td.hostsWrites = append(td.hostsWrites, hosts)
		return isChanged, nil
	}
	td.flushDNS = func() string { td.flushes++; return "ok" }
	if err := os.MkdirAll(td.paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return td
}

// step is 1 tick of the loop. Then it handles the probe results that arrive in a short time, the same as the loop.
func (td *testDriver) step() {
	td.t.Helper()
	done := make(chan struct{})
	go func() {
		td.handleTick()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		td.t.Fatal("a tick must not wait for the network")
	}
	td.drainProbes()
	td.afterStep(false)
	td.clock = td.clock.Add(tickInterval)
}

// drainProbes handles the probe results. It keeps the results of full checks for the test.
func (td *testDriver) drainProbes() {
	for {
		select {
		case result := <-td.results:
			if done, isCheck := result.(checkDone); isCheck {
				td.checkResults = append(td.checkResults, done)
				continue
			}
			td.handleResult(result)
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func (td *testDriver) nextCheck() *fakeCheck {
	td.t.Helper()
	select {
	case check := <-td.network.checks:
		return check
	case <-time.After(time.Second):
		td.t.Fatal("no full check started")
	}
	return nil
}

func (td *testDriver) waitCheckResult() checkDone {
	td.t.Helper()
	if len(td.checkResults) > 0 {
		done := td.checkResults[0]
		td.checkResults = td.checkResults[1:]
		return done
	}
	for {
		select {
		case result := <-td.results:
			if done, isCheck := result.(checkDone); isCheck {
				return done
			}
			td.handleResult(result)
		case <-time.After(time.Second):
			td.t.Fatal("no result of the full check")
		}
	}
}

// The loop must not wait for a full check. Otherwise the next tick counts as a wake, and the pre-block never ends.
func TestDriverDoesNotWaitForSlowFullCheck(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	check := td.nextCheck()
	if check.options.Rounds != preBlockRounds || !slices.Contains(geocheck.ServiceNames(check.options.Services), probeServiceName) {
		t.Errorf("the full check options = %+v", check.options)
	}
	for second := 2; second <= 40; second += 2 {
		td.step()
	}
	check.reply <- newResult("DE", "DE", "DE")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	if td.core.state.IsPreBlocked || len(td.core.state.blockedProviders()) != 0 {
		t.Errorf("the result of a slow full check must apply: %v", getEventKinds(td.core.state))
	}
	if got := getEventKinds(td.core.state); !slices.Equal(got, []eventKind{eventPreBlock, eventPreBlockEnd}) {
		t.Errorf("a slow full check must not count as a wake: %v", got)
	}
	if last := td.hostsWrites[len(td.hostsWrites)-1]; len(last) != 0 || td.flushes != 2 {
		t.Errorf("the block must go, with 2 flushes of the DNS cache: %v, %d", last, td.flushes)
	}
	if len(td.hostsWrites[0]) != len(getBlockedHosts(geocheck.Providers())) {
		t.Errorf("the pre-block must write the hosts of both providers: %d", len(td.hostsWrites[0]))
	}
}

func TestDriverNewNetworkStopsOldFullCheck(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	old := td.nextCheck()
	td.fingerprint = "hotel"
	td.step()
	current := td.nextCheck()
	select {
	case <-old.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("a new generation must stop the old full check")
	}
	if stale := td.waitCheckResult(); stale.Err == nil {
		t.Errorf("the stopped check must end with an error: %+v", stale)
	} else {
		td.handleResult(stale)
	}
	if !td.core.state.IsPreBlocked {
		t.Fatal("the stopped check must not change the state")
	}
	current.reply <- newResult("CN", "CN", "CN")
	td.handleResult(td.waitCheckResult())
	if !slices.Equal(td.core.state.blockedProviders(), geocheck.Providers()) || td.core.state.IsPreBlocked {
		t.Errorf("the current check must apply: %v", td.core.state.blockedProviders())
	}
}

func TestDriverUserCheck(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	first := td.nextCheck()
	if response := td.handleRequest(commandRequest{Command: socketCheck}); response.Code != codeOK {
		t.Errorf("the first user check = %+v", response)
	}
	select {
	case extra := <-td.network.checks:
		t.Errorf("a user check during a running full check must start no second check: %+v", extra.options)
	case <-time.After(100 * time.Millisecond):
	}
	if response := td.handleRequest(commandRequest{Command: socketCheck}); response.Code != codeTooSoon {
		t.Errorf("a second user check in 30 seconds = %+v", response)
	}
	first.reply <- newResult("DE")
	td.handleResult(td.waitCheckResult())
	td.clock = td.clock.Add(userCheckInterval)
	if response := td.handleRequest(commandRequest{Command: socketCheck}); response.Code != codeOK {
		t.Errorf("a user check after 30 seconds = %+v", response)
	}
	td.nextCheck()
}

func TestDriverPauseRequests(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	td.nextCheck()
	if response := td.handleRequest(commandRequest{Command: socketPause, Until: td.clock.Add(-time.Minute)}); response.Code != codeBadRequest {
		t.Errorf("a pause with an end in the past = %+v", response)
	}
	if response := td.handleRequest(commandRequest{Command: socketPause, IsForever: true, User: "ivan"}); response.Code != codeOK {
		t.Fatalf("a pause with no end = %+v", response)
	}
	td.afterStep(false)
	if last := td.hostsWrites[len(td.hostsWrites)-1]; len(last) != 0 {
		t.Errorf("a pause must remove the block at once: %v", last)
	}
	if response := td.handleRequest(commandRequest{Command: socketResume}); response.Code != codeOK {
		t.Errorf("resume = %+v", response)
	}
	if response := td.handleRequest(commandRequest{Command: socketResume}); response.Code != codeNoPause {
		t.Errorf("resume without a pause = %+v", response)
	}
	td.afterStep(false)
	if last := td.hostsWrites[len(td.hostsWrites)-1]; len(last) == 0 {
		t.Error("after the resume, the pre-block must apply")
	}
}

func TestDriverHostsErrorAddsOneEvent(t *testing.T) {
	td := newTestDriver(t)
	td.hostsErr = errors.New("read-only file system")
	td.step()
	td.step()
	var errorEvents int
	for _, change := range td.core.state.Events {
		if change.Kind == eventError {
			errorEvents++
		}
	}
	if errorEvents != 1 {
		t.Errorf("the same error must add 1 event, not %d", errorEvents)
	}
	td.hostsErr = nil
	td.step()
	if len(td.hostsWrites) != 1 || td.flushes != 1 {
		t.Errorf("after the error, the next pass must write the block: %d writes, %d flushes", len(td.hostsWrites), td.flushes)
	}
}

func TestDriverSavesStateAndLog(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	saved, err := loadServiceState(td.paths.State)
	if err != nil || !saved.IsPreBlocked || saved.LastEventSeq != 1 {
		t.Fatalf("the first pass must save the pre-block: %+v, %v", saved, err)
	}
	log, _ := os.ReadFile(td.paths.Log)
	if !strings.Contains(string(log), "all pre-block") {
		t.Errorf("the log must have the pre-block: %q", log)
	}
	firstSave := td.lastSave
	td.step()
	if !td.lastSave.Equal(firstSave) {
		t.Error("a pass without a change must not save before 30 seconds")
	}
	for td.clock.Sub(firstSave) < saveInterval {
		td.step()
	}
	td.step()
	if !td.lastSave.After(firstSave) {
		t.Error("the service must save the heartbeat at least every 30 seconds")
	}
}

// The block must be in the hosts file before the check of the new network starts.
func TestDriverAppliesBlockBeforeCheck(t *testing.T) {
	td := newTestDriver(t)
	var order []string
	writeHosts := td.writeHosts
	td.writeHosts = func(hosts []string) (bool, error) {
		order = append(order, "hosts")
		return writeHosts(hosts)
	}
	td.spawn = func(task func()) {
		order = append(order, "start")
		go task()
	}
	td.step()
	td.nextCheck()
	if len(order) < 2 || order[0] != "hosts" {
		t.Errorf("the pre-block must come before the start of the check: %v", order)
	}
}

// A failed full check must write its reason to the log, so that a long pre-block has a visible cause.
func TestDriverLogsFailedCheck(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	check := td.nextCheck()
	check.fail <- errors.New("2 of 4 location services replied, but the minimum is 3 (cloudflare: DE; country.is: DE; ipinfo.io: HTTP 429; 2ip.io: timeout)")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	log, _ := os.ReadFile(td.paths.Log)
	if !strings.Contains(string(log), "all check-failed 2 of 4 location services replied") || !strings.Contains(string(log), "ipinfo.io: HTTP 429") {
		t.Errorf("the log must have the failed check with the reply of each service:\n%s", log)
	}
	if !strings.Contains(td.core.state.LastFailure.Error, "ipinfo.io: HTTP 429") || !td.core.state.IsPreBlocked {
		t.Errorf("the state must keep the failure and the pre-block: %+v", td.core.state.LastFailure)
	}

	// A stopped check of an older generation is not a failure.
	td.fingerprint = "hotel"
	td.step()
	old := td.nextCheck()
	td.fingerprint = "hotel-2"
	td.step()
	td.nextCheck()
	<-old.ctx.Done()
	td.handleResult(td.waitCheckResult())
	log, _ = os.ReadFile(td.paths.Log)
	if strings.Count(string(log), "check-failed") != 1 {
		t.Errorf("a stopped check must not add a failure to the log:\n%s", log)
	}
}

// The verbose log must show each step with its reason, its result and its duration.
func TestDriverVerboseLog(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	check := td.nextCheck()
	check.fail <- errors.New("2 of 4 location services replied (ipinfo.io: HTTP 429)")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	td.handleRequest(commandRequest{Command: socketCheck, User: "ivan"})
	data, err := os.ReadFile(td.paths.DebugLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{
		"2026-10-14T09:00:00.000Z core network fingerprint \"wifi\"",
		"core generation 1: service start",
		"check start generation 1, 1 rounds, reason: service start",
		"hosts blocked providers: Anthropic, OpenAI (41 hosts). The file changed: true",
		"2026-10-14T09:00:00.000Z all pre-block the network changed",
		"dns flush: ok",
		"probe generation 1 in",
		"country DE",
		"check generation 1 failed in",
		"ipinfo.io: HTTP 429",
		"core failed full checks in a row: 1. The next retry is in 5s. Pre-block: true",
		"all pre-block the network changed",
		"command check from user \"ivan\": ok",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the verbose log must contain %q:\n%s", want, log)
		}
	}
	normal, _ := os.ReadFile(td.paths.Log)
	if strings.Contains(string(normal), "probe generation") || !strings.Contains(string(normal), "all pre-block") {
		t.Errorf("the normal log must have only the events and the failed checks:\n%s", normal)
	}
}
