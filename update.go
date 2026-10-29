package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// releasesURL is the GitHub repository with the releases. Its page /releases/latest redirects to the newest tag.
	releasesURL = "https://github.com/keyCat/clawdiator"
	// websiteURL is the website of the tool. The texts about a new version send the user there.
	websiteURL = "https://ilya.zone/clawdiator/"
	// updateLabel is the one-shot LaunchDaemon that installs a new version. The install stops the service,
	// so the service cannot run the install as its own child process.
	updateLabel = "local.clawdiator.update"
	// updateCheckInterval is the time between 2 checks for a new version.
	updateCheckInterval = 6 * time.Hour
	// updateFirstCheckDelay is the time from the start of the service to the first check.
	// After an automatic update, the update job must end before the check removes it.
	updateFirstCheckDelay = 5 * time.Minute
	// updateTimeout is the time limit of a check, together with the download and the test of a new version.
	updateTimeout = 5 * time.Minute
	// maxArchiveSize is the maximum size of a release archive and of each binary in it.
	maxArchiveSize = 100 << 20
	// updateWaitTimeout is the maximum wait of "update --check" and of the tray for the result of the check.
	updateWaitTimeout = 2 * time.Minute
	// updateJobTimeout is the maximum time of the update job. A successful install stops the old service before this time.
	updateJobTimeout = 5 * time.Minute
)

// tagPattern is the only accepted form of a release tag. The tag goes into a URL, a path, a plist and the logs.
var tagPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// parseVersion returns the 3 numbers of a tag, for example v1.2.3. A local build has the version "dev", which gives false.
func parseVersion(tag string) ([3]int, bool) {
	match := tagPattern.FindStringSubmatch(tag)
	if match == nil {
		return [3]int{}, false
	}
	var numbers [3]int
	for index := range numbers {
		numbers[index], _ = strconv.Atoi(match[index+1])
	}
	return numbers, true
}

// isNewerVersion tells if the tag latest is newer than the version current.
// A current version that is not a tag, for example "dev", is older than each tag.
func isNewerVersion(latest, current string) bool {
	latestNumbers, isLatest := parseVersion(latest)
	currentNumbers, isCurrent := parseVersion(current)
	return isLatest && (!isCurrent || slices.Compare(latestNumbers[:], currentNumbers[:]) > 0)
}

// fetchLatestVersion returns the tag of the newest release. It reads the redirect of /releases/latest,
// so it does not use the API of GitHub and its rate limit.
func fetchLatestVersion(ctx context.Context, baseURL string) (string, error) {
	client := *lookupClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", appName+"/"+version)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	_ = response.Body.Close()
	location := response.Header.Get("Location")
	_, tag, _ := strings.Cut(location, "/releases/tag/")
	if response.StatusCode != http.StatusFound || !tagPattern.MatchString(tag) {
		return "", fmt.Errorf("GitHub gave %s and no correct tag: %q", response.Status, location)
	}
	return tag, nil
}

// downloadRelease downloads the archive of the tag for the architecture. It checks the archive with SHA256SUMS
// of the same release, and it unpacks the 2 binaries into dir. It returns the path of the new main binary.
func downloadRelease(ctx context.Context, baseURL, tag, arch, dir string) (string, error) {
	if !tagPattern.MatchString(tag) {
		return "", fmt.Errorf("the tag %q is not correct", tag)
	}
	archiveName := appName + "-darwin-" + arch + ".tar.gz"
	sums, err := downloadFile(ctx, baseURL+"/releases/download/"+tag+"/SHA256SUMS", 1<<20)
	if err != nil {
		return "", err
	}
	wantSum := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == archiveName {
			wantSum = strings.ToLower(fields[0])
		}
	}
	if wantSum == "" {
		return "", fmt.Errorf("SHA256SUMS of %s has no line for %s", tag, archiveName)
	}
	archive, err := downloadFile(ctx, baseURL+"/releases/download/"+tag+"/"+archiveName, maxArchiveSize)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != wantSum {
		return "", fmt.Errorf("the checksum of %s is not correct", archiveName)
	}
	if err := extractBinaries(archive, dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, appName), nil
}

// downloadFile returns the body of a GET request. A body that is larger than maxSize is an error.
func downloadFile(ctx context.Context, url string, maxSize int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", appName+"/"+version)
	client := *lookupClient
	client.CheckRedirect = checkGitHubRedirect
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s gave %s", url, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err == nil && int64(len(data)) > maxSize {
		err = fmt.Errorf("%s is larger than %d bytes", url, maxSize)
	}
	return data, err
}

