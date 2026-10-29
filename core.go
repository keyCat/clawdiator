package main

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"clawdiator/internal/geocheck"
)

// These constants set the times, the retries and the number of lookups of the core.
const (
	// probeInterval is the time between 2 probes to the Cloudflare trace.
	probeInterval = 30 * time.Second
	// fullCheckInterval is the time between 2 full checks when nothing changes.
	fullCheckInterval = 60 * time.Minute
	// probeFailureCheckInterval is the minimum time between 2 full checks that a failed probe starts.
	probeFailureCheckInterval = 5 * time.Minute
	// retryInterval is the time before the next full check after a failed full check, when no pre-block is on.
	retryInterval = 10 * time.Second
	// slowRetryInterval replaces the other waits after many failed full checks in a row.
	// Many fast retries can go above the request limits of the location services.
	slowRetryInterval = 5 * time.Minute
	// slowRetryAfter is the number of failed full checks in a row, without a pre-block, before the slow retries.
	slowRetryAfter = 4
	// preBlockSlowRetryAfter is the same number during a pre-block. With preBlockRetryDelays, the fast retries last about 3 minutes.
	preBlockSlowRetryAfter = 20
	// wakeGap is the gap between 2 ticks that counts as a wake from sleep.
	// The driver calls tick every 250 milliseconds, so a longer gap means that the computer slept.
	wakeGap = 30 * time.Second
	// networkQuietDelay is the time without a new change of the network before the full check starts.
	// A change of the network gives several changes of the fingerprint in a short time. The pre-block starts at the first change,
	// but the full check waits for quiet. So a burst of changes starts only 1 full check, and the HTTP requests do not increase.
	networkQuietDelay = 300 * time.Millisecond
	// sleepWakeGap is the gap between 2 ticks after a pre-block before sleep that shows the end of the sleep.
	// The tick runs every 250 milliseconds, so a longer gap means that the loop stopped for the sleep.
	sleepWakeGap = 2 * time.Second
	// maxSleepWait is the maximum time between the pre-block before sleep and the next full check.
	// If no sleep comes in this time, the core starts the full check anyway, so the pre-block cannot stay.
	maxSleepWait = time.Minute
	// preBlockRounds is the number of lookups of a full check during a pre-block.
	// The pre-block already blocks, so the service does not wait for more lookups. It tries again after preBlockRetryDelays.
	preBlockRounds = 1
	// steadyRounds is the number of lookups of a full check when the network did not change.
	steadyRounds = 3
)

// preBlockRetryDelays are the waits after the first failed full checks during a pre-block.
// The pre-block stops all access, so the service tries again soon. After the list, it waits the last value.
var preBlockRetryDelays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

// getRetryDelay returns the wait after the failed full check with the number failures in a row.
func getRetryDelay(failures int, isPreBlocked bool) time.Duration {
	if !isPreBlocked {
		if failures >= slowRetryAfter {
			return slowRetryInterval
		}
		return retryInterval
	}
	if failures >= preBlockSlowRetryAfter {
		return slowRetryInterval
	}
	return preBlockRetryDelays[min(failures, len(preBlockRetryDelays))-1]
}

type actionKind int

const (
	// actionProbe asks the driver to send 1 probe to the Cloudflare trace.
	actionProbe actionKind = iota
	// actionFullCheck asks the driver to start a full check. The driver stops a full check of an older generation first.
	actionFullCheck
)

// action is a request from the core to the driver. The core itself sends no requests and changes no files.
type action struct {
	Kind actionKind
	// Generation is the network generation at the start. The core ignores a result of an older generation.
	Generation int
	// Rounds is the number of lookups of a full check.
	Rounds int
	// Reason tells why the core started the full check. The verbose log shows it.
	Reason string
}

