package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strings"
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
// The duration goes to the verbose log.
type probeDone struct {
	Generation int
	// Locations holds the reply of the probe service first. Then it holds the opinion of the local database, if it has one.
	Locations []geocheck.Location
	Err       error
	// DBErr tells why the local database gave no opinion. The probe does not fail for it.
	DBErr    error
	Duration time.Duration
}

type checkDone struct {
	Generation int
	Result     geocheck.Result
	Err        error
	Duration   time.Duration
	// FirstErr is the error of the first lookup, if the check asked the services again without the local database.
	FirstErr error
}

// dbBlockDone carries the opinion of the local database during a full check. The rules do not allow its country.
type dbBlockDone struct {
	Generation int
	Location   geocheck.Location
}

// dbDone is the result of a download of the local database.
type dbDone struct {
	Month    string
	DB       *countryDB
	Err      error
	Duration time.Duration
}

// serviceDriver connects the core to the system. Only the goroutine of run calls its methods,
// except the goroutines of the probes, the full checks and the downloads. These send their results over the results channel.
type serviceDriver struct {
	core  *core
	paths appPaths
	// checkOptions are the options of the full check. The driver sets the rounds of each check.
	checkOptions geocheck.Options

	now            func() time.Time
	lookup         func(context.Context, geocheck.Options) (geocheck.Result, error)
	getFingerprint func() string
	writeHosts     func(hosts []string) (bool, error)
	// flushDNS returns the result of each flush command for the verbose log.
	flushDNS func() string
	// spawn starts a goroutine. The tests replace it to see the order of the steps.
	spawn func(task func())
	// downloadDB downloads the file of the local database for the month, checks it and saves it.
	downloadDB func(ctx context.Context, month string) (*countryDB, error)

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

	// countryDB is the local database. It is nil if the service has no correct file.
	countryDB           *countryDB
	isDBDownloadRunning bool
	lastDBDownload      time.Time
}

// newServiceDriver returns a driver that uses the real system. The tests replace the functions.
func newServiceDriver(ctx context.Context, c *core, paths appPaths, checkOptions geocheck.Options) *serviceDriver {
	driver := &serviceDriver{
		core:           c,
		paths:          paths,
		checkOptions:   checkOptions,
		now:            time.Now,
		lookup:         geocheck.Lookup,
		getFingerprint: getNetworkFingerprint,
		writeHosts:     func(hosts []string) (bool, error) { return writeHostsFile(paths.Hosts, hosts) },
		flushDNS:       flushDNS,
		spawn:          func(task func()) { go task() },
		ctx:            ctx,
		results:        make(chan any, 8),
		requests:       make(chan request),
		savedSeq:       c.state.LastEventSeq,
		loggedSeq:      c.state.LastEventSeq,
	}
	driver.downloadDB = newDBDownloader(paths.Dir)
	c.debugf = func(format string, args ...any) { driver.debugf("core", format, args...) }
	return driver
}

// debugf writes 1 line to the verbose log. Only the goroutine of the loop calls it, so the lines do not mix.
// The verbose log is a superset of the normal log: writeLog also writes the event lines to it.
func (d *serviceDriver) debugf(component, format string, args ...any) {
	line := d.now().Round(0).Format(debugTimeFormat) + " " + component + " " + cleanLogText(fmt.Sprintf(format, args...))
	_ = appendLogLimit(d.paths.DebugLog, []string{line}, maxDebugLogSize)
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
			d.debugf("network", "the system sent a message about a change of an interface or an address")
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
	d.startDBDownload()
}

// startDBDownload starts a download of the local database, if the service has no file of the current month.
// After a failure, it waits dbRetryInterval. For example, DB-IP can publish the file of the month some hours after its start.
func (d *serviceDriver) startDBDownload() {
	// The wall clock also counts the sleep of the computer. The monotonic clock stops during the sleep.
	now := d.now().Round(0)
	if d.isDBDownloadRunning || !isDBDownloadDue(d.countryDB, now) || (!d.lastDBDownload.IsZero() && now.Sub(d.lastDBDownload) < dbRetryInterval) {
		return
	}
	d.isDBDownloadRunning, d.lastDBDownload = true, now
	month, hasUsableFile := getDBMonth(now), d.countryDB.isUsable(now)
	d.debugf("dbip", "download the file of %s. The current file: %s", month, d.countryDB.describe())
	d.spawn(func() {
		start := time.Now()
		db, err := downloadNewestDB(d.ctx, d.downloadDB, now, hasUsableFile)
		d.sendResult(dbDone{Month: month, DB: db, Err: err, Duration: time.Since(start)})
	})
}

