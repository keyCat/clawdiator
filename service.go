package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"syscall"
	"time"

	"geocheck"
)

const (
	// tickInterval is the time between 2 passes of the loop.
	tickInterval = 2 * time.Second
	// saveInterval is the maximum time between 2 saves of state.json. The agent watches the heartbeat in the file.
	saveInterval = 30 * time.Second
	// userCheckInterval is the minimum time between 2 full checks that a user starts.
	// Any user can send the check command, so the limit protects the request limits of the location services.
	userCheckInterval = 30 * time.Second
)

// probeDone and checkDone carry the results of the goroutines back to the loop.
type probeDone struct {
	Generation int
	Location   geocheck.Location
	Err        error
}

type checkDone struct {
	Generation int
	Result     geocheck.Result
	Err        error
}

// serviceDriver connects the core to the system. Only the goroutine of run calls its methods,
// except the goroutines of the probes and the full checks. These send their results over the results channel.
type serviceDriver struct {
	core  *core
	paths appPaths
	// checkOptions are the options of the full check. The driver sets the rounds of each check.
	checkOptions geocheck.Options

	now            func() time.Time
	lookup         func(context.Context, geocheck.Options) (geocheck.Result, error)
	getFingerprint func() string
	writeHosts     func(hosts []string) (bool, error)
	flushDNS       func()

	ctx         context.Context
	results     chan any
	requests    chan request
	cancelCheck context.CancelFunc

	appliedHosts  []string
	isApplied     bool
	lastHostsErr  string
	lastSave      time.Time
	savedSeq      int64
	loggedSeq     int64
	lastUserCheck time.Time
}

// newServiceDriver returns a driver that uses the real system. The tests replace the functions.
func newServiceDriver(ctx context.Context, c *core, paths appPaths, checkOptions geocheck.Options) *serviceDriver {
	return &serviceDriver{
		core:           c,
		paths:          paths,
		checkOptions:   checkOptions,
		now:            time.Now,
		lookup:         geocheck.Lookup,
		getFingerprint: getNetworkFingerprint,
		writeHosts:     func(hosts []string) (bool, error) { return writeHostsFile(paths.Hosts, hosts) },
		flushDNS:       flushDNS,
		ctx:            ctx,
		results:        make(chan any, 8),
		requests:       make(chan request),
		savedSeq:       c.state.LastEventSeq,
		loggedSeq:      c.state.LastEventSeq,
	}
}

// run is the loop of the service. It never waits for the network: the probes and the full checks run in other goroutines.
// A message of the system about a change of the network starts a pass at once, without a wait for the next tick.
// A nil channel of network changes never sends, so then only the ticks start the passes.
func (d *serviceDriver) run(ticks <-chan time.Time, networkChanges <-chan struct{}) {
	for {
		select {
		case <-d.ctx.Done():
			if d.cancelCheck != nil {
				d.cancelCheck()
			}
			d.afterStep(true)
			return
		case <-ticks:
			d.handleTick()
		case <-networkChanges:
			d.handleTick()
		case result := <-d.results:
			d.handleResult(result)
		case next := <-d.requests:
			next.Reply <- d.handleRequest(next.commandRequest)
		}
		d.afterStep(false)
	}
}

func (d *serviceDriver) handleTick() {
	d.startActions(d.core.tick(d.now(), d.getFingerprint()))
}

func (d *serviceDriver) handleResult(result any) {
	now := d.now()
	switch done := result.(type) {
	case probeDone:
		d.startActions(d.core.handleProbe(now, done.Generation, done.Location, done.Err))
	case checkDone:
		d.core.handleCheck(now, done.Generation, done.Result, done.Err)
	}
}

func (d *serviceDriver) handleRequest(received commandRequest) commandResponse {
	now := d.now()
	switch received.Command {
	case socketCheck:
		if !d.lastUserCheck.IsZero() && now.Sub(d.lastUserCheck) < userCheckInterval {
			return commandResponse{Code: codeTooSoon}
		}
		d.lastUserCheck = now
		d.startActions(d.core.requestCheck())
	case socketPause:
		until := received.Until
		if received.IsForever {
			until = time.Time{}
		} else if !until.After(now) {
			return commandResponse{Code: codeBadRequest}
		}
		d.core.pause(now, until, received.User)
	case socketResume:
		if !d.core.state.Pause.IsOn {
			return commandResponse{Code: codeNoPause}
		}
		d.startActions(d.core.resume(now))
	default:
		return commandResponse{Code: codeNotAllowed}
	}
	return commandResponse{Code: codeOK}
}

// startActions starts a goroutine for each action. A new full check stops a running full check of an older generation.
func (d *serviceDriver) startActions(actions []action) {
	for _, next := range actions {
		switch next.Kind {
		case actionProbe:
			go d.runProbe(next.Generation)
		case actionFullCheck:
			if d.cancelCheck != nil {
				d.cancelCheck()
			}
			ctx, cancel := context.WithCancel(d.ctx)
			d.cancelCheck = cancel
			go d.runCheck(ctx, next)
		}
	}
}

