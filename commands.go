package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"geocheck"
)

// installer holds the parts of the installation that the tests replace.
type installer struct {
	paths      appPaths
	manager    serviceManager
	executable func() (string, error)
	isRoot     func() bool
	flushDNS   func()
	// consoleUserID returns the user at the screen. The installation starts the agents in the session of this user.
	consoleUserID func() int
	// downloadDB downloads the file of the local database for the month into the app folder.
	downloadDB func(ctx context.Context, month string) (*countryDB, error)
	// firewall controls the firewall rules. It is nil on the systems without these rules.
	firewall *pfFirewall
	// serviceStopTimeout is the maximum time to wait for the end of the service process.
	// launchd sends SIGKILL 20 seconds after SIGTERM, so 30 seconds is enough.
	serviceStopTimeout time.Duration
	now                func() time.Time
	user               string
	stdin              io.Reader
	stdout             io.Writer
}

func newInstaller(paths appPaths) *installer {
	return &installer{
		paths:              paths,
		manager:            launchdManager{},
		executable:         os.Executable,
		isRoot:             func() bool { return os.Geteuid() == 0 },
		flushDNS:           func() { _ = flushDNS() },
		consoleUserID:      getConsoleUserID,
		downloadDB:         newDBDownloader(paths.Dir),
		firewall:           newFirewall(),
		serviceStopTimeout: 30 * time.Second,
		now:                time.Now,
		user:               getUserName(),
		stdin:              os.Stdin,
		stdout:             os.Stdout,
	}
}

// getUserName returns the user who started sudo, or the current user.
func getUserName() string {
	if name := os.Getenv("SUDO_USER"); name != "" {
		return name
	}
	if current, err := user.Current(); err == nil {
		return current.Username
	}
	return ""
}

// commandInstall saves the settings and the consent, copies the binary and registers the service.
// If an option is not set, the value from config.json stays.
func (in *installer) commandInstall(args []string) error {
	if runtime.GOOS != "darwin" {
		return errorf(msgOnlyMacOS)
	}
	flags := newFlagSet("install")
	isYes := flags.Bool("yes", false, text(msgFlagYes))
	geoServices := flags.String("geo-services", "", text(msgFlagGeoServices, strings.Join(geocheck.DefaultServiceNames(), ",")))
	geoMinReplies := flags.Int("geo-min-replies", 0, text(msgFlagGeoMinReplies))
	blockedCountries := flags.String("blocked-countries", "", text(msgFlagBlockedCountries))
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !in.isRoot() {
		return errorf(msgNeedRoot, "install")
	}
	isSet := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) { isSet[flag.Name] = true })

	cfg, err := loadConfig(in.paths.Config)
	if errors.Is(err, os.ErrNotExist) {
		cfg, err = &Config{}, nil
	}
	if err != nil {
		return errorf(msgCannotReadConfig, in.paths.Config, err)
	}
	if isSet["geo-services"] {
		cfg.GeoServices = splitList(*geoServices, false)
	}
	if isSet["geo-min-replies"] {
		cfg.GeoMinReplies = *geoMinReplies
	}
	if isSet["blocked-countries"] {
		cfg.BlockedCountries = splitList(*blockedCountries, true)
	}
	if _, err := getGeoOptions(cfg); err != nil {
		return err
	}
	if cfg.Consent.Version < consentVersion {
		fmt.Fprint(in.stdout, text(msgConsentText))
		if !*isYes && !readYes(in.stdin) {
			return errorf(msgConsentDeclined)
		}
		cfg.Consent = consent{Time: in.now(), Version: consentVersion, User: in.user}
	}

	if err := makeRootFolder(in.paths.Dir); err != nil {
		return err
	}
	if err := in.installBinary(); err != nil {
		return errorf(msgCannotCopyBinary, err)
	}
	hasTray, err := in.installTrayBinary()
	if err != nil {
		return errorf(msgCannotCopyBinary, err)
	}
	dbFile, dbErr := in.installCountryDB()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRootFile(in.paths.Config, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := in.manager.Install(in.paths); err != nil {
		return errorf(msgCannotRegister, err)
	}
	fmt.Fprintln(in.stdout, text(msgInstalled, in.paths.Dir))
	// Without the file, the service downloads it. So a failed download is a warning, not an error.
	if dbErr != nil {
		fmt.Fprintln(in.stdout, text(msgLocalDBNotDownloaded, dbErr))
	} else {
		fmt.Fprintln(in.stdout, text(msgLocalDBReady, dbFile))
	}
	// Without the agents, the service still blocks. So a failure of the agents is a warning, not an error.
	if !hasTray {
		fmt.Fprintln(in.stdout, text(msgTrayMissing, trayBinaryName))
	}
	if err := in.manager.InstallAgents(in.paths, hasTray, in.consoleUserID()); err != nil {
		fmt.Fprintln(in.stdout, text(msgAgentsNotStarted, err))
	} else {
		fmt.Fprintln(in.stdout, text(msgAgentsStarted))
	}
	if err := createLink(in.paths.Link, in.paths.Binary); err != nil {
		fmt.Fprintln(in.stdout, text(msgLinkNotCreated, err))
	} else if in.paths.Link != "" {
		fmt.Fprintln(in.stdout, text(msgLinkCreated, in.paths.Link))
	}
	if len(cfg.BlockedCountries) > 0 {
		fmt.Fprintln(in.stdout, text(msgBlockedCountriesActive, strings.Join(cfg.BlockedCountries, ", ")))
	}
	fmt.Fprintln(in.stdout, text(msgHintStatus))
	return nil
}