// core holds the logic of the service. It reads no clock, so the tests can drive it with fixed times.
//
// The driver must follow these rules:
//   - It calls tick every 250 milliseconds, and at once after a message about the network.
//     All calls of the core come from 1 goroutine, because the core has no locks.
//   - It never waits for the network in that goroutine. Probes and full checks run in other goroutines,
//     and their results come back over a channel. Otherwise a full check of 35 seconds stops the ticks,
//     the next tick counts as a wake, and the service never leaves the pre-block.
//   - Before it starts a full check, it stops a running full check of an older generation.
//   - The full check asks the services of addProbeService.
//   - After each call, it applies blockedProviders. It saves state.json after each change and at least every 30 seconds,
//     because the agent watches the heartbeat.
type core struct {
	state *serviceState
	// blockedCountries holds more country codes to block, from the config.
	blockedCountries []string

	// generation increases at each change of the network. A result of an older generation is not valid.
	generation  int
	fingerprint string
	lastTick    time.Time
	lastProbe   time.Time
	// isProbeRunning tells if a probe started and its result did not come back.
	isProbeRunning bool
	// checkGeneration is the generation of the full check that runs now. 0 means that no full check runs.
	checkGeneration int
	// failures is the number of failed full checks in a row.
	failures              int
	nextRetry             time.Time
	lastProbeFailureCheck time.Time
	// pendingCheckAt is the time when the full check after a change of the network starts. A zero value means no wait.
	pendingCheckAt     time.Time
	pendingCheckReason string
	// sleepStart is the time of the pre-block before a sleep. Until the wake, the core ignores the results of the full checks.
	// isWakeNoted tells that the system sent the message about the wake.
	sleepStart  time.Time
	isWakeNoted bool
	// debugf writes a line to the verbose log. The driver sets it. It can be nil.
	debugf func(format string, args ...any)
}

func newCore(state *serviceState, blockedCountries []string) *core {
	return &core{state: state, blockedCountries: blockedCountries}
}

// debug writes a line to the verbose log, if the driver gave a function for it.
func (c *core) debug(format string, args ...any) {
	if c.debugf != nil {
		c.debugf(format, args...)
	}
}

// tick runs every 250 milliseconds, and at once after a message about the network. The fingerprint identifies the network:
// the interface of the default route and its stable addresses.
func (c *core) tick(now time.Time, fingerprint string) []action {
	// Use the wall clock. The monotonic clock can stop while the computer sleeps.
	now = now.Round(0)
	var reason string
	switch {
	case c.lastTick.IsZero():
		reason = "service start"
		c.debug("network fingerprint %q", fingerprint)
	case !c.sleepStart.IsZero() && (c.isWakeNoted || now.Sub(c.lastTick) > sleepWakeGap || now.Sub(c.sleepStart) > maxSleepWait):
		reason = fmt.Sprintf("wake after the sleep: %v without a pass", now.Sub(c.lastTick).Round(time.Millisecond))
		c.sleepStart, c.isWakeNoted = time.Time{}, false
		// The pre-block started before the sleep. The agent measures its length for the notices,
		// so the length starts again at the wake. Otherwise each long sleep gives a notice of a long pre-block.
		if c.state.IsPreBlocked {
			c.state.PreBlockSince = now
		}
	case now.Sub(c.lastTick) > wakeGap:
		reason = fmt.Sprintf("wake: %v without a pass", now.Sub(c.lastTick).Round(time.Second))
	case fingerprint != c.fingerprint:
		reason = "network change"
		c.debug("network fingerprint %q -> %q", c.fingerprint, fingerprint)
	}
	c.lastTick = now
	c.fingerprint = fingerprint
	c.state.Heartbeat = now

	if c.state.Pause.IsOn && !c.state.Pause.Until.IsZero() && !now.Before(c.state.Pause.Until) {
		c.endPause(now)
		if reason == "" {
			reason = "end of the pause"
		}
	}
	var actions []action
	switch {
	case reason == "network change":
		c.preBlockNetwork(now, reason)
	case reason != "":
		actions = append(actions, c.changeNetwork(now, reason))
	}
	if !c.pendingCheckAt.IsZero() && !now.Before(c.pendingCheckAt) {
		c.pendingCheckAt = time.Time{}
		actions = append(actions, c.requestCheck(c.pendingCheckReason)...)
	}
	// During the wait for quiet, no retry and no hourly check starts. The deferred check replaces them.
	if c.checkGeneration == 0 && c.pendingCheckAt.IsZero() {
		if c.failures > 0 && !now.Before(c.nextRetry) {
			actions = append(actions, c.startCheck(fmt.Sprintf("retry after %d failed checks", c.failures)))
		} else if c.failures == 0 && now.Sub(c.state.LastFullCheck) >= fullCheckInterval {
			actions = append(actions, c.startCheck("hourly check"))
		}
	}
	if !c.isProbeRunning && now.Sub(c.lastProbe) >= probeInterval {
		c.isProbeRunning = true
		c.lastProbe = now
		actions = append(actions, action{Kind: actionProbe, Generation: c.generation})
	}
	return actions
}

