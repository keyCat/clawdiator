package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"geocheck"
)

var testStart = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

var (
	germany = geocheck.Location{Country: "DE", Source: "cloudflare"}
	china   = geocheck.Location{Country: "CN", Source: "cloudflare"}
	ukraine = geocheck.Location{Country: "UA", Source: "cloudflare"}
)

var errOffline = errors.New("offline")

// newResult returns a result with 1 reply for each place. A place is "DE" or "UA/Kyiv City".
func newResult(places ...string) geocheck.Result {
	var result geocheck.Result
	for index, place := range places {
		country, region, _ := strings.Cut(place, "/")
		result.Locations = append(result.Locations, geocheck.Location{Country: country, Region: region, Source: "service" + string(rune('a'+index))})
	}
	return result
}

func findAction(actions []action, kind actionKind) (action, bool) {
	for _, next := range actions {
		if next.Kind == kind {
			return next, true
		}
	}
	return action{}, false
}

func mustFindCheck(t *testing.T, actions []action) action {
	t.Helper()
	check, isFound := findAction(actions, actionFullCheck)
	if !isFound {
		t.Fatalf("the actions %+v have no full check", actions)
	}
	return check
}

func getEventKinds(state *serviceState) []eventKind {
	var kinds []eventKind
	for _, change := range state.Events {
		kinds = append(kinds, change.Kind)
	}
	return kinds
}

// simulation drives a core with 1 tick every 2 seconds, the same as the driver. It never skips a tick,
// because a gap of more than 30 seconds counts as a wake from sleep.
type simulation struct {
	t           *testing.T
	c           *core
	now         time.Time
	fingerprint string
	probe       geocheck.Location
	probeErr    error
}

// newSimulation returns a simulation before its first tick. The first tick is at testStart.
func newSimulation(t *testing.T) *simulation {
	return &simulation{t: t, c: newCore(&serviceState{}, nil), now: testStart.Add(-2 * time.Second), fingerprint: "wifi", probe: germany}
}

// newOpenSimulation returns a simulation after the first full check in Germany. The check ended at testStart plus 1 second.
func newOpenSimulation(t *testing.T) *simulation {
	t.Helper()
	s := newSimulation(t)
	check := mustFindCheck(t, s.runUntil(testStart))
	s.c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("DE", "DE", "DE"), nil)
	if len(s.c.state.blockedProviders()) != 0 {
		t.Fatalf("the setup must give no block: %v", s.c.state.blockedProviders())
	}
	return s
}

// runUntil ticks until the clock reaches the end. It answers each probe at once with the probe of the simulation.
// It returns the full checks that the core started.
func (s *simulation) runUntil(end time.Time) []action {
	var checks []action
	for s.now.Before(end) {
		s.now = s.now.Add(2 * time.Second)
		for _, next := range s.c.tick(s.now, s.fingerprint) {
			if next.Kind == actionFullCheck {
				checks = append(checks, next)
				continue
			}
			checks = append(checks, s.c.handleProbe(s.now, next.Generation, s.probe, s.probeErr)...)
		}
	}
	return checks
}

func TestStartPreBlocksUntilFullCheck(t *testing.T) {
	c := newCore(&serviceState{}, nil)
	actions := c.tick(testStart, "wifi")
	check := mustFindCheck(t, actions)
	probe, hasProbe := findAction(actions, actionProbe)
	if check.Generation != 1 || check.Rounds != preBlockRounds || !hasProbe || probe.Generation != 1 {
		t.Errorf("the start must start a full check and a probe: %+v", actions)
	}
	if got := c.state.blockedProviders(); !slices.Equal(got, geocheck.Providers()) {
		t.Errorf("the start must block all providers: %v", got)
	}
	c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("DE", "DE", "DE"), nil)
	if got := c.state.blockedProviders(); len(got) != 0 || c.state.IsPreBlocked {
		t.Errorf("an allowed country must remove the pre-block: %v", got)
	}
	if got := getEventKinds(c.state); !slices.Equal(got, []eventKind{eventPreBlock, eventPreBlockEnd}) {
		t.Errorf("events = %v", got)
	}
	if c.state.Country != "DE" || !c.state.LastFullCheck.Equal(testStart.Add(time.Second)) {
		t.Errorf("the state must keep the full check: %q, %v", c.state.Country, c.state.LastFullCheck)
	}
}