// setCountryDB starts to use the local database. The status command shows it.
func (d *serviceDriver) setCountryDB(db *countryDB) {
	d.countryDB = db
	d.core.state.LocalDB = localDBState{}
	if db != nil {
		d.core.state.LocalDB = localDBState{File: getDBFileName(db.month), BuildTime: db.buildTime}
	}
}

// getUsableDB returns the local database, or nil if the data is too old.
func (d *serviceDriver) getUsableDB(now time.Time) *countryDB {
	if !d.countryDB.isUsable(now) {
		return nil
	}
	return d.countryDB
}

func (d *serviceDriver) handleResult(result any) {
	now := d.now()
	switch done := result.(type) {
	case probeDone:
		if done.Err != nil {
			d.debugf("probe", "generation %d failed in %v: %v", done.Generation, done.Duration.Round(time.Millisecond), done.Err)
		} else {
			d.debugf("probe", "generation %d in %v: %s, address %s", done.Generation, done.Duration.Round(time.Millisecond),
				geocheck.Result{Locations: done.Locations}.Describe(), done.Locations[0].IP)
		}
		if done.DBErr != nil {
			d.debugf("probe", "generation %d: the local database gave no country: %v", done.Generation, done.DBErr)
		}
		d.startActions(d.core.handleProbe(now, done.Generation, done.Locations, done.Err))
	case checkDone:
		if done.FirstErr != nil {
			d.debugf("check", "generation %d: the local database gave no country, so the check asked the services again with the full minimum: %v", done.Generation, done.FirstErr)
		}
		if done.Err != nil {
			d.debugf("check", "generation %d failed in %v: %v", done.Generation, done.Duration.Round(time.Millisecond), done.Err)
		} else {
			d.debugf("check", "generation %d passed in %v: %s", done.Generation, done.Duration.Round(time.Millisecond), done.Result.Describe())
		}
		// Log a failed full check of the current generation. The error names each service and its failure.
		// A new generation stops the old check, so a stopped check always has an older generation. It is not a failure.
		if done.Err != nil && done.Generation == d.core.generation {
			_ = appendLog(d.paths.Log, []string{now.Round(0).Format(time.RFC3339) + " all check-failed " + cleanLogText(done.Err.Error())})
		}
		d.core.handleCheck(now, done.Generation, done.Result, done.Err)
	case dbDone:
		d.isDBDownloadRunning = false
		if done.Err != nil {
			d.debugf("dbip", "the download of %s failed in %v: %v. The next try is in %v", done.Month, done.Duration.Round(time.Millisecond), done.Err, dbRetryInterval)
			return
		}
		d.setCountryDB(done.DB)
		d.debugf("dbip", "the download passed in %v: %s", done.Duration.Round(time.Millisecond), done.DB.describe())
	case dbBlockDone:
		d.debugf("check", "generation %d: the local database gives %s for %s. The rules do not allow it, so the service blocks at once and waits for all services",
			done.Generation, done.Location.Country, done.Location.IP)
		d.core.handleDBBlock(now, done.Generation, done.Location)
	}
}

func (d *serviceDriver) handleRequest(received commandRequest) commandResponse {
	response := d.answerRequest(received)
	d.debugf("command", "%s from user %q: %s", received.Command, received.User, response.Code)
	return response
}