// prepareSleep blocks all providers before a sleep of the computer. The computer can wake in another country,
// so the block must be on before the first packet after the wake. A new generation makes the running checks invalid.
// Until the wake, the core ignores the results of the full checks. The first tick after the wake starts a full check.
func (c *core) prepareSleep(now time.Time) {
	now = now.Round(0)
	c.startGeneration(now, "the computer goes to sleep", preBlockCauseSleep)
	c.sleepStart, c.isWakeNoted = now, false
}

// noteWake takes the message of the system about the end of a sleep. The next tick starts a full check.
func (c *core) noteWake() {
	if !c.sleepStart.IsZero() {
		c.isWakeNoted = true
	}
}

// changeNetwork starts a new generation and a full check at once. It is for the changes without a burst of messages:
// the start of the service, a wake, a resume, the end of a pause and a new public address.
func (c *core) changeNetwork(now time.Time, reason string) action {
	c.startGeneration(now, reason, "")
	return c.startCheck(reason)
}

// preBlockNetwork starts a new generation at the first change of the fingerprint. The full check waits for quiet.
// Each new change in the wait starts a new generation and moves the start of the check.
func (c *core) preBlockNetwork(now time.Time, reason string) {
	c.startGeneration(now, reason, "")
	c.pendingCheckAt, c.pendingCheckReason = now.Add(networkQuietDelay), reason
}

// startGeneration starts a new generation. Outside a pause, it blocks all providers until a full check passes.
// The cause goes to the event of the pre-block, so the log tells why the pre-block started.
func (c *core) startGeneration(now time.Time, reason, cause string) {
	c.generation++
	c.failures = 0
	c.pendingCheckAt = time.Time{}
	c.debug("generation %d: %s", c.generation, reason)
	if !c.state.Pause.IsOn && !c.state.IsPreBlocked {
		c.state.IsPreBlocked = true
		c.state.PreBlockSince = now
		c.addEvent(now, event{Kind: eventPreBlock, Cause: cause})
	}
}

// startCheck marks a full check of the current generation as started. A full check during a pre-block does preBlockRounds lookups. Other full checks do steadyRounds lookups.
func (c *core) startCheck(reason string) action {
	c.checkGeneration = c.generation
	rounds := steadyRounds
	if c.state.IsPreBlocked {
		rounds = preBlockRounds
	}
	return action{Kind: actionFullCheck, Generation: c.generation, Rounds: rounds, Reason: reason}
}

// requestCheck starts a full check, if no full check of the current generation runs.
func (c *core) requestCheck(reason string) []action {
	if c.checkGeneration == c.generation {
		c.debug("no new full check for %q, because a full check of generation %d runs", reason, c.generation)
		return nil
	}
	return []action{c.startCheck(reason)}
}