func TestUnsupportedCountryBlocksBothProviders(t *testing.T) {
	c := newCore(&serviceState{}, nil)
	check := mustFindCheck(t, c.tick(testStart, "wifi"))
	c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("CN", "CN", "CN"), nil)
	if got := c.state.blockedProviders(); !slices.Equal(got, geocheck.Providers()) {
		t.Errorf("CN must block both providers: %v", got)
	}
	if got := getEventKinds(c.state); !slices.Equal(got, []eventKind{eventPreBlock, eventBlock, eventPreBlockEnd}) {
		t.Fatalf("events = %v", got)
	}
	blockEvent := c.state.Events[1]
	if blockEvent.Country != "CN" || blockEvent.Verdict != "not_supported" || !slices.Equal(blockEvent.Providers, geocheck.Providers()) ||
		!strings.Contains(blockEvent.Reasons[geocheck.Anthropic], "country CN is not supported by Anthropic") {
		t.Errorf("the block event = %+v", blockEvent)
	}
}

func TestEachProviderHasItsOwnList(t *testing.T) {
	c := newCore(&serviceState{}, nil)
	check := mustFindCheck(t, c.tick(testStart, "wifi"))
	c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("AF", "AF", "AF"), nil)
	if got := c.state.blockedProviders(); !slices.Equal(got, []geocheck.Provider{geocheck.Anthropic}) {
		t.Errorf("AF must block only Anthropic: %v", got)
	}
	// 1 blocked provider blocks the third-party platforms.
	if !c.state.isThirdPartyBlocked() {
		t.Error("1 blocked provider must block the third-party platforms")
	}
	if newOpenSimulation(t).c.state.isThirdPartyBlocked() {
		t.Error("without a block, the third-party platforms must stay open")
	}
}

func TestBlockedCountriesOfTheConfig(t *testing.T) {
	c := newCore(&serviceState{}, []string{"de"})
	check := mustFindCheck(t, c.tick(testStart, "wifi"))
	c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("DE"), nil)
	if got := c.state.blockedProviders(); !slices.Equal(got, geocheck.Providers()) || c.state.provider(geocheck.OpenAI).Verdict != "blocked" {
		t.Errorf("a country in blocked_countries must block both providers: %v", got)
	}
}

// Trap 1: a full check can take about 35 seconds. A result from the old network must not unblock the new network.
func TestNetworkChangeIgnoresOldResult(t *testing.T) {
	s := newOpenSimulation(t)
	s.fingerprint = "hotel"
	second := mustFindCheck(t, s.runUntil(testStart.Add(2*time.Second)))
	s.fingerprint = "hotel-2"
	third := mustFindCheck(t, s.runUntil(testStart.Add(4*time.Second)))
	if second.Generation == third.Generation || !s.c.state.IsPreBlocked {
		t.Fatalf("each change must start a new generation and a pre-block: %+v, %+v", second, third)
	}
	s.c.handleCheck(testStart.Add(5*time.Second), second.Generation, newResult("DE", "DE", "DE"), nil)
	if !s.c.state.IsPreBlocked {
		t.Error("a result of an older generation must not remove the pre-block")
	}
	s.c.handleCheck(testStart.Add(6*time.Second), third.Generation, newResult("CN", "CN", "CN"), nil)
	if s.c.state.IsPreBlocked || !slices.Equal(s.c.state.blockedProviders(), geocheck.Providers()) {
		t.Errorf("the result of the current generation must apply: %v", s.c.state.blockedProviders())
	}
}

// Trap 2: after a wake from sleep, the fingerprint can be the same. A long gap between 2 ticks must count as a change.
func TestWakeOnSameNetworkPreBlocks(t *testing.T) {
	s := newOpenSimulation(t)
	if checks := s.runUntil(testStart.Add(time.Minute)); len(checks) != 0 || s.c.state.IsPreBlocked {
		t.Fatalf("normal ticks must not pre-block: %+v", checks)
	}
	// The Mac sleeps for 8 hours. Then the driver ticks again on the same network.
	s.now = s.now.Add(8 * time.Hour)
	checks := s.runUntil(s.now.Add(2 * time.Second))
	if len(checks) != 1 || !s.c.state.IsPreBlocked || checks[0].Rounds != preBlockRounds {
		t.Errorf("a wake must pre-block and start a full check: %+v", checks)
	}
}