// installBinary copies the running binary to the app folder. The service uses this copy.
func (in *installer) installBinary() error {
	source, err := in.executable()
	if err != nil {
		return err
	}
	if isSameFile(source, in.paths.Binary) {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := makeRootFolder(filepath.Dir(in.paths.Binary)); err != nil {
		return err
	}
	return writeRootFile(in.paths.Binary, data, 0o755)
}

// installTrayBinary copies the tray binary from the folder of the running binary. The release puts it there.
// The result tells if the tray binary is installed. A build without the tray binary is not an error.
func (in *installer) installTrayBinary() (bool, error) {
	source, err := in.executable()
	if err != nil {
		return false, err
	}
	trayPath := filepath.Join(filepath.Dir(source), trayBinaryName)
	if isSameFile(trayPath, in.paths.TrayBinary) {
		return true, nil
	}
	data, err := os.ReadFile(trayPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, writeRootFile(in.paths.TrayBinary, data, 0o755)
}

// installCountryDB downloads the file of the local database into the app folder. The service opens the file at its start,
// so the download comes before the registration of the service. A correct file of the current month stays,
// so an install for a change of the settings does not download again. The result is the name of the file in the app folder.
func (in *installer) installCountryDB() (string, error) {
	now := in.now()
	current, _ := loadCountryDB(in.paths.Dir)
	if current != nil && !isDBDownloadDue(current, now) {
		return getDBFileName(current.month), nil
	}
	fmt.Fprintln(in.stdout, text(msgLocalDBDownload))
	ctx, cancel := context.WithTimeout(context.Background(), installDBTimeout)
	defer cancel()
	db, err := downloadNewestDB(ctx, in.downloadDB, now, current.isUsable(now))
	if err != nil {
		return "", err
	}
	return getDBFileName(db.month), nil
}

// commandUninstall removes all changes. It continues after a failed step, so that it also works without the network
// and with a damaged installation. It stops the service first, so that the service cannot write the block again.
func (in *installer) commandUninstall() error {
	if !in.isRoot() {
		return errorf(msgNeedRoot, "uninstall")
	}
	isComplete := true
	report := func(step messageKey, err error) {
		if err != nil {
			isComplete = false
			fmt.Fprintln(in.stdout, text(msgStepFailed, text(step), err))
			return
		}
		fmt.Fprintln(in.stdout, text(msgStepDone, text(step)))
	}
	if in.paths.AgentPlist != "" {
		report(msgStepStopAgents, in.manager.UninstallAgents(in.paths, in.consoleUserID()))
	}
	if in.paths.Plist != "" {
		report(msgStepStopService, in.stopService())
	}
	// The token of the firewall is in state.json, so this step comes before the removal of the app folder.
	if in.firewall != nil {
		report(msgStepRemoveFirewall, in.removeFirewall())
	}
	isChanged, err := writeHostsFile(in.paths.Hosts, nil)
	if isChanged {
		in.flushDNS()
	}
	report(msgStepRemoveBlock, err)
	if in.paths.Link != "" {
		report(msgStepRemoveLink, removeLink(in.paths.Link, in.paths.Binary))
	}
	report(msgStepRemoveSockets, os.RemoveAll(in.paths.SocketDir))
	report(msgStepRemoveFolder, os.RemoveAll(in.paths.Dir))
	if in.paths.UsersDir != "" {
		kept, err := removeUserFiles(in.paths.UsersDir)
		report(msgStepRemoveUserFiles, err)
		for _, folder := range kept {
			if folder.Err != nil {
				fmt.Fprintln(in.stdout, text(msgUserFolderNotRemoved, folder.Path, folder.Err))
			} else {
				fmt.Fprintln(in.stdout, text(msgUserFolderKept, folder.Path))
			}
		}
	}
	if !isComplete {
		return errorf(msgUninstallIncomplete)
	}
	fmt.Fprintln(in.stdout, text(msgUninstalled))
	return nil
}

// stopService stops the service and waits for the end of its process, because launchctl bootout can return before the end.
// The service saves state.json at its end. Without the wait, it can write into the app folder that uninstall removes.
// The service holds its lock until the end of its process, so a free lock shows the end.
func (in *installer) stopService() error {
	if err := in.manager.Uninstall(in.paths); err != nil {
		return err
	}
	deadline := time.Now().Add(in.serviceStopTimeout)
	for {
		release, err := lockService(in.paths.Lock)
		if err == nil {
			release()
			return nil
		}
		// Without the app folder, no service can run.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the service process did not stop in %v: %w", in.serviceStopTimeout, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// commandRemoveBlock removes the clawdiator block from the hosts file without the service.
// It is the recovery when the service is gone, but the block stayed.
func (in *installer) commandRemoveBlock() error {
	if !in.isRoot() {
		return errorf(msgNeedRoot, "hosts --remove")
	}
	if state, err := loadServiceState(in.paths.State); err == nil && in.now().Sub(state.Heartbeat) < time.Minute {
		fmt.Fprintln(in.stdout, text(msgServiceWritesAgain))
	}
	isChanged, err := writeHostsFile(in.paths.Hosts, nil)
	if err != nil {
		return err
	}
	if isChanged {
		in.flushDNS()
		fmt.Fprintln(in.stdout, text(msgBlockRemoved, in.paths.Hosts))
	} else {
		fmt.Fprintln(in.stdout, text(msgNoBlock, in.paths.Hosts))
	}
	if in.firewall == nil {
		return nil
	}
	if err := in.removeFirewall(); err != nil {
		return err
	}
	fmt.Fprintln(in.stdout, text(msgFirewallRemoved, pfAnchor))
	return nil
}

// removeFirewall removes the firewall rules and releases the token from state.json. It works without the service.
// Without state.json, it only removes the rules.
func (in *installer) removeFirewall() error {
	token := ""
	if state, err := loadServiceState(in.paths.State); err == nil {
		token = state.Firewall.Token
	}
	return in.firewall.Unblock(token)
}

// commandStatus shows the state of the service. It needs no administrator rights: it reads state.json and the logs.
// The age of the heartbeat shows if the service works. launchctl can fail for a user who is not root.
// With --verbose, it shows the end of the verbose log instead of the end of the normal log.
func commandStatus(paths appPaths, args []string, now time.Time, stdout io.Writer) error {
	flags := newFlagSet("status")
	isVerbose := flags.Bool("verbose", false, text(msgFlagVerbose))
	lineCount := flags.Int("lines", 0, text(msgFlagLines))
	if err := flags.Parse(args); err != nil {
		return err
	}
	logPath, defaultLines := paths.Log, 10
	if *isVerbose {
		logPath, defaultLines = paths.DebugLog, 50
	}
	if *lineCount <= 0 {
		*lineCount = defaultLines
	}
	state, err := loadServiceState(paths.State)
	if err != nil {
		return errorf(msgCannotReadState, paths.State, err)
	}
	if paths.Plist != "" {
		if _, err := os.Stat(paths.Plist); err != nil {
			fmt.Fprintln(stdout, text(msgStatusNotInstalled))
		} else {
			fmt.Fprintln(stdout, text(msgStatusInstalled, paths.Plist))
		}
	}
	switch age := now.Sub(state.Heartbeat).Round(time.Second); {
	case state.Heartbeat.IsZero():
		fmt.Fprintln(stdout, text(msgStatusNeverRan))
	case age < time.Minute:
		fmt.Fprintln(stdout, text(msgStatusWorks, age))
	default:
		fmt.Fprintln(stdout, text(msgStatusStopped, formatTime(state.Heartbeat)))
	}
	if state.Pause.IsOn {
		if state.Pause.Until.IsZero() {
			fmt.Fprintln(stdout, text(msgStatusPausedForever, state.Pause.User))
		} else {
			fmt.Fprintln(stdout, text(msgStatusPausedUntil, formatTime(state.Pause.Until), state.Pause.User))
		}
	}
	if state.IsPreBlocked && !state.Pause.IsOn {
		fmt.Fprintln(stdout, text(msgStatusPreBlocked, formatTime(state.PreBlockSince)))
	}
	if !state.LastFullCheck.IsZero() {
		fmt.Fprintln(stdout, text(msgStatusCountry, state.Country, formatTime(state.LastFullCheck)))
	}
	if state.LastFailure.Error != "" {
		fmt.Fprintln(stdout, text(msgStatusLastFailure, formatTime(state.LastFailure.Time), state.LastFailure.Error))
	}
	switch {
	case state.Firewall.Error != "":
		fmt.Fprintln(stdout, text(msgStatusFirewallError, state.Firewall.Error))
	case state.Firewall.IsBlocked:
		fmt.Fprintln(stdout, text(msgStatusFirewallOn))
	}
	switch db := state.LocalDB; {
	case db.File == "":
		fmt.Fprintln(stdout, text(msgStatusLocalDBNone))
	case now.Sub(db.BuildTime) > maxDBAge:
		fmt.Fprintln(stdout, text(msgStatusLocalDBOld, db.File, db.BuildTime.Format(time.DateOnly)))
	default:
		fmt.Fprintln(stdout, text(msgStatusLocalDB, db.File, db.BuildTime.Format(time.DateOnly)))
	}
	blocked := state.blockedProviders()
	for _, provider := range geocheck.Providers() {
		word := text(msgStatusOpen)
		if slices.Contains(blocked, provider) {
			word = text(msgStatusBlocked)
		}
		fmt.Fprintf(stdout, "  %-10s %-12s %s\n", provider.DisplayName(), word, state.provider(provider).Reason)
	}
	if count, err := countBlockedHosts(paths.Hosts); err == nil {
		fmt.Fprintln(stdout, text(msgStatusHosts, paths.Hosts, count))
	}
	if lines := readLastLines(logPath, *lineCount); len(lines) > 0 {
		fmt.Fprintln(stdout, text(msgStatusLog, logPath))
		for _, line := range lines {
			fmt.Fprintln(stdout, "  "+line)
		}
	}
	return nil
}

// commandCheck asks the service for a full check now. Any user can run it.
func commandCheck(paths appPaths, stdout io.Writer) error {
	response, err := sendCommand(paths.CheckSocket, commandRequest{Command: socketCheck})
	if err != nil {
		return errorf(msgServiceNotReachable, err)
	}
	switch response.Code {
	case codeOK:
		fmt.Fprintln(stdout, text(msgCheckStarted))
		return nil
	case codeTooSoon:
		return errorf(msgCheckTooSoon, int(userCheckInterval.Seconds()))
	}
	return errorf(msgUnexpectedAnswer, response.Code)
}

// commandPause starts a pause through the control socket. Only root can open the socket.
func commandPause(paths appPaths, args []string, now time.Time, isRoot bool, stdout io.Writer) error {
	flags := newFlagSet("pause")
	forValue := flags.String("for", "", text(msgFlagPauseFor))
	isForever := flags.Bool("forever", false, text(msgFlagPauseForever))
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !isRoot {
		return errorf(msgNeedRoot, "pause")
	}
	if (*forValue == "") == !*isForever {
		return errorf(msgPauseNeedsTime)
	}
	command := commandRequest{Command: socketPause, IsForever: *isForever, User: getUserName()}
	if !*isForever {
		duration, err := parsePauseDuration(*forValue)
		if err != nil {
			return err
		}
		command.Until = now.Add(duration)
	}
	response, err := sendCommand(paths.ControlSocket, command)
	if err != nil {
		return errorf(msgServiceNotReachable, err)
	}
	if response.Code != codeOK {
		return errorf(msgUnexpectedAnswer, response.Code)
	}
	if *isForever {
		fmt.Fprintln(stdout, text(msgPausedForever))
	} else {
		fmt.Fprintln(stdout, text(msgPausedUntil, formatTime(command.Until)))
	}
	return nil
}

// commandResume stops the pause. Then the service blocks until a full check passes.
func commandResume(paths appPaths, isRoot bool, stdout io.Writer) error {
	if !isRoot {
		return errorf(msgNeedRoot, "resume")
	}
	response, err := sendCommand(paths.ControlSocket, commandRequest{Command: socketResume})
	if err != nil {
		return errorf(msgServiceNotReachable, err)
	}
	switch response.Code {
	case codeOK:
		fmt.Fprintln(stdout, text(msgResumed))
		return nil
	case codeNoPause:
		return errorf(msgNoPauseActive)
	}
	return errorf(msgUnexpectedAnswer, response.Code)
}

// parsePauseDuration reads a time such as "30m", "4h" or "3d". The time must be from 1 minute to 365 days.
func parsePauseDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	var duration time.Duration
	if days, isDays := strings.CutSuffix(value, "d"); isDays {
		count, err := strconv.Atoi(days)
		if err != nil {
			return 0, errorf(msgPauseTimeInvalid, value)
		}
		duration = time.Duration(count) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return 0, errorf(msgPauseTimeInvalid, value)
		}
		duration = parsed
	}
	if duration < time.Minute || duration > 365*24*time.Hour {
		return 0, errorf(msgPauseTimeInvalid, value)
	}
	return duration, nil
}

// splitList splits a list separated by commas. It removes the empty items. With isUpper, it changes the items to uppercase.
func splitList(value string, isUpper bool) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if isUpper {
			item = strings.ToUpper(item)
		}
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

// readYes reads 1 line and tells if it is "yes". The Russian "да" also counts.
func readYes(input io.Reader) bool {
	line, _ := bufio.NewReader(input).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "yes" || answer == "да"
}

// makeRootFolder creates the folder with the rights 0755 and gives it to root, also when the folder exists.
func makeRootFolder(path string) error {
	if err := makeFolder(path, 0o755); err != nil {
		return err
	}
	return chownToRoot(path)
}

// createLink makes a link to the binary for the CLI. It does not replace a file that is not a link.
func createLink(link, target string) error {
	if link == "" {
		return nil
	}
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return errorf(msgLinkIsFile, link)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}

// removeLink removes the link only if it points to the binary of clawdiator.
func removeLink(link, target string) error {
	destination, err := os.Readlink(link)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || destination != target {
		return nil
	}
	return os.Remove(link)
}

// countBlockedHosts returns the number of hosts in the clawdiator block of the hosts file.
func countBlockedHosts(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	count, isInBlock := 0, false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, hostsBeginMarker):
			isInBlock = true
		case strings.HasPrefix(line, hostsEndMarker):
			isInBlock = false
		case isInBlock && strings.HasPrefix(line, "0.0.0.0 "):
			count++
		}
	}
	return count, nil
}

// readLastLines returns the last lines of a file. A missing file gives no lines.
func readLastLines(path string, count int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return lines
}

func formatTime(value time.Time) string {
	return value.Local().Format("2006-01-02 15:04:05")
}

func isSameFile(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