// handleCheck takes the result of a full check. A result of an older generation is not valid, because the network changed.
func (c *core) handleCheck(now time.Time, generation int, result geocheck.Result, lookupErr error) {
	now = now.Round(0)
	if !c.sleepStart.IsZero() {
		// The computer goes to sleep and can wake in another country. Only a check after the wake can remove the block.
		c.debug("ignore the full check of generation %d, because the computer goes to sleep", generation)
		return
	}
	if generation != c.generation || c.checkGeneration != generation {
		c.debug("ignore the full check of generation %d, because the current generation is %d", generation, c.generation)
		return
	}
	c.checkGeneration = 0
	if lookupErr != nil {
		c.failures++
		delay := getRetryDelay(c.failures, c.state.IsPreBlocked)
		c.nextRetry = now.Add(delay)
		c.state.LastFailure = checkFailure{Time: now, Error: lookupErr.Error()}
		c.debug("failed full checks in a row: %d. The next retry is in %v. Pre-block: %v", c.failures, delay, c.state.IsPreBlocked)
		if !c.state.IsPreBlocked {
			// The network did not change, and the full check did steadyRounds lookups.
			c.applyVerdicts(now, c.getVerdicts(geocheck.Result{}, lookupErr))
		}
		return
	}
	c.failures = 0
	c.state.LastFailure = checkFailure{}
	c.state.LastFullCheck = now
	c.state.LastResult = result
	c.state.Country = strings.Join(result.Countries(), " / ")
	if ip := getResultIP(result); ip != "" {
		c.state.PublicIP = ip
	}
	c.applyVerdicts(now, c.getVerdicts(result, nil))
	if c.state.IsPreBlocked {
		c.state.IsPreBlocked = false
		c.state.PreBlockSince = time.Time{}
		c.addEvent(now, event{Kind: eventPreBlockEnd})
	}
}

// handleProbe takes the result of a probe. The probe can make the result stricter, but it cannot remove a block.
// The first location is the reply of the probe service. The next location is the opinion of the local database, if it has one.
func (c *core) handleProbe(now time.Time, generation int, locations []geocheck.Location, probeErr error) []action {
	now = now.Round(0)
	c.isProbeRunning = false
	if generation != c.generation {
		c.debug("ignore the probe of generation %d, because the current generation is %d", generation, c.generation)
		return nil
	}
	if probeErr == nil && len(locations) == 0 {
		probeErr = errors.New("the probe has no reply")
	}
	if probeErr != nil {
		// A failed probe is not a change of the network, and it does not block. Only a failed full check blocks.
		if c.checkGeneration != 0 || c.failures > 0 || now.Sub(c.lastProbeFailureCheck) < probeFailureCheckInterval {
			return nil
		}
		c.lastProbeFailureCheck = now
		return []action{c.startCheck("failed probe")}
	}
	if ip := locations[0].IP; ip != "" {
		if c.state.PublicIP != "" && !isSameNetwork(c.state.PublicIP, ip) {
			// Move the known address at once. Otherwise the next probe sees the same change again and stops the full check.
			reason := fmt.Sprintf("public address %s -> %s", c.state.PublicIP, ip)
			c.state.PublicIP = ip
			return []action{c.changeNetwork(now, reason)}
		}
		if c.state.PublicIP == "" {
			c.state.PublicIP = ip
		}
	}
	// The trace and the local database give no region. The probe uses the region that the last full check gives
	// for the same country. Without it, each probe in Ukraine gives RegionUnknown and blocks again.
	probeResult := geocheck.Result{Locations: slices.Clone(locations)}
	isNewCountry := false
	for index, location := range probeResult.Locations {
		if location.Region == "" {
			probeResult.Locations[index].Region = c.state.LastResult.Region(location.Country)
		}
		isNewCountry = isNewCountry || !hasCountry(c.state.LastResult, location.Country)
	}
	countries := strings.Join(probeResult.Countries(), " / ")
	stricterVerdicts := c.getStricterVerdicts(probeResult)
	if len(stricterVerdicts) > 0 {
		c.debug("the probe in %s does not allow %d providers, so the service blocks them at once", countries, len(stricterVerdicts))
		c.applyVerdicts(now, stricterVerdicts)
	}
	// After a failed full check, only the retry schedule starts a full check. Otherwise each probe goes around the slow retries.
	if (len(stricterVerdicts) > 0 || isNewCountry) && c.failures == 0 {
		return c.requestCheck("probe in " + countries)
	}
	return nil
}