// Without a change of the network, a failed full check of 3 lookups blocks.
func TestLookupFailureWithoutChangeBlocks(t *testing.T) {
	s := newOpenSimulation(t)
	checks := s.runUntil(testStart.Add(fullCheckInterval + 2*time.Second))
	if len(checks) != 1 || checks[0].Rounds != steadyRounds {
		t.Fatalf("after 60 minutes, the core must start 1 full check of %d lookups: %+v", steadyRounds, checks)
	}
	// The full check runs for about 36 seconds. No other full check starts in this time.
	if more := s.runUntil(s.now.Add(36 * time.Second)); len(more) != 0 {
		t.Fatalf("a running full check must block a second one: %+v", more)
	}
	failedAt := s.now.Add(time.Second)
	s.c.handleCheck(failedAt, checks[0].Generation, geocheck.Result{}, errors.New("2 of 4 location services replied"))
	if got := s.c.state.blockedProviders(); !slices.Equal(got, geocheck.Providers()) || s.c.state.provider(geocheck.Anthropic).Verdict != "lookup_failed" {
		t.Fatalf("a failed full check must block both providers: %v", got)
	}
	if retries := s.runUntil(failedAt.Add(retryInterval - time.Second)); len(retries) != 0 {
		t.Errorf("the retry must wait %v: %+v", retryInterval, retries)
	}
	retries := s.runUntil(failedAt.Add(retryInterval + time.Second))
	if len(retries) != 1 {
		t.Fatalf("the retry must start after %v: %+v", retryInterval, retries)
	}
	s.c.handleCheck(s.now, retries[0].Generation, newResult("DE", "DE", "DE"), nil)
	if len(s.c.state.blockedProviders()) != 0 || s.c.state.Events[len(s.c.state.Events)-1].Kind != eventUnblock {
		t.Errorf("a passed retry must unblock: %v, %v", s.c.state.blockedProviders(), getEventKinds(s.c.state))
	}
}

// After a change of the network, a failed full check keeps the pre-block.
// The pre-block stops all access, so the first retries come soon. They slow down after 10 failures.
func TestLookupFailureAfterChangeKeepsPreBlock(t *testing.T) {
	s := newSimulation(t)
	check := mustFindCheck(t, s.runUntil(testStart))
	wantDelays := []time.Duration{
		5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, 30 * time.Second,
		30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second, 5 * time.Minute,
	}
	for index, wantDelay := range wantDelays {
		failure := index + 1
		failedAt := s.now
		s.c.handleCheck(failedAt, check.Generation, geocheck.Result{}, errOffline)
		if s.c.state.LastFailure.Error != "offline" || !s.c.state.LastFailure.Time.Equal(failedAt) {
			t.Fatalf("failure %d: the state must keep the last failure: %+v", failure, s.c.state.LastFailure)
		}
		if !s.c.nextRetry.Equal(failedAt.Add(wantDelay)) {
			t.Fatalf("failure %d: the next retry = %v, want %v later", failure, s.c.nextRetry, wantDelay)
		}
		// The probes continue during the wait. They must not start a full check before the retry.
		if early := s.runUntil(failedAt.Add(wantDelay - 2*time.Second)); len(early) != 0 {
			t.Fatalf("failure %d: a full check started before the retry: %+v", failure, early)
		}
		checks := s.runUntil(failedAt.Add(wantDelay))
		if len(checks) != 1 || checks[0].Rounds != preBlockRounds {
			t.Fatalf("failure %d: the retry = %+v", failure, checks)
		}
		check = checks[0]
	}
	if !s.c.state.IsPreBlocked || slices.Contains(getEventKinds(s.c.state), eventBlock) {
		t.Errorf("failed checks in a pre-block must keep the pre-block and add no block event: %v", getEventKinds(s.c.state))
	}
	s.c.handleCheck(s.now, check.Generation, newResult("DE", "DE", "DE"), nil)
	if s.c.state.LastFailure.Error != "" || s.c.state.IsPreBlocked {
		t.Errorf("a passed full check must clear the last failure and end the pre-block: %+v", s.c.state.LastFailure)
	}
}

func TestGetRetryDelay(t *testing.T) {
	cases := []struct {
		failures     int
		isPreBlocked bool
		want         time.Duration
	}{
		{1, false, 30 * time.Second},
		{3, false, 30 * time.Second},
		{4, false, 5 * time.Minute},
		{1, true, 5 * time.Second},
		{2, true, 10 * time.Second},
		{3, true, 20 * time.Second},
		{4, true, 30 * time.Second},
		{9, true, 30 * time.Second},
		{10, true, 5 * time.Minute},
	}
	for _, testCase := range cases {
		if got := getRetryDelay(testCase.failures, testCase.isPreBlocked); got != testCase.want {
			t.Errorf("getRetryDelay(%d, %v) = %v, want %v", testCase.failures, testCase.isPreBlocked, got, testCase.want)
		}
	}
}

