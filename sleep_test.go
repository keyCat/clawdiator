package main

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"clawdiator/internal/geocheck"
)

// Before a sleep, the core blocks. A check before the wake must not remove the block,
// because the computer can wake in another country. A gap of the ticks shows the wake, and then a check starts.
func TestPreBlockBeforeSleep(t *testing.T) {
	s := newOpenSimulation(t)
	s.runUntil(testStart.Add(time.Second))
	running := mustFindCheck(t, s.c.requestCheck("test"))
	s.c.prepareSleep(s.now)
	if !s.c.state.IsPreBlocked || !slices.Equal(getEventKinds(s.c.state), []eventKind{eventPreBlock, eventPreBlockEnd, eventPreBlock}) {
		t.Fatalf("a sleep must start the pre-block: %v", getEventKinds(s.c.state))
	}
	last := s.c.state.Events[len(s.c.state.Events)-1]
	if lines := formatEventLog(last); len(lines) != 1 || !strings.Contains(lines[0], "all pre-block the computer goes to sleep") {
		t.Errorf("the log must tell that the pre-block comes from the sleep: %q", lines)
	}
	s.c.handleCheck(s.now, running.Generation, newResult("DE", "DE"), nil)
	// A probe can start a check of the new generation before the computer sleeps.
	beforeWake := mustFindCheck(t, s.c.requestCheck("probe before the sleep"))
	s.c.handleCheck(s.now, beforeWake.Generation, newResult("DE", "DE"), nil)
	if !s.c.state.IsPreBlocked {
		t.Fatal("a check before the wake must not remove the block")
	}
	// The system needs some time to sleep. Ticks without a gap are not a wake.
	for _, next := range s.runUntil(s.now.Add(time.Second)) {
		if next.Kind == actionFullCheck {
			t.Fatalf("a tick without a gap must start no check: %+v", next)
		}
	}
	// The computer sleeps for 5 seconds. The first tick after the wake starts a check.
	s.now = s.now.Add(5 * time.Second)
	check := mustFindCheck(t, s.c.tick(s.now, s.fingerprint))
	s.c.handleCheck(s.now, check.Generation, newResult("DE", "DE"), nil)
	if s.c.state.IsPreBlocked {
		t.Error("a check after the wake must remove the pre-block")
	}
}

func TestWakeMessageAndSleepThatDoesNotCome(t *testing.T) {
	s := newOpenSimulation(t)
	s.c.prepareSleep(s.now)
	s.c.noteWake()
	if check := mustFindCheck(t, s.c.tick(s.now.Add(tickInterval), s.fingerprint)); check.Reason[:5] != "wake " {
		t.Errorf("the message about the wake must start a check at the next tick: %+v", check)
	}

	// No sleep and no wake come. After maxSleepWait, the core starts a check anyway.
	s = newOpenSimulation(t)
	s.c.prepareSleep(s.now)
	var checks []action
	for _, next := range s.runUntil(s.now.Add(maxSleepWait + time.Second)) {
		if next.Kind == actionFullCheck {
			checks = append(checks, next)
		}
	}
	if len(checks) != 1 {
		t.Errorf("a missing sleep must give 1 check after %v: %+v", maxSleepWait, checks)
	}
	s.c.noteWake()
	if s.c.isWakeNoted {
		t.Error("a message about a wake without a sleep must change nothing")
	}
}

// The loop must write the block before it answers the request. Then the system sleeps only with the block on.
func TestDriverBlocksBeforeSleep(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	td.nextCheck().reply <- newResult("DE", "DE", "DE")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	writes := make(chan []string, 8)
	td.writeHosts = func(hosts []string) (bool, error) {
		writes <- hosts
		return true, nil
	}
	sleeps := make(chan sleepRequest)
	stopped := make(chan struct{})
	go func() {
		td.run(loopInputs{sleeps: sleeps})
		close(stopped)
	}()
	if !requestSleepBlock(sleeps) {
		t.Fatal("the loop must answer the request")
	}
	select {
	case hosts := <-writes:
		if len(hosts) != len(getBlockedHosts(geocheck.Providers())) {
			t.Errorf("the block before the sleep must cover both providers: %d hosts", len(hosts))
		}
	default:
		t.Error("the loop must write the block before the answer")
	}
	td.stop()
	<-stopped
}

func TestSleepRequestWithoutAnswer(t *testing.T) {
	start := time.Now()
	if requestSleepBlock(make(chan sleepRequest)) {
		t.Error("a request without a reader must fail")
	}
	if elapsed := time.Since(start); elapsed < sleepBlockTimeout || elapsed > sleepBlockTimeout+time.Second {
		t.Errorf("the request must wait %v, not %v", sleepBlockTimeout, elapsed)
	}
}