// handleDBBlock takes the opinion of the local database during a full check. The rules do not allow its country,
// so the service blocks at once. The result of the full check comes later and applies the strict rule to all replies.
// The opinion can only add a block. A country without a region never blocks here, because the database gives no region.
func (c *core) handleDBBlock(now time.Time, generation int, location geocheck.Location) {
	now = now.Round(0)
	if generation != c.generation || c.checkGeneration != generation {
		c.debug("ignore the opinion of the local database of generation %d, because the current generation is %d", generation, c.generation)
		return
	}
	stricterVerdicts := c.getStricterVerdicts(geocheck.Result{Locations: []geocheck.Location{location}})
	for provider, verdict := range stricterVerdicts {
		if verdict.Kind != geocheck.NotSupported && verdict.Kind != geocheck.Blocked {
			delete(stricterVerdicts, provider)
		}
	}
	if len(stricterVerdicts) > 0 {
		c.debug("the local database gives %s during the full check, so the service blocks %d providers at once", location.Country, len(stricterVerdicts))
		c.applyVerdicts(now, stricterVerdicts)
	}
}

// getStricterVerdicts returns the verdicts that do not allow a provider that is not blocked now.
func (c *core) getStricterVerdicts(result geocheck.Result) map[geocheck.Provider]geocheck.Verdict {
	stricterVerdicts := map[geocheck.Provider]geocheck.Verdict{}
	for _, provider := range geocheck.Providers() {
		verdict := geocheck.Check(provider, result, nil, c.blockedCountries)
		if !verdict.Allowed() && !c.state.provider(provider).IsBlocked {
			stricterVerdicts[provider] = verdict
		}
	}
	return stricterVerdicts
}

// pause starts a pause. A zero until means a pause with no end. The pause removes the block at once.
func (c *core) pause(now time.Time, until time.Time, user string) {
	now = now.Round(0)
	c.state.Pause = pauseState{IsOn: true, Since: now, Until: until.Round(0), User: user}
	c.state.IsPreBlocked = false
	c.state.PreBlockSince = time.Time{}
	c.addEvent(now, event{Kind: eventPause, Until: c.state.Pause.Until, User: user})
}

// resume stops the pause. Then the service blocks all providers until a full check passes.
func (c *core) resume(now time.Time) []action {
	now = now.Round(0)
	if !c.state.Pause.IsOn {
		return nil
	}
	c.endPause(now)
	return []action{c.changeNetwork(now, "resume")}
}

// endPause stops the pause. The block of the last verdicts applies again, so it adds a block event for them.
func (c *core) endPause(now time.Time) {
	c.state.Pause = pauseState{}
	c.addEvent(now, event{Kind: eventPauseEnd})
	blockEvent := event{Kind: eventBlock, Reasons: map[geocheck.Provider]string{}}
	for _, provider := range geocheck.Providers() {
		state := c.state.provider(provider)
		if state.IsBlocked {
			addToEvent(&blockEvent, provider, state)
		}
	}
	if len(blockEvent.Providers) > 0 {
		c.addEvent(now, blockEvent)
	}
}

// addError adds an event for an error of the driver, for example a failed change of the hosts file.
func (c *core) addError(now time.Time, message string) {
	c.addEvent(now.Round(0), event{Kind: eventError, Error: message})
}