// checkGitHubRedirect lets a download follow only https redirects to the hosts of GitHub. GitHub sends the files of a release
// from release-assets.githubusercontent.com.
func checkGitHubRedirect(request *http.Request, via []*http.Request) error {
	host := request.URL.Hostname()
	if request.URL.Scheme != "https" || (host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
		return fmt.Errorf("the download redirects to %s, which is not GitHub", request.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("the download has more than 10 redirects")
	}
	return nil
}

// extractBinaries writes the 2 binaries of the archive into dir. It takes only regular files with the exact names,
// so an entry cannot write outside dir or put a link in place of a binary.
func extractBinaries(archive []byte, dir string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	reader := tar.NewReader(gzipReader)
	found := map[string]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name != appName && name != trayBinaryName {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size > maxArchiveSize || found[name] {
			return fmt.Errorf("the archive has a wrong entry %s", name)
		}
		found[name] = true
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(file, reader)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	if !found[appName] {
		return fmt.Errorf("the archive has no %s", appName)
	}
	return nil
}

// updateInfo is what a new binary tells about itself before the automatic update.
type updateInfo struct {
	Binary  string
	Version string
	// Consent is the version of the consent text in the new binary.
	Consent int
}

// readUpdateInfo runs the command update-info of a new binary. A binary without the command, for example v0.1.0,
// gives an error, so the service does not install it automatically.
func readUpdateInfo(ctx context.Context, binary string) (updateInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "update-info").Output()
	if err != nil {
		return updateInfo{}, fmt.Errorf("%s update-info failed: %w", binary, err)
	}
	info := updateInfo{Binary: binary}
	if _, err := fmt.Sscanf(string(output), "%s %d", &info.Version, &info.Consent); err != nil {
		return updateInfo{}, fmt.Errorf("%s update-info gave %q", binary, output)
	}
	return info, nil
}

// prepareUpdate downloads the release of the tag into dir and asks the new binary about itself.
func prepareUpdate(ctx context.Context, dir, tag string) (updateInfo, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return updateInfo{}, err
	}
	binary, err := downloadRelease(ctx, releasesURL, tag, runtime.GOARCH, dir)
	if err != nil {
		return updateInfo{}, err
	}
	info, err := readUpdateInfo(ctx, binary)
	if err == nil && info.Version != tag {
		err = fmt.Errorf("the binary of %s tells the version %s", tag, info.Version)
	}
	return info, err
}

// cleanUpdate removes the job and the folder of the last update. A job that still runs installs a version now,
// for example the job that started this service. A stop of this job would leave a half install, so the function stops.
func cleanUpdate(dir string) error {
	if output, err := runCommand("launchctl", "print", "system/"+updateLabel); err == nil && strings.Contains(output, "state = running") {
		return errors.New("the install of the last update still runs")
	}
	_, _ = runCommand("launchctl", "bootout", "system/"+updateLabel)
	return os.RemoveAll(dir)
}

// startUpdateJob starts "install --unattended" of the new binary as a one-shot LaunchDaemon. The output goes to update.log.
func startUpdateJob(paths appPaths, binary string) error {
	plist := filepath.Join(filepath.Dir(binary), updateLabel+".plist")
	if err := writeRootFile(plist, []byte(renderUpdatePlist(binary, filepath.Join(paths.Dir, "update.log"))), 0o644); err != nil {
		return err
	}
	_, _ = runCommand("launchctl", "bootout", "system/"+updateLabel)
	return bootstrapLaunchd("system", plist)
}

// renderUpdatePlist returns the one-shot LaunchDaemon of the update. It has no KeepAlive, so it runs 1 time.
func renderUpdatePlist(binary, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>install</string>
		<string>--unattended</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, updateLabel, escapeXML(binary), escapeXML(logPath), escapeXML(logPath))
}