// The registration only asks IOKit for the messages about sleep. It changes nothing on the computer.
func TestWatchSleepRegisters(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS has the messages about sleep now")
	}
	sleeps, wakes, err := watchSleep()
	if err != nil || sleeps == nil || wakes == nil {
		t.Errorf("the registration with IOKit must pass: %v", err)
	}
}

// Each message that needs an answer gets exactly 1 answer: after the block, after the timeout and after a panic.
func TestHandlePowerMessage(t *testing.T) {
	var answers []uintptr
	allow := func(argument uintptr) { answers = append(answers, argument) }
	wakes := make(chan struct{}, 1)

	handlePowerMessage(ioMessageCanSystemSleep, 7, allow, nil, wakes)
	handlePowerMessage(ioMessageSystemHasPoweredOn, 8, allow, nil, wakes)
	handlePowerMessage(0xe0000999, 9, allow, nil, wakes)
	if !slices.Equal(answers, []uintptr{7}) || len(wakes) != 1 {
		t.Errorf("only the question about a sleep needs an answer, and a wake must send a value: %v, %d", answers, len(wakes))
	}

	requests := make(chan sleepRequest)
	go func() {
		request := <-requests
		close(request.Done)
	}()
	handlePowerMessage(ioMessageSystemWillSleep, 10, allow, requests, wakes)
	timeouts := sleepBlockTimeouts.Load()
	handlePowerMessage(ioMessageSystemWillSleep, 11, allow, make(chan sleepRequest), wakes)
	closed := make(chan sleepRequest)
	close(closed)
	handlePowerMessage(ioMessageSystemWillSleep, 12, allow, closed, wakes)
	if !slices.Equal(answers, []uintptr{7, 10, 11, 12}) {
		t.Errorf("each sleep must get 1 answer, also after a timeout and after a panic: %v", answers)
	}
	if sleepBlockTimeouts.Load() != timeouts+1 {
		t.Error("a timeout must increase the count for the log")
	}
}

// At the wake, the pre-block starts again. The agent then measures a short pre-block, not the time of the sleep.
// The service saves at once, because the agent and status need the new heartbeat.
func TestWakeGivesNewPreBlockStartAndSave(t *testing.T) {
	td := newTestDriver(t)
	td.step()
	td.nextCheck().reply <- newResult("DE", "DE", "DE")
	td.handleResult(td.waitCheckResult())
	td.afterStep(false)
	td.core.prepareSleep(td.clock)
	td.afterStep(false)
	td.clock = td.clock.Add(time.Hour)
	td.lastSave = td.clock
	wake := td.clock
	td.step()
	if !td.core.state.PreBlockSince.Equal(wake) {
		t.Errorf("the pre-block must start again at the wake: %v, not %v", td.core.state.PreBlockSince, wake)
	}
	saved, err := loadServiceState(td.paths.State)
	if err != nil || !saved.Heartbeat.Equal(wake) || !saved.PreBlockSince.Equal(wake) {
		t.Errorf("the new generation at the wake must save at once: %+v, %v", saved, err)
	}
}

// After a wake, the agent can read the state from before the sleep. It must not show a notice of a long pre-block for it.
func TestAgentAfterWakeWithOldState(t *testing.T) {
	model := newAgentModel(agentSettings{})
	sleep := testStart
	old := &serviceState{Heartbeat: sleep, IsPreBlocked: true, PreBlockSince: sleep, LastEventSeq: 1,
		Events: []event{{Seq: 1, Time: sleep, Kind: eventPreBlock}}}
	model.update(sleep, old, false)
	wake := sleep.Add(time.Hour)
	if got := model.update(wake, old, false); len(got) != 0 {
		t.Errorf("the old state after a wake must give no notice: %v", got)
	}
	fresh := *old
	fresh.Heartbeat, fresh.PreBlockSince = wake.Add(200*time.Millisecond), wake.Add(200*time.Millisecond)
	if got := model.update(wake.Add(time.Second), &fresh, false); len(got) != 0 {
		t.Errorf("a short pre-block after the wake must give no notice: %v", got)
	}
	later := fresh
	later.Heartbeat = wake.Add(time.Minute)
	if got := model.update(wake.Add(time.Minute), &later, false); len(got) != 1 {
		t.Errorf("a long pre-block after the wake must still give 1 notice: %v", got)
	}
}