func (d *serviceDriver) runProbe(generation int) {
	result, err := d.lookup(d.ctx, getProbeOptions())
	done := probeDone{Generation: generation, Err: err}
	if err == nil && len(result.Locations) > 0 {
		done.Location = result.Locations[0]
	}
	d.sendResult(done)
}

func (d *serviceDriver) runCheck(ctx context.Context, next action) {
	options := d.checkOptions
	options.Rounds = next.Rounds
	result, err := d.lookup(ctx, options)
	d.sendResult(checkDone{Generation: next.Generation, Result: result, Err: err})
}

// sendResult gives the result to the loop. After the end of the service, nobody reads the channel, so the goroutine stops.
func (d *serviceDriver) sendResult(result any) {
	select {
	case d.results <- result:
	case <-d.ctx.Done():
	}
}

// afterStep applies the block, writes the log and saves the state. With isFinal, it always saves the state.
func (d *serviceDriver) afterStep(isFinal bool) {
	now := d.now()
	d.applyBlock(now)
	d.writeLog()
	state := d.core.state
	if isFinal || state.LastEventSeq != d.savedSeq || now.Sub(d.lastSave) >= saveInterval {
		if err := saveServiceState(d.paths.State, state); err == nil {
			d.savedSeq = state.LastEventSeq
			d.lastSave = now
		}
	}
}

// applyBlock writes the hosts of the blocked providers to the hosts file, if they changed.
// After a failure, it tries again at each pass. It adds an error event only when the error changes.
func (d *serviceDriver) applyBlock(now time.Time) {
	hosts := getBlockedHosts(d.core.state.blockedProviders())
	if d.isApplied && slices.Equal(hosts, d.appliedHosts) {
		return
	}
	isChanged, err := d.writeHosts(hosts)
	if err != nil {
		if message := "cannot change the hosts file: " + err.Error(); message != d.lastHostsErr {
			d.lastHostsErr = message
			d.core.addError(now, message)
		}
		return
	}
	d.lastHostsErr = ""
	d.appliedHosts, d.isApplied = hosts, true
	if isChanged {
		d.flushDNS()
	}
}

// writeLog writes the log lines of the new events.
func (d *serviceDriver) writeLog() {
	var lines []string
	for _, change := range d.core.state.Events {
		if change.Seq > d.loggedSeq {
			lines = append(lines, formatEventLog(change)...)
		}
	}
	if appendLog(d.paths.Log, lines) == nil {
		d.loggedSeq = d.core.state.LastEventSeq
	}
}

// flushDNS runs the flush commands of the system. A missing command is not an error, so the function ignores the errors.
func flushDNS() {
	for _, command := range getDNSFlushCommands(runtime.GOOS) {
		_ = exec.Command(command[0], command[1:]...).Run()
	}
}

// commandService is the start point of the service. The service manager starts it as root.
// The service stops when the service manager sends SIGTERM. The block of that time stays.
func commandService(paths appPaths) error {
	// The log is always in English, so the service writes its errors in English too.
	currentLanguage = languageEnglish
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runService(ctx, paths)
}

// runService runs the service until the context ends.
// The service does not stop for a bad config or a damaged state, because each start blocks until a full check passes.
// A crash loop would then block all the time.
func runService(ctx context.Context, paths appPaths) error {
	if err := makeFolder(paths.Dir, 0o755); err != nil {
		return err
	}
	release, err := lockService(paths.Lock)
	if err != nil {
		return fmt.Errorf("another copy of the service runs: %w", err)
	}
	defer release()

	var startErrors []string
	cfg, err := loadConfig(paths.Config)
	if err != nil {
		startErrors = append(startErrors, "cannot read the config, so the service uses the defaults: "+err.Error())
		cfg = &Config{}
	}
	checkOptions, err := getGeoOptions(cfg)
	if err != nil {
		startErrors = append(startErrors, "the config is not correct, so the service uses the defaults: "+err.Error())
		cfg = &Config{}
		checkOptions, _ = getGeoOptions(cfg)
	}
	state, err := loadServiceState(paths.State)
	if err != nil {
		startErrors = append(startErrors, "cannot read the state, so the service starts with an empty state: "+err.Error())
		state = &serviceState{}
	}
	c := newCore(state, cfg.BlockedCountries)
	driver := newServiceDriver(ctx, c, paths, checkOptions)
	for _, message := range startErrors {
		c.addError(time.Now(), message)
	}
	checkListener, controlListener, err := listenSockets(paths)
	if err != nil {
		c.addError(time.Now(), "cannot open the sockets, so the check, pause and resume commands do not work: "+err.Error())
	} else {
		go serveCommands(ctx, checkListener, []string{socketCheck}, driver.requests)
		go serveCommands(ctx, controlListener, []string{socketPause, socketResume}, driver.requests)
	}
	// The first pass applies the pre-block at once. The ticker gives the next pass after 2 seconds.
	driver.handleTick()
	driver.afterStep(true)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	driver.run(ticker.C, watchNetwork(ctx))
	return nil
}