// commandUpdate installs the newest release by hand. The new binary runs its own install in this terminal,
// so the user sees each step and the consent text, if the text changed. With --check, the service checks now.
func (in *installer) commandUpdate(args []string) error {
	flags := newFlagSet("update")
	isCheck := flags.Bool("check", false, text(msgFlagUpdateCheck))
	autoInstall := flags.String("auto-install", "", text(msgFlagAutoInstall))
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *autoInstall != "" {
		return in.setAutoUpdate(*autoInstall)
	}
	if *isCheck {
		result, err := checkForUpdate(in.paths)
		if err == nil {
			fmt.Fprintln(in.stdout, result)
		}
		return err
	}
	if runtime.GOOS != "darwin" {
		return errorf(msgOnlyMacOS)
	}
	if !in.isRoot() {
		return errorf(msgNeedRoot, "update")
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	tag, err := fetchLatestVersion(ctx, releasesURL)
	if err != nil {
		return errorf(msgUpdateCheckFailed, err)
	}
	if !isNewerVersion(tag, version) {
		fmt.Fprintln(in.stdout, text(msgUpdateNewest, version))
		return nil
	}
	fmt.Fprintln(in.stdout, text(msgUpdateDownload, tag))
	// Root runs the new binary. A folder in TMPDIR can be in the folder of a user, who can then replace the binary.
	// Only root can write in the app folder. The automatic update uses the folder "update", so this folder has another name.
	dir, err := os.MkdirTemp(in.paths.Dir, "manual-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	binary, err := downloadRelease(ctx, releasesURL, tag, runtime.GOARCH, dir)
	if err != nil {
		return errorf(msgUpdateDownloadFailed, err)
	}
	if info, err := readUpdateInfo(ctx, binary); err != nil || info.Version != tag {
		return errorf(msgUpdateDownloadFailed, fmt.Errorf("the binary of %s does not tell this version: %+v, %v", tag, info, err))
	}
	command := exec.Command(binary, "install")
	command.Stdin, command.Stdout, command.Stderr = in.stdin, in.stdout, os.Stderr
	return command.Run()
}

// setAutoUpdate turns the automatic updates on or off in config.json. The service reads the setting at each check.
func (in *installer) setAutoUpdate(value string) error {
	if value != "on" && value != "off" {
		return errorf(msgAutoInstallValue)
	}
	if !in.isRoot() {
		return errorf(msgNeedRoot, "update --auto-install")
	}
	cfg, err := loadConfig(in.paths.Config)
	if err != nil {
		return errorf(msgCannotReadConfig, in.paths.Config, err)
	}
	cfg.AutoUpdate = value == "on"
	if err := saveConfig(in.paths.Config, cfg); err != nil {
		return err
	}
	if !cfg.AutoUpdate {
		fmt.Fprintln(in.stdout, text(msgAutoUpdateOff))
		return nil
	}
	fmt.Fprintln(in.stdout, text(msgAutoUpdateOn))
	// A version can wait already. The check installs it now, not after some hours. A failed request is not important.
	_, _ = sendCommand(in.paths.CheckSocket, commandRequest{Command: socketUpdateCheck})
	return nil
}

// checkForUpdate asks the service for an update check now. It waits for the result in state.json and returns its text.
// It needs no sudo, so the tray uses it too.
func checkForUpdate(paths appPaths) (string, error) {
	start := time.Now().Round(0)
	response, err := sendCommand(paths.CheckSocket, commandRequest{Command: socketUpdateCheck})
	if err != nil {
		return "", errorf(msgServiceNotReachable, err)
	}
	switch response.Code {
	case codeOK:
	case codeTooSoon:
		return "", errorf(msgCheckTooSoon, int(userCheckInterval.Seconds()))
	case codeLocalBuild:
		return "", errorf(msgUpdateLocalBuild)
	case codeUpdateRunning:
		return text(msgUpdateRunning), nil
	default:
		return "", errorf(msgUnexpectedAnswer, response.Code)
	}
	for deadline := start.Add(updateWaitTimeout); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if state := readServiceState(paths.State); state != nil && state.Update.CheckTime.After(start) {
			return describeUpdateResult(state.Update)
		}
	}
	return "", errorf(msgUpdateCheckTimeout, int(updateWaitTimeout.Seconds()))
}

// describeUpdateResult returns the text about the last update check of the service.
func describeUpdateResult(update updateState) (string, error) {
	switch {
	case update.Error != "":
		return "", errorf(msgUpdateCheckError, update.Error)
	case !isNewerVersion(update.Latest, version):
		return text(msgUpdateNewest, version), nil
	case update.IsAuto && update.ConsentTag != update.Latest:
		return text(msgUpdateInstalling, update.Latest), nil
	}
	return text(msgNoticeUpdateAvailable, update.Latest, websiteURL), nil
}