func TestProbeFailureStartsFewFullChecks(t *testing.T) {
	s := newOpenSimulation(t)
	s.probe, s.probeErr = geocheck.Location{}, errors.New("timeout")
	checks := s.runUntil(testStart.Add(30 * time.Second))
	if len(checks) != 1 || checks[0].Rounds != steadyRounds || s.c.state.IsPreBlocked || len(s.c.state.blockedProviders()) != 0 {
		t.Fatalf("a failed probe must start a full check, but no block: %+v", checks)
	}
	s.c.handleCheck(s.now, checks[0].Generation, newResult("DE", "DE", "DE"), nil)
	if more := s.runUntil(testStart.Add(30*time.Second + probeFailureCheckInterval - 2*time.Second)); len(more) != 0 {
		t.Errorf("failed probes in 5 minutes must start no other full check: %+v", more)
	}
	if more := s.runUntil(testStart.Add(time.Minute + probeFailureCheckInterval)); len(more) != 1 {
		t.Errorf("a failed probe after 5 minutes must start a full check: %+v", more)
	}
}

func TestProbeAddressChangePreBlocks(t *testing.T) {
	s := newOpenSimulation(t)
	s.c.state.PublicIP = "192.0.2.1"
	s.probe = geocheck.Location{Country: "DE", IP: "198.51.100.7", Source: "cloudflare"}
	checks := s.runUntil(testStart.Add(30 * time.Second))
	if len(checks) != 1 || !s.c.state.IsPreBlocked || checks[0].Generation != 2 || s.c.state.PublicIP != "198.51.100.7" {
		t.Fatalf("a new public address must pre-block: %+v, %q", checks, s.c.state.PublicIP)
	}
	if more := s.runUntil(testStart.Add(2 * time.Minute)); len(more) != 0 || s.c.generation != 2 {
		t.Errorf("the next probes with the same new address must not change the network again: %+v", more)
	}
}

func TestProbeBlocksButCannotUnblock(t *testing.T) {
	s := newOpenSimulation(t)
	s.probe = china
	checks := s.runUntil(testStart.Add(30 * time.Second))
	if len(checks) != 1 || checks[0].Rounds != steadyRounds || !slices.Equal(s.c.state.blockedProviders(), geocheck.Providers()) || s.c.state.IsPreBlocked {
		t.Fatalf("a probe in CN must block at once and start a full check: %+v, %v", checks, s.c.state.blockedProviders())
	}
	s.probe = germany
	if more := s.runUntil(testStart.Add(2 * time.Minute)); len(more) != 0 || len(s.c.state.blockedProviders()) != 2 {
		t.Errorf("a probe must not remove a block: %+v, %v", more, s.c.state.blockedProviders())
	}
}

// Trap 4: the trace has no region. In Ukraine, the probe must use the region of the last full check.
func TestProbeUsesRegionOfLastFullCheck(t *testing.T) {
	s := newSimulation(t)
	s.probe = ukraine
	check := mustFindCheck(t, s.runUntil(testStart))
	s.c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("UA", "UA/Kyiv City"), nil)
	if len(s.c.state.blockedProviders()) != 0 {
		t.Fatalf("Kyiv must be allowed: %v", s.c.state.blockedProviders())
	}
	if checks := s.runUntil(testStart.Add(10 * time.Minute)); len(checks) != 0 || len(s.c.state.blockedProviders()) != 0 {
		t.Errorf("probes in Ukraine with a known region must not block: %+v, %v", checks, s.c.state.blockedProviders())
	}

	other := newOpenSimulation(t)
	other.probe = ukraine
	mustFindCheck(t, other.runUntil(testStart.Add(30*time.Second)))
	if got := other.c.state.provider(geocheck.Anthropic).Verdict; got != "region_unknown" {
		t.Errorf("a probe in Ukraine without a known region must block: %q", got)
	}
}

func TestHourlyFullCheck(t *testing.T) {
	s := newOpenSimulation(t)
	if checks := s.runUntil(testStart.Add(fullCheckInterval)); len(checks) != 0 {
		t.Errorf("no full check before 60 minutes: %+v", checks)
	}
	if checks := s.runUntil(testStart.Add(fullCheckInterval + 2*time.Second)); len(checks) != 1 || checks[0].Rounds != steadyRounds {
		t.Errorf("1 full check after 60 minutes: %+v", checks)
	}
}