func (d *serviceDriver) answerRequest(received commandRequest) commandResponse {
	now := d.now()
	switch received.Command {
	case socketCheck:
		if !d.lastUserCheck.IsZero() && now.Sub(d.lastUserCheck) < userCheckInterval {
			return commandResponse{Code: codeTooSoon}
		}
		d.lastUserCheck = now
		d.startActions(d.core.requestCheck("user command"))
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
// It applies the block first. Then a pre-block is in the hosts file before any request of the new network,
// and the flush of the DNS cache comes before the lookups of the check.
func (d *serviceDriver) startActions(actions []action) {
	if len(actions) == 0 {
		return
	}
	now := d.now()
	d.applyBlock(now)
	db, blockedCountries := d.getUsableDB(now), d.core.blockedCountries
	for _, next := range actions {
		switch next.Kind {
		case actionProbe:
			generation := next.Generation
			d.spawn(func() { d.runProbe(generation, db) })
		case actionFullCheck:
			if d.cancelCheck != nil {
				d.cancelCheck()
			}
			ctx, cancel := context.WithCancel(d.ctx)
			d.cancelCheck = cancel
			dbText := "none"
			if db != nil {
				dbText = db.describe()
			} else if d.countryDB != nil {
				dbText = d.countryDB.describe() + ", too old"
			}
			d.debugf("check", "start generation %d, %d rounds, reason: %s, local database: %s", next.Generation, next.Rounds, next.Reason, dbText)
			check := next
			d.spawn(func() { d.runCheck(ctx, check, db, blockedCountries) })
		}
	}
}

// runProbe asks the probe service. The local database adds its opinion about the address of the reply.
// The probe uses the database the same way as the full check. Otherwise the probe and the full check can disagree,
// and the block starts and stops every 30 seconds.
func (d *serviceDriver) runProbe(generation int, db *countryDB) {
	start := time.Now()
	result, err := d.lookup(d.ctx, getProbeOptions())
	done := probeDone{Generation: generation, Err: err}
	if err == nil && len(result.Locations) == 0 {
		done.Err = errors.New("the probe service gave no reply")
	}
	if done.Err == nil {
		location := result.Locations[0]
		done.Locations = []geocheck.Location{location}
		if db != nil {
			// With 1 online reply and a minimum of 1, addDBLocations cannot fail. A missing opinion is in the failures.
			withDB, _ := addDBLocations(geocheck.Result{Locations: done.Locations}, db, 1)
			done.Locations = withDB.Locations
			if len(withDB.Failures) > 0 {
				done.DBErr = errors.New(strings.Join(withDB.Failures, "; "))
			}
		}
	}
	done.Duration = time.Since(start)
	d.sendResult(done)
}

// runCheck asks the services of the full check. With the local database, the online services must give 1 reply less.
// The database can give no opinion, for example if cloudflare fails. Then the first lookup can stop a slow service
// that the full minimum needs. So the check asks the services again with the full minimum.
func (d *serviceDriver) runCheck(ctx context.Context, next action, db *countryDB, blockedCountries []string) {
	options := d.checkOptions
	options.Rounds = next.Rounds
	options.OnReply = d.newReplyHook(next.Generation, db, blockedCountries)
	if db != nil {
		options.MinReplies = max(1, options.MinReplies-1)
	}
	start := time.Now()
	done := checkDone{Generation: next.Generation}
	done.Result, done.Err = d.lookup(ctx, options)
	if done.Err == nil {
		done.Result, done.Err = addDBLocations(done.Result, db, d.checkOptions.MinReplies)
		if done.Err != nil {
			done.FirstErr = done.Err
			options.MinReplies = d.checkOptions.MinReplies
			done.Result, done.Err = d.lookup(ctx, options)
			if done.Err == nil {
				done.Result, done.Err = addDBLocations(done.Result, db, d.checkOptions.MinReplies)
			}
		}
	}
	done.Duration = time.Since(start)
	d.sendResult(done)
}

// newReplyHook returns the OnReply function of a full check. It runs for each reply when the reply comes.
// The local database looks up the address of the reply at once. If the rules do not allow the country of the database,
// the service blocks at once. The full check then waits for all services, and the strict rule applies to all replies.
// The full check also waits for all services if a reply is not allowed. A slow service can give the region of a country
// with excluded regions, for example Ukraine. In the other cases, the extra wait stops the slow services.
func (d *serviceDriver) newReplyHook(generation int, db *countryDB, blockedCountries []string) func(geocheck.Location) bool {
	var addresses []string
	return func(location geocheck.Location) bool {
		locations := []geocheck.Location{location}
		if db != nil && location.IP != "" && !slices.Contains(addresses, location.IP) {
			addresses = append(addresses, location.IP)
			if country, err := db.lookupCountry(location.IP); err == nil {
				opinion := geocheck.Location{Country: country, IP: location.IP, Source: dbSource}
				locations = append(locations, opinion)
				if isCountryNotAllowed(opinion, blockedCountries) {
					d.sendResult(dbBlockDone{Generation: generation, Location: opinion})
				}
			}
		}
		return slices.ContainsFunc(locations, func(next geocheck.Location) bool { return !isLocationAllowed(next, blockedCountries) })
	}
}

// isCountryNotAllowed tells if the rules do not allow the country for a provider. It does not use the region.
func isCountryNotAllowed(location geocheck.Location, blockedCountries []string) bool {
	for _, provider := range geocheck.Providers() {
		switch geocheck.CheckLocation(provider, location, blockedCountries).Kind {
		case geocheck.NotSupported, geocheck.Blocked:
			return true
		}
	}
	return false
}

// isLocationAllowed tells if the rules allow the location for all providers. A country with excluded regions needs the region.
func isLocationAllowed(location geocheck.Location, blockedCountries []string) bool {
	for _, provider := range geocheck.Providers() {
		if !geocheck.CheckLocation(provider, location, blockedCountries).Allowed() {
			return false
		}
	}
	return true
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
		if err := saveServiceState(d.paths.State, state); err != nil {
			d.debugf("state", "cannot save %s: %v", d.paths.State, err)
		} else {
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
		d.debugf("hosts", "cannot write %d hosts: %v", len(hosts), err)
		if message := "cannot change the hosts file: " + err.Error(); message != d.lastHostsErr {
			d.lastHostsErr = message
			d.core.addError(now, message)
		}
		return
	}
	d.lastHostsErr = ""
	d.appliedHosts, d.isApplied = hosts, true
	providers := getProviderNames(d.core.state.blockedProviders())
	if providers == "" {
		providers = "none"
	}
	d.debugf("hosts", "blocked providers: %s (%d hosts). The file changed: %v", providers, len(hosts), isChanged)
	if isChanged {
		d.debugf("dns", "flush: %s", d.flushDNS())
	}
}

// writeLog writes the log lines of the new events.
func (d *serviceDriver) writeLog() {
	var lines, debugLines []string
	for _, change := range d.core.state.Events {
		if change.Seq > d.loggedSeq {
			lines = append(lines, formatEventLog(change)...)
			debugLines = append(debugLines, formatEventLogWith(change, debugTimeFormat)...)
		}
	}
	if appendLog(d.paths.Log, lines) == nil {
		d.loggedSeq = d.core.state.LastEventSeq
		_ = appendLogLimit(d.paths.DebugLog, debugLines, maxDebugLogSize)
	}
}

// flushDNS runs the flush commands of the system and returns the result of each command.
// A missing command is not an error, for example nscd on most Linux systems. So the service only logs the results.
func flushDNS() string {
	var results []string
	for _, command := range getDNSFlushCommands(runtime.GOOS) {
		result := "ok"
		if err := exec.Command(command[0], command[1:]...).Run(); err != nil {
			result = err.Error()
		}
		results = append(results, strings.Join(command, " ")+": "+result)
	}
	return strings.Join(results, "; ")
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
	db, dbErr := loadCountryDB(paths.Dir)
	driver.setCountryDB(db)
	driver.debugf("service", "start: process %d, services %s, minimum replies %d, blocked countries %v, last event %d, local database %s",
		os.Getpid(), strings.Join(geocheck.ServiceNames(checkOptions.Services), ","), checkOptions.MinReplies, cfg.BlockedCountries, state.LastEventSeq, db.describe())
	if dbErr != nil {
		driver.debugf("dbip", "cannot open a file: %v", dbErr)
	}
	for _, message := range startErrors {
		driver.debugf("service", "%s", message)
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
	networkChanges := watchNetwork(ctx)
	driver.debugf("service", "network messages of the system: %v", networkChanges != nil)
	driver.run(ticker.C, networkChanges)
	driver.debugf("service", "stop")
	return nil
}