// getVerdicts applies the strict rule of geocheck for each provider.
func (c *core) getVerdicts(result geocheck.Result, lookupErr error) map[geocheck.Provider]geocheck.Verdict {
	verdicts := map[geocheck.Provider]geocheck.Verdict{}
	for _, provider := range geocheck.Providers() {
		verdicts[provider] = geocheck.Check(provider, result, lookupErr, c.blockedCountries)
	}
	return verdicts
}

// applyVerdicts saves the verdicts. It adds 1 event for the providers that are now blocked,
// and 1 event for the providers that are now allowed. During a pause, the events tell about the verdicts only.
func (c *core) applyVerdicts(now time.Time, verdicts map[geocheck.Provider]geocheck.Verdict) {
	blockEvent := event{Kind: eventBlock, Reasons: map[geocheck.Provider]string{}}
	unblockEvent := event{Kind: eventUnblock, Reasons: map[geocheck.Provider]string{}}
	if c.state.Pause.IsOn {
		blockEvent.Kind, unblockEvent.Kind = eventPauseBlock, eventPauseUnblock
	}
	for _, provider := range geocheck.Providers() {
		verdict, hasVerdict := verdicts[provider]
		if !hasVerdict {
			continue
		}
		state := c.state.provider(provider)
		isBlocked := !verdict.Allowed()
		state.Verdict = verdict.Kind.String()
		state.Country = verdict.Location.Country
		state.Reason = verdict.ReasonWithReplies()
		if isBlocked == state.IsBlocked {
			continue
		}
		state.IsBlocked = isBlocked
		state.Since = now
		if isBlocked {
			addToEvent(&blockEvent, provider, state)
		} else {
			addToEvent(&unblockEvent, provider, state)
		}
	}
	for _, change := range []event{blockEvent, unblockEvent} {
		if len(change.Providers) > 0 {
			c.addEvent(now, change)
		}
	}
}

// addToEvent adds the provider to the event. The first provider gives the country and the kind of the verdict.
func addToEvent(change *event, provider geocheck.Provider, state *providerState) {
	if len(change.Providers) == 0 {
		change.Country, change.Verdict = state.Country, state.Verdict
	}
	change.Providers = append(change.Providers, provider)
	change.Reasons[provider] = state.Reason
}

// addEvent gives the event the next sequence number and keeps only the last maxEvents events.
func (c *core) addEvent(now time.Time, change event) {
	c.state.LastEventSeq++
	change.Seq = c.state.LastEventSeq
	change.Time = now
	c.state.Events = append(c.state.Events, change)
	if len(c.state.Events) > maxEvents {
		c.state.Events = slices.Clone(c.state.Events[len(c.state.Events)-maxEvents:])
	}
}

// getResultIP returns the first public IP address of the replies. Now only the cloudflare service gives it.
func getResultIP(result geocheck.Result) string {
	for _, location := range result.Locations {
		if location.IP != "" {
			return location.IP
		}
	}
	return ""
}

// hasCountry tells if a reply of the result has the country.
func hasCountry(result geocheck.Result, country string) bool {
	return slices.ContainsFunc(result.Locations, func(location geocheck.Location) bool { return location.Country == country })
}

// isSameNetwork tells if 2 public addresses are in the same network.
// For IPv6, it compares the /64 prefixes, because the system changes the temporary IPv6 addresses often.
// Addresses of different families cannot show a change, so the result is true.
func isSameNetwork(first, second string) bool {
	firstAddress, firstErr := netip.ParseAddr(first)
	secondAddress, secondErr := netip.ParseAddr(second)
	if firstErr != nil || secondErr != nil {
		return first == second
	}
	firstAddress, secondAddress = firstAddress.Unmap(), secondAddress.Unmap()
	if firstAddress.Is4() != secondAddress.Is4() {
		return true
	}
	if firstAddress.Is4() {
		return firstAddress == secondAddress
	}
	firstPrefix, _ := firstAddress.Prefix(64)
	secondPrefix, _ := secondAddress.Prefix(64)
	return firstPrefix == secondPrefix
}