func TestPauseAndResume(t *testing.T) {
	s := newOpenSimulation(t)
	s.c.pause(testStart.Add(time.Second), time.Time{}, "ivan")
	s.fingerprint = "hotel"
	check := mustFindCheck(t, s.runUntil(testStart.Add(2*time.Second)))
	if s.c.state.IsPreBlocked || check.Rounds != steadyRounds {
		t.Errorf("a change of the network during a pause must not pre-block: %+v", check)
	}
	s.c.handleCheck(testStart.Add(3*time.Second), check.Generation, newResult("CN", "CN"), nil)
	if len(s.c.state.blockedProviders()) != 0 {
		t.Errorf("a pause must block nothing: %v", s.c.state.blockedProviders())
	}
	mustFindCheck(t, s.c.resume(testStart.Add(3*time.Second)))
	want := []eventKind{eventPreBlock, eventPreBlockEnd, eventPause, eventPauseBlock, eventPauseEnd, eventBlock, eventPreBlock}
	if got := getEventKinds(s.c.state); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if !slices.Equal(s.c.state.blockedProviders(), geocheck.Providers()) || s.c.state.Pause.IsOn {
		t.Errorf("after the resume, the block must apply again: %v", s.c.state.blockedProviders())
	}
}

func TestPauseEndsAtItsTime(t *testing.T) {
	s := newOpenSimulation(t)
	until := testStart.Add(2 * time.Minute)
	s.c.pause(testStart.Add(time.Second), until, "ivan")
	if pause := s.c.state.Events[len(s.c.state.Events)-1]; !pause.Until.Equal(until) || pause.User != "ivan" {
		t.Errorf("the pause event must have the end and the user: %+v", pause)
	}
	s.runUntil(until.Add(-2 * time.Second))
	if !s.c.state.Pause.IsOn {
		t.Fatal("the pause must stay until its end")
	}
	checks := s.runUntil(until)
	if s.c.state.Pause.IsOn || !s.c.state.IsPreBlocked || len(checks) != 1 {
		t.Errorf("at its end, the pause must stop and start a pre-block: %+v", checks)
	}
}

func TestRestartPreBlocksAndKeepsVerdicts(t *testing.T) {
	state := &serviceState{}
	state.provider(geocheck.Anthropic).IsBlocked = true
	c := newCore(state, nil)
	check := mustFindCheck(t, c.tick(testStart, "wifi"))
	if !slices.Equal(state.blockedProviders(), geocheck.Providers()) {
		t.Errorf("the start after a restart must pre-block: %v", state.blockedProviders())
	}
	c.handleCheck(testStart.Add(time.Second), check.Generation, newResult("DE"), nil)
	unblock := state.Events[1]
	if unblock.Kind != eventUnblock || !slices.Equal(unblock.Providers, []geocheck.Provider{geocheck.Anthropic}) {
		t.Errorf("the saved block of Anthropic must end with an unblock event: %+v", state.Events)
	}
}

func TestEventsKeepSequenceAfterRestart(t *testing.T) {
	path := t.TempDir() + "/state.json"
	c := newCore(&serviceState{}, nil)
	for index := 0; index < maxEvents+10; index++ {
		c.addError(testStart, "test")
	}
	if len(c.state.Events) != maxEvents || c.state.Events[0].Seq != 11 || c.state.LastEventSeq != maxEvents+10 {
		t.Fatalf("the state must keep the last %d events: %d, first %d", maxEvents, len(c.state.Events), c.state.Events[0].Seq)
	}
	if err := saveServiceState(path, c.state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadServiceState(path)
	if err != nil {
		t.Fatal(err)
	}
	newCore(loaded, nil).addError(testStart, "after restart")
	if got := loaded.Events[len(loaded.Events)-1].Seq; got != maxEvents+11 {
		t.Errorf("the sequence must continue after a restart: %d", got)
	}
}

func TestIsSameNetwork(t *testing.T) {
	cases := []struct {
		first, second string
		want          bool
	}{
		{"192.0.2.1", "192.0.2.1", true},
		{"192.0.2.1", "192.0.2.2", false},
		{"2001:db8:1:2::10", "2001:db8:1:2:a:b:c:d", true},
		{"2001:db8:1:2::10", "2001:db8:1:3::10", false},
		{"192.0.2.1", "2001:db8::1", true},
		{"::ffff:192.0.2.1", "192.0.2.1", true},
		{"not an address", "not an address", true},
	}
	for _, testCase := range cases {
		if got := isSameNetwork(testCase.first, testCase.second); got != testCase.want {
			t.Errorf("isSameNetwork(%q, %q) = %v, want %v", testCase.first, testCase.second, got, testCase.want)
		}
	}
}
