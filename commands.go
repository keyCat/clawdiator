package main

import (
	"bufio"
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
	now        func() time.Time
	user       string
	stdin      io.Reader
	stdout     io.Writer
}

func newInstaller(paths appPaths) *installer {
	return &installer{
		paths:      paths,
		manager:    launchdManager{},
		executable: os.Executable,
		isRoot:     func() bool { return os.Geteuid() == 0 },
		flushDNS:   flushDNS,
		now:        time.Now,
		user:       getUserName(),
		stdin:      os.Stdin,
		stdout:     os.Stdout,
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
	geoServices := flags.String("geo-services", "", text(msgFlagGeoServices))
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
	if in.paths.Plist != "" {
		report(msgStepStopService, in.manager.Uninstall(in.paths))
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
	if !isComplete {
		return errorf(msgUninstallIncomplete)
	}
	fmt.Fprintln(in.stdout, text(msgUninstalled))
	return nil
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
		return nil
	}
	fmt.Fprintln(in.stdout, text(msgNoBlock, in.paths.Hosts))
	return nil
}

// commandStatus shows the state of the service. It needs no administrator rights: it reads state.json and the log.
// The age of the heartbeat shows if the service works. launchctl can fail for a user who is not root.
func commandStatus(paths appPaths, now time.Time, stdout io.Writer) error {
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
	if lines := readLastLines(paths.Log, 10); len(lines) > 0 {
		fmt.Fprintln(stdout, text(msgStatusLog, paths.Log))
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
