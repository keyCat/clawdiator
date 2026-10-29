package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"clawdiator/internal/geocheck"
)

// testStep is the time of 1 step of the test driver. It is longer than tickInterval, because each step waits
// for the results of the probes. The core and the driver use time differences, so the length of a step does not change the logic.
const testStep = 2 * time.Second

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
	// isWaitForAll tells if OnReply asked to wait for all services. The real lookup then stops no service.
	isWaitForAll bool
}

func (n *fakeNetwork) lookup(ctx context.Context, options geocheck.Options) (geocheck.Result, error) {
	// Only the full check sets OnReply. A full check with 1 service and the minimum 1 has the same services as the probe.
	if options.OnReply == nil {
		return geocheck.Result{Locations: []geocheck.Location{n.probe}}, nil
	}
	check := &fakeCheck{ctx: ctx, options: options, reply: make(chan geocheck.Result, 1), fail: make(chan error, 1)}
	n.checks <- check
	select {
	case result := <-check.reply:
		// The real lookup calls OnReply for each correct reply when it comes.
		for _, location := range result.Locations {
			if options.OnReply != nil && options.OnReply(location) {
				check.isWaitForAll = true
			}
		}
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
	// hostsFileStamp is the stamp of the fake hosts file. isHostsTampered makes the next write report a change.
	hostsFileStamp  string
	isHostsTampered bool
	flushes         int
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
		isChanged := len(td.hostsWrites) == 0 || !slices.Equal(td.hostsWrites[len(td.hostsWrites)-1], hosts) || td.isHostsTampered
		td.isHostsTampered = false
		// A real write changes the stamp of the file, so the fake write does it too.
		td.hostsFileStamp += "w"
		td.hostsWrites = append(td.hostsWrites, hosts)
		return isChanged, nil
	}
	td.flushDNS = func() string { td.flushes++; return "ok" }
	// The flush runs in the goroutine of the test, so the test can count the flushes without a race.
	td.runFlush = func(task func()) { task() }
	// The tests never read the real hosts file. A test sets a new stamp to show a change by another program.
	td.hostsStamp = func() string { return td.hostsFileStamp }
	// The tests never use the network. A test of the local database replaces this function.
	td.downloadDB = func(context.Context, string) (*countryDB, error) { return nil, errOffline }
	td.update = updater{
		clean:   func() {},
		latest:  func(context.Context) (string, error) { return "", errOffline },
		prepare: func(context.Context, string) (updateInfo, error) { return updateInfo{}, errOffline },
		start:   func(string) error { return errOffline },
	}
	// The tests never change pf. A test of the firewall sets a fake pfctl.
	td.firewall = nil
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
	td.clock = td.clock.Add(testStep)
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

// changeNetwork changes the fingerprint. The pre-block must start at the next step,
// and the full check must start only at the step after it, when the network is quiet.
func (td *testDriver) changeNetwork(fingerprint string) *fakeCheck {
	td.t.Helper()
	td.fingerprint = fingerprint
	td.step()
	if !td.core.state.IsPreBlocked && !td.core.state.Pause.IsOn {
		td.t.Fatal("a change of the network must start the pre-block at once")
	}
	select {
	case early := <-td.network.checks:
		td.t.Fatalf("the full check must wait until the network is quiet: %+v", early.options)
	default:
	}
	td.step()
	return td.nextCheck()
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
	current := td.changeNetwork("hotel")
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
	old := td.changeNetwork("hotel")
	td.changeNetwork("hotel-2")
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
		"dns flush in",
		"probe generation 1 in",
		"cloudflare: DE",
		"check generation 1 failed in",
		"ipinfo.io: HTTP 429",
		"core failed full checks in a row: 1. The next retry is in 1s. Pre-block: true",
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

// With the local database, the online services must give 1 reply less. The probe and the full check both use the database.
func TestDriverUsesLocalDB(t *testing.T) {
	td := newTestDriver(t)
	td.setCountryDB(newTestDB(t))
	td.network.probe = geocheck.Location{Country: "DE", IP: "198.51.100.7", Source: probeServiceName}
	td.step()
	check := td.nextCheck()
	if check.options.MinReplies != 1 || td.checkOptions.MinReplies != 2 {
		t.Errorf("the full check must ask for 1 online reply less: %d", check.options.MinReplies)
	}
	check.reply <- geocheck.Result{Locations: []geocheck.Location{td.network.probe}}
	done := td.waitCheckResult()
	if done.Err != nil || done.Result.Describe() != "cloudflare: DE; dbip: DE" {
		t.Fatalf("the full check must add the opinion of the database: %q, %v", done.Result.Describe(), done.Err)
	}
	if check.isWaitForAll {
		t.Error("if all replies are allowed, the extra wait must stop the slow services")
	}
	td.handleResult(done)
	if td.core.state.IsPreBlocked || td.core.state.LocalDB.File != "dbip-country-lite-2026-10.mmdb" {
		t.Errorf("the full check must pass, and the state must name the file: %+v", td.core.state.LocalDB)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	for _, want := range []string{
		"local database: dbip-country-lite-2026-10.mmdb, built 2026-10-04",
		"probe generation 1 in 0s: cloudflare: DE; dbip: DE, address 198.51.100.7",
	} {
		if !strings.Contains(string(log), want) {
			t.Errorf("the verbose log must contain %q:\n%s", want, log)
		}
	}

	// The same address moves to China in the new file of the database. The next probe blocks at once, without a pre-block.
	td.network.probe.IP = "203.0.113.9"
	td.core.state.PublicIP = td.network.probe.IP
	for range probeInterval / testStep {
		td.step()
	}
	if !slices.Equal(td.core.state.blockedProviders(), geocheck.Providers()) || td.core.state.IsPreBlocked ||
		!slices.Equal(getEventKinds(td.core.state), []eventKind{eventPreBlock, eventPreBlockEnd, eventBlock}) {
		t.Errorf("the opinion of the database in the probe must block: %v", getEventKinds(td.core.state))
	}
}

// If cloudflare fails, the database gives no opinion. The first lookup can then stop a slow service that the full minimum needs.
// So the check asks the services again with the full minimum.
func TestDriverLocalDBWithoutAddress(t *testing.T) {
	td := newTestDriver(t)
	td.setCountryDB(newTestDB(t))
	td.step()
	first := td.nextCheck()
	first.reply <- geocheck.Result{
		Locations: []geocheck.Location{{Country: "DE", Region: "Hessen", Source: "ipwho.is"}},
		Failures:  []string{"cloudflare: HTTP 429"},
	}
	second := td.nextCheck()
	if second.options.MinReplies != 2 {
		t.Fatalf("the second lookup must ask for the full minimum: %d", second.options.MinReplies)
	}
	second.reply <- newResult("DE", "DE")
	done := td.waitCheckResult()
	if done.Err != nil || len(done.Result.Locations) != 2 || done.FirstErr == nil {
		t.Errorf("the check must pass with the second lookup: %+v", done)
	}
	td.handleResult(done)
	log, _ := os.ReadFile(td.paths.DebugLog)
	if !strings.Contains(string(log), "the local database gave no country, so the check asked the services again with the full minimum") {
		t.Errorf("the verbose log must show the second lookup:\n%s", log)
	}
}

// With a minimum of 1, the online services must still give 1 reply. A minimum of 0 would give the default of geocheck.
func TestDriverLocalDBWithMinimumOfOne(t *testing.T) {
	td := newTestDriver(t)
	td.setCountryDB(newTestDB(t))
	td.checkOptions.MinReplies = 1
	td.step()
	if check := td.nextCheck(); check.options.MinReplies != 1 {
		t.Errorf("the minimum of the online services = %d", check.options.MinReplies)
	}
}

func TestDriverIgnoresOldLocalDB(t *testing.T) {
	td := newTestDriver(t)
	td.setCountryDB(newTestDB(t))
	td.clock = td.countryDB.buildTime.Add(maxDBAge + time.Hour)
	td.network.probe = geocheck.Location{Country: "DE", IP: "198.51.100.7", Source: probeServiceName}
	td.step()
	if check := td.nextCheck(); check.options.MinReplies != 2 {
		t.Errorf("with old data, the full check must ask for all online replies: %d", check.options.MinReplies)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	if !strings.Contains(string(log), "local database: dbip-country-lite-2026-10.mmdb, built 2026-10-04, too old") || strings.Contains(string(log), "dbip: DE") {
		t.Errorf("the service must not use old data:\n%s", log)
	}
}

func TestDriverDownloadsLocalDB(t *testing.T) {
	td := newTestDriver(t)
	var months []string
	td.downloadDB = func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		return nil, errOffline
	}
	td.step()
	td.step()
	if !slices.Equal(months, []string{"2026-10"}) || td.countryDB != nil {
		t.Fatalf("the first pass must start 1 download: %v", months)
	}
	td.clock = td.clock.Add(dbRetryInterval)
	td.step()
	if len(months) != 2 {
		t.Fatalf("after a failure, the next download must start after %v: %v", dbRetryInterval, months)
	}

	db := newTestDB(t)
	td.downloadDB = func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		return db, nil
	}
	td.clock = td.clock.Add(dbRetryInterval)
	td.step()
	td.step()
	if td.countryDB != db || td.core.state.LocalDB.File != "dbip-country-lite-2026-10.mmdb" || len(months) != 3 {
		t.Fatalf("a passed download must give the new file: %v, %+v", months, td.core.state.LocalDB)
	}
	td.clock = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	td.step()
	if !slices.Equal(months[3:], []string{"2026-11"}) {
		t.Errorf("a new month must start a download of its file: %v", months)
	}

	// Without a file, the service takes the file of the month before, if DB-IP has not published the new file yet.
	td.setCountryDB(nil)
	td.clock = td.clock.Add(dbRetryInterval)
	months = nil
	td.downloadDB = func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		if month == "2026-11" {
			return nil, errDBNotPublished
		}
		return db, nil
	}
	td.step()
	td.step()
	if !slices.Equal(months, []string{"2026-11", "2026-10"}) || td.countryDB != db {
		t.Errorf("the service must download the file of the month before: %v", months)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	if !strings.Contains(string(log), "dbip the download of 2026-10 failed in") || !strings.Contains(string(log), "dbip the download passed in") {
		t.Errorf("the verbose log must show the downloads:\n%s", log)
	}
}

// getTestGeoOptions returns the options of the full check with the services, as the config gives them.
func getTestGeoOptions(t *testing.T, services ...string) geocheck.Options {
	t.Helper()
	options, err := getGeoOptions(&Config{GeoServices: services})
	if err != nil {
		t.Fatal(err)
	}
	return options
}

// newOpenDriverWithDB returns a test driver with the local database, after a full check in Germany.
func newOpenDriverWithDB(t *testing.T) *testDriver {
	t.Helper()
	td := newTestDriver(t)
	td.setCountryDB(newTestDB(t))
	td.network.probe = geocheck.Location{Country: "DE", IP: "198.51.100.7", Source: probeServiceName}
	td.step()
	td.nextCheck().reply <- geocheck.Result{Locations: []geocheck.Location{td.network.probe}}
	td.handleResult(td.waitCheckResult())
	if td.core.state.IsPreBlocked || len(td.core.state.blockedProviders()) != 0 {
		t.Fatalf("the setup must give no block: %v", getEventKinds(td.core.state))
	}
	return td
}

// The database gives a blocked country during a full check. The service blocks at once, before the end of the check.
// Then the check waits for all services, and the strict rule keeps the block, also if the online services allow the country.
func TestDriverLocalDBBlocksDuringCheck(t *testing.T) {
	td := newOpenDriverWithDB(t)
	td.handleRequest(commandRequest{Command: socketCheck})
	check := td.nextCheck()
	check.reply <- geocheck.Result{Locations: []geocheck.Location{{Country: "DE", IP: "203.0.113.9", Source: probeServiceName}}}
	done := td.waitCheckResult()
	if !slices.Equal(td.core.state.blockedProviders(), geocheck.Providers()) || !check.isWaitForAll {
		t.Fatalf("the opinion of the database must block before the end of the check: %v, %v", td.core.state.blockedProviders(), check.isWaitForAll)
	}
	td.handleResult(done)
	if !slices.Equal(td.core.state.blockedProviders(), geocheck.Providers()) || td.core.state.provider(geocheck.Anthropic).Verdict != "not_supported" {
		t.Errorf("the strict rule must keep the block: %+v", td.core.state.provider(geocheck.Anthropic))
	}
	if got := getEventKinds(td.core.state); !slices.Equal(got, []eventKind{eventPreBlock, eventPreBlockEnd, eventBlock}) {
		t.Errorf("the block must give 1 event: %v", got)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	if !strings.Contains(string(log), "the local database gives CN for 203.0.113.9. The rules do not allow it, so the service blocks at once and waits for all services") {
		t.Errorf("the verbose log must show the early block:\n%s", log)
	}
}

// The database gives no region. For a country with excluded regions, the check waits for all services, but it does not block early.
func TestDriverLocalDBWithoutRegionWaits(t *testing.T) {
	td := newOpenDriverWithDB(t)
	td.checkOptions = getTestGeoOptions(t, "cloudflare", "ipwho.is", "geojs.io")
	td.handleRequest(commandRequest{Command: socketCheck})
	check := td.nextCheck()
	check.reply <- geocheck.Result{Locations: []geocheck.Location{
		{Country: "UA", IP: "2001:db8:1::5", Source: probeServiceName},
		{Country: "UA", Region: "Kyiv City", Source: "ipwho.is"},
	}}
	done := td.waitCheckResult()
	if len(td.core.state.blockedProviders()) != 0 || !check.isWaitForAll {
		t.Fatalf("Ukraine without a region must wait for all services, but not block early: %v, %v", td.core.state.blockedProviders(), check.isWaitForAll)
	}
	td.handleResult(done)
	if len(td.core.state.blockedProviders()) != 0 {
		t.Errorf("the region of ipwho.is must allow Kyiv: %v", td.core.state.provider(geocheck.Anthropic))
	}
	if log, _ := os.ReadFile(td.paths.DebugLog); strings.Contains(string(log), "the local database gives UA") {
		t.Errorf("Ukraine without a region must not send an early block:\n%s", log)
	}

	// An online reply without a region also waits, if the database gives an allowed country.
	td.clock = td.clock.Add(userCheckInterval)
	td.handleRequest(commandRequest{Command: socketCheck})
	check = td.nextCheck()
	check.reply <- geocheck.Result{Locations: []geocheck.Location{{Country: "UA", IP: "198.51.100.7", Source: probeServiceName}}}
	td.waitCheckResult()
	if !check.isWaitForAll {
		t.Error("an online reply of Ukraine without a region must wait for all services")
	}
}

// An opinion of an older check must not block. The network changed after the start of that check.
func TestCoreIgnoresOldDBBlock(t *testing.T) {
	s := newOpenSimulation(t)
	s.c.handleDBBlock(s.now, s.c.generation, geocheck.Location{Country: "CN", Source: dbSource})
	if len(s.c.state.blockedProviders()) != 0 {
		t.Errorf("an opinion without a running check must not block: %v", s.c.state.blockedProviders())
	}
	check := mustFindCheck(t, s.c.requestCheck("test"))
	s.c.handleDBBlock(s.now, check.Generation-1, geocheck.Location{Country: "CN", Source: dbSource})
	s.c.handleDBBlock(s.now, check.Generation, geocheck.Location{Country: "UA", Source: dbSource})
	if len(s.c.state.blockedProviders()) != 0 {
		t.Errorf("an old opinion and a country without a region must not block: %v", s.c.state.blockedProviders())
	}
	s.c.handleDBBlock(s.now, check.Generation, geocheck.Location{Country: "AF", Source: dbSource})
	if got := s.c.state.blockedProviders(); !slices.Equal(got, []geocheck.Provider{geocheck.Anthropic}) {
		t.Errorf("Afghanistan must block only Anthropic: %v", got)
	}
}

// The firewall rules follow the block of Anthropic. The token is on the disk at once, so a crash cannot lose it.
func TestDriverFirewall(t *testing.T) {
	td := newTestDriver(t)
	pf := newFakePF()
	td.firewall = &pfFirewall{run: pf.run}
	td.step()
	if !td.core.state.Firewall.IsBlocked || len(pf.tokens) != 1 || !hasPFRules(pf.anchor) {
		t.Fatalf("the pre-block must turn the rules on: %+v, %+v", td.core.state.Firewall, pf)
	}
	if saved, err := loadServiceState(td.paths.State); err != nil || saved.Firewall.Token != pf.tokens[0] {
		t.Errorf("the token must be on the disk at once: %+v, %v", saved, err)
	}
	td.nextCheck().reply <- newResult("DE", "DE", "DE")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	if td.core.state.Firewall.IsBlocked || td.core.state.Firewall.Token != "" || pf.anchor != "" || len(pf.tokens) != 0 || pf.isEnabled {
		t.Errorf("an allowed country must remove the rules and release the token: %+v, %+v", td.core.state.Firewall, pf)
	}
	if saved, _ := loadServiceState(td.paths.State); saved.Firewall.Token != "" {
		t.Errorf("the released token must leave the disk at once: %+v", saved.Firewall)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	if !strings.Contains(string(log), "firewall block the ranges of Anthropic in") || !strings.Contains(string(log), "firewall remove the rules in") {
		t.Errorf("the verbose log must show the changes of the firewall:\n%s", log)
	}
}

func TestDriverFirewallCheckAndErrors(t *testing.T) {
	td := newTestDriver(t)
	pf := newFakePF()
	td.firewall = &pfFirewall{run: pf.run}
	td.step()
	td.nextCheck().reply <- newResult("CN", "CN", "CN")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	token := td.core.state.Firewall.Token

	// Other software removes the rules. The quick check of the next second loads them again.
	// The first step runs at the time of the last check, so the check comes at the second step.
	pf.anchor = ""
	td.step()
	td.step()
	if !hasPFRules(pf.anchor) || td.core.state.Firewall.Token != token {
		t.Errorf("the check must load the rules again and keep the token: %+v", pf)
	}

	// A check that fails every second adds only 1 error event, and only 1 log line.
	pf.anchor, pf.failOn = "", "-a com.apple/clawdiator -f -"
	for range 10 {
		td.step()
	}
	errorEvents := 0
	for _, change := range td.core.state.Events {
		if change.Kind == eventError && strings.HasPrefix(change.Error, "firewall: ") {
			errorEvents++
		}
	}
	if errorEvents != 1 || !strings.Contains(td.core.state.Firewall.Error, "Operation not permitted") {
		t.Errorf("the same error must add 1 event, not %d: %+v", errorEvents, td.core.state.Firewall)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	checkLines := 0
	for _, line := range strings.Split(string(log), "\n") {
		if strings.Contains(line, "firewall check in") && strings.Contains(line, "Operation not permitted") {
			checkLines++
		}
	}
	if checkLines != 1 {
		t.Errorf("the same error must add 1 line of the check to the verbose log, not %d:\n%s", checkLines, log)
	}
	pf.failOn = ""
	td.step()
	td.step()
	if td.core.state.Firewall.Error != "" || !hasPFRules(pf.anchor) {
		t.Errorf("a passed check must clear the error: %+v", td.core.state.Firewall)
	}

	// pfctl -d turns pf off, and pf forgets the reference. The check takes a new token without an event.
	// The new token must be on the disk at once, not at the next save of the heartbeat.
	pf.isEnabled, pf.tokens = false, nil
	td.lastFirewallAttempt, td.lastSave = time.Time{}, td.clock
	td.step()
	if saved, _ := loadServiceState(td.paths.State); len(pf.tokens) != 1 || saved.Firewall.Token != pf.tokens[0] {
		t.Errorf("the new token must be on the disk at once: %v, %+v", pf.tokens, saved.Firewall)
	}

	// A pause removes the rules.
	td.handleRequest(commandRequest{Command: socketPause, IsForever: true, User: "ivan"})
	td.afterStep(false)
	if pf.anchor != "" || len(pf.tokens) != 0 {
		t.Errorf("a pause must remove the rules and release the token: %+v", pf)
	}
}

// A new process of the service takes over the token of the old process. pf then still has exactly 1 token of clawdiator.
func TestDriverFirewallTakesOverToken(t *testing.T) {
	pf := newFakePF()
	first := newTestDriver(t)
	first.firewall = &pfFirewall{run: pf.run}
	first.step()
	saved, err := loadServiceState(first.paths.State)
	if err != nil || saved.Firewall.Token == "" {
		t.Fatalf("the first process must save its token: %+v, %v", saved, err)
	}
	second := newTestDriver(t)
	second.firewall = &pfFirewall{run: pf.run}
	second.core.state.Firewall = saved.Firewall
	second.step()
	if len(pf.tokens) != 1 || second.core.state.Firewall.Token != saved.Firewall.Token || pf.countCalls("-E") != 1 {
		t.Errorf("the second process must take over the token: %v, %+v", pf.tokens, second.core.state.Firewall)
	}
}

// Another program can change the hosts file. A new stamp makes the service check the block at once.
// If the block changed, the service writes it again and flushes the DNS cache. A change of other lines needs no flush.
func TestDriverRepairsHostsFile(t *testing.T) {
	td := newTestDriver(t)
	td.hostsFileStamp = "1"
	td.step()
	writes, flushes := len(td.hostsWrites), td.flushes
	td.afterStep(false)
	if len(td.hostsWrites) != writes {
		t.Fatalf("the same stamp must give no write: %d", len(td.hostsWrites))
	}

	// Another program removes the block.
	td.hostsFileStamp, td.isHostsTampered = "2", true
	td.afterStep(false)
	if len(td.hostsWrites) != writes+1 || td.flushes != flushes+1 {
		t.Errorf("a removed block must come back with a flush: %d writes, %d flushes", len(td.hostsWrites), td.flushes)
	}
	log, _ := os.ReadFile(td.paths.DebugLog)
	if !strings.Contains(string(log), "another program changed the block in the file, so the service wrote it again in") {
		t.Errorf("the verbose log must show the repair:\n%s", log)
	}

	// Another program changes other lines. The block is still correct.
	td.hostsFileStamp = "3"
	td.afterStep(false)
	if len(td.hostsWrites) != writes+2 || td.flushes != flushes+1 {
		t.Errorf("a change of other lines must give a check, but no flush: %d writes, %d flushes", len(td.hostsWrites), td.flushes)
	}
	td.afterStep(false)
	if len(td.hostsWrites) != writes+2 {
		t.Errorf("after the check, the same stamp must give no write: %d", len(td.hostsWrites))
	}
	log, _ = os.ReadFile(td.paths.DebugLog)
	if count := strings.Count(string(log), "another program changed the block"); count != 1 {
		t.Errorf("only the removed block is a repair, not the change of other lines: %d lines", count)
	}
}

// A failed removal of the rules gets a new try only every 30 seconds, not at each quick check.
func TestDriverFirewallRetriesRemovalSlowly(t *testing.T) {
	td := newTestDriver(t)
	pf := newFakePF()
	td.firewall = &pfFirewall{run: pf.run}
	td.step()
	pf.failOn = "-F rules"
	td.nextCheck().reply <- newResult("DE", "DE", "DE")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	for range 10 {
		td.step()
	}
	if count := pf.countCalls("-a com.apple/clawdiator -F rules"); count != 1 {
		t.Errorf("a failed removal must wait %v before the next try, not %d tries in 20 seconds", firewallRetryInterval, count)
	}
	for range 6 {
		td.step()
	}
	if count := pf.countCalls("-a com.apple/clawdiator -F rules"); count != 2 {
		t.Errorf("after %v, the removal must get 1 new try: %d", firewallRetryInterval, count)
	}
}

// During a block, a quick check runs every second and a full check every 5 seconds. Only a full check reads the token.
func TestDriverFirewallQuickAndFullChecks(t *testing.T) {
	td := newTestDriver(t)
	pf := newFakePF()
	td.firewall = &pfFirewall{run: pf.run}
	td.step()
	td.nextCheck().reply <- newResult("CN", "CN", "CN")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	pf.calls = nil
	for range 10 {
		td.step()
	}
	checks, fullChecks := pf.countCalls("-s info"), pf.countCalls("-s References")
	if checks < 9 || fullChecks < 3 || fullChecks > 5 {
		t.Errorf("in 20 seconds, each step must give a quick check, and about each third step a full check: %d checks, %d full checks", checks, fullChecks)
	}
}

// The DNS flush talks to mDNSResponder, which can be slow during a change of the network. The pass with the block must not wait for it.
func TestDriverDoesNotWaitForDNSFlush(t *testing.T) {
	td := newTestDriver(t)
	isFlushed := make(chan struct{})
	td.flushDNS = func() string {
		time.Sleep(300 * time.Millisecond)
		close(isFlushed)
		return "ok"
	}
	td.runFlush = func(task func()) { go task() }
	start := time.Now()
	td.handleTick()
	td.afterStep(false)
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Errorf("the pass with the block must not wait for the flush: %v", elapsed)
	}
	select {
	case <-isFlushed:
	case <-time.After(time.Second):
		t.Fatal("the flush must run after the write of the hosts file")
	}
	// The probe of the pass can send its result first, so read the results until the result of the flush comes.
	for isFlushLogged := false; !isFlushLogged; {
		select {
		case result := <-td.results:
			_, isFlushLogged = result.(dnsFlushDone)
			if isFlushLogged {
				td.handleResult(result)
			}
		case <-time.After(time.Second):
			t.Fatal("the result of the flush must come back to the loop")
		}
	}
	if log, _ := os.ReadFile(td.paths.DebugLog); !strings.Contains(string(log), "dns flush in 3") {
		t.Errorf("the verbose log must show the time of the flush:\n%s", log)
	}
}
