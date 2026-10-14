package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"geocheck"
	"github.com/oschwald/maxminddb-golang/v2"
)

// The local database of DB-IP gives 1 more opinion about the country of the public IP address.
// The service looks up the address from the replies of the probe and of the full check.
// The license of the database is CC BY 4.0, so each text about it must give dbCredit.
const (
	// dbSource is the name of the local database in the replies, for example "dbip: DE".
	dbSource = "dbip"
	// dbCredit is the credit that the CC BY 4.0 license of DB-IP requires.
	dbCredit = "IP Geolocation by DB-IP (https://db-ip.com)"
	// dbFilePrefix and dbFileSuffix give the name of the file of 1 month, for example dbip-country-lite-2026-10.mmdb.
	// The release has the file with this name next to the binary.
	dbFilePrefix = "dbip-country-lite-"
	dbFileSuffix = ".mmdb"
	// dbURLFormat gives the URL of the file of 1 month. DB-IP publishes a new file at the start of each month.
	dbURLFormat = "https://download.db-ip.com/free/dbip-country-lite-%s.mmdb.gz"
	// dbType is the type in the metadata of the file.
	dbType = "DBIP-Country-Lite"
	// maxDBAge is the maximum age of the data. The service does not use older data.
	maxDBAge = 60 * 24 * time.Hour
	// maxDBSize is the maximum size of the unpacked file. It stops a bad file that fills the memory.
	// The file of 2026-10 has 8.3 MB.
	maxDBSize = 64 << 20
	// dbRetryInterval is the minimum time between 2 downloads.
	dbRetryInterval = 6 * time.Hour
	// dbDownloadTimeout is the time limit for 1 download of the service.
	dbDownloadTimeout = 2 * time.Minute
	// installDBTimeout is the time limit for the download during install. The user waits at the terminal.
	installDBTimeout = 30 * time.Second
	// dbTemporaryPattern gives the names of the temporary files. The names do not match the files of findDBFiles.
	dbTemporaryPattern = ".dbip-download-*"
	// staleTemporaryAge is the age of a temporary file that a stopped download left. A running download is never so old.
	staleTemporaryAge = time.Hour
)

// errDBNotPublished tells that DB-IP has no file of the month yet. DB-IP publishes the file some hours after the start of the month.
var errDBNotPublished = errors.New("DB-IP has not published the file of the month yet (HTTP 404)")

// dbTestAddresses are 1 IPv4 and 1 IPv6 address. A correct file gives a country for each of them.
var dbTestAddresses = []string{"8.8.8.8", "2001:4860:4860::8888"}

// countryDB is an open file of the local database. It does not change after the open, so all goroutines can use it.
type countryDB struct {
	reader *maxminddb.Reader
	// month is the month of the file, for example "2026-10". It comes from the name of the file.
	month string
	// buildTime is the time when DB-IP made the file. It comes from the metadata of the file.
	buildTime time.Time
}

// openCountryDB checks the data and opens it.
// The data must have the correct type, a correct search tree and a country for each test address.
func openCountryDB(data []byte, month string) (*countryDB, error) {
	reader, err := maxminddb.OpenBytes(data)
	if err != nil {
		return nil, err
	}
	if reader.Metadata.DatabaseType != dbType || reader.Metadata.IPVersion != 6 {
		return nil, fmt.Errorf("the type of the database is %q with IP version %d, but it must be %q with IP version 6",
			reader.Metadata.DatabaseType, reader.Metadata.IPVersion, dbType)
	}
	if err := reader.Verify(); err != nil {
		return nil, fmt.Errorf("the database is damaged: %w", err)
	}
	db := &countryDB{reader: reader, month: month, buildTime: reader.Metadata.BuildTime().UTC()}
	for _, address := range dbTestAddresses {
		if _, err := db.lookupCountry(address); err != nil {
			return nil, fmt.Errorf("the test lookup failed: %w", err)
		}
	}
	return db, nil
}

// lookupCountry returns the country code of the IP address.
func (db *countryDB) lookupCountry(address string) (string, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return "", err
	}
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	found := db.reader.Lookup(ip.Unmap())
	if err := found.Decode(&record); err != nil {
		return "", err
	}
	country := strings.ToUpper(record.Country.ISOCode)
	if !found.Found() || !isCountryCode(country) {
		return "", fmt.Errorf("no country for %s", address)
	}
	return country, nil
}

// isUsable tells if the data is not older than maxDBAge.
func (db *countryDB) isUsable(now time.Time) bool {
	return db != nil && now.Sub(db.buildTime) <= maxDBAge
}

// describe returns the file and the build date for the log, for example "dbip-country-lite-2026-10.mmdb, built 2026-10-01".
func (db *countryDB) describe() string {
	if db == nil {
		return "none"
	}
	return getDBFileName(db.month) + ", built " + db.buildTime.Format(time.DateOnly)
}

func getDBFileName(month string) string {
	return dbFilePrefix + month + dbFileSuffix
}

// getDBMonth returns the month of the UTC time, for example "2026-10". The name of the file of DB-IP uses it.
func getDBMonth(now time.Time) string {
	return now.UTC().Format("2006-01")
}

// isDBDownloadDue tells if the service must download the file of the current month.
func isDBDownloadDue(db *countryDB, now time.Time) bool {
	return db == nil || db.month < getDBMonth(now)
}

// findDBFiles returns the files of the local database in the folder, the newest month first.
func findDBFiles(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, dbFilePrefix+"*"+dbFileSuffix))
	var files []string
	for _, path := range paths {
		if _, err := time.Parse("2006-01", getMonthOfFile(path)); err == nil {
			files = append(files, path)
		}
	}
	slices.Sort(files)
	slices.Reverse(files)
	return files
}

func getMonthOfFile(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), dbFilePrefix), dbFileSuffix)
}

// loadCountryDB opens the newest correct file in the folder. Without a file, the result is nil and no error.
// The error tells about each newer file that is not correct, also if an older file is correct.
func loadCountryDB(dir string) (*countryDB, error) {
	var errs []error
	for _, path := range findDBFiles(dir) {
		data, err := os.ReadFile(path)
		if err == nil {
			var db *countryDB
			if db, err = openCountryDB(data, getMonthOfFile(path)); err == nil {
				return db, errors.Join(errs...)
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(path), err))
	}
	return nil, errors.Join(errs...)
}

// newDBDownloader returns the function that downloads the file of a month from db-ip.com into the folder.
func newDBDownloader(dir string) func(ctx context.Context, month string) (*countryDB, error) {
	return func(ctx context.Context, month string) (*countryDB, error) {
		return downloadCountryDB(ctx, http.DefaultClient, fmt.Sprintf(dbURLFormat, month), dir, month)
	}
}

// downloadNewestDB downloads the file of the current month. If DB-IP has not published it yet, and no usable file is on the disk,
// it downloads the file of the month before. DB-IP keeps the file of the month before on the site.
func downloadNewestDB(ctx context.Context, download func(context.Context, string) (*countryDB, error), now time.Time, hasUsableFile bool) (*countryDB, error) {
	db, err := download(ctx, getDBMonth(now))
	if !errors.Is(err, errDBNotPublished) || hasUsableFile {
		return db, err
	}
	monthBefore := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	dbBefore, errBefore := download(ctx, getDBMonth(monthBefore))
	if errBefore != nil {
		return nil, errors.Join(err, errBefore)
	}
	return dbBefore, nil
}

// downloadCountryDB downloads the file of the month from the URL and checks it. Then it saves the file in the folder
// and removes the files of the other months. It reads the file into the memory, so the file on the disk can change at any time.
func downloadCountryDB(ctx context.Context, client *http.Client, url, dir, month string) (*countryDB, error) {
	ctx, cancel := context.WithTimeout(ctx, dbDownloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", appName)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, errDBNotPublished
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	unpacked, err := gzip.NewReader(response.Body)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(unpacked, maxDBSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDBSize {
		return nil, fmt.Errorf("the unpacked file is larger than %d MB", maxDBSize>>20)
	}
	db, err := openCountryDB(data, month)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, getDBFileName(month))
	if err := saveDBFile(path, data); err != nil {
		return nil, err
	}
	for _, other := range findDBFiles(dir) {
		if other != path {
			os.Remove(other)
		}
	}
	removeStaleTemporaryFiles(dir)
	return db, nil
}

// saveDBFile writes the file safely. It writes a new temporary file in the same folder and syncs it to the disk.
// Then it renames the temporary file to the final name and syncs the folder.
// So a crash or a power loss leaves the old file or the complete new file, never a part of a file.
// Each writer has its own temporary file, so the install and the service can write the file of the same month at the same time.
func saveDBFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), dbTemporaryPattern)
	if err != nil {
		return err
	}
	// After the rename, this remove finds no file and has no effect.
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary.Name(), 0o644); err != nil {
		return err
	}
	if err := chownToRoot(temporary.Name()); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	// The sync of the folder makes the rename durable. Windows cannot sync a folder, so the error has no effect.
	if folder, err := os.Open(filepath.Dir(path)); err == nil {
		_ = folder.Sync()
		folder.Close()
	}
	return nil
}

// removeStaleTemporaryFiles removes the temporary files that a stopped download left, for example after a power loss.
func removeStaleTemporaryFiles(dir string) {
	paths, _ := filepath.Glob(filepath.Join(dir, dbTemporaryPattern))
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > staleTemporaryAge {
			os.Remove(path)
		}
	}
}

// addDBLocations adds the opinion of the local database to the replies of the online services.
// The database looks up each public IP address of the replies. It counts as 1 reply for the minimum.
// Without an opinion of the database, the online replies must meet the minimum.
func addDBLocations(result geocheck.Result, db *countryDB, minReplies int) (geocheck.Result, error) {
	onlineCount := len(result.Locations)
	if db == nil {
		return result, nil
	}
	hasOpinion := false
	var addresses []string
	for _, location := range result.Locations[:onlineCount] {
		if location.IP == "" || slices.Contains(addresses, location.IP) {
			continue
		}
		addresses = append(addresses, location.IP)
		country, err := db.lookupCountry(location.IP)
		if err != nil {
			result.Failures = append(result.Failures, dbSource+": "+err.Error())
			continue
		}
		result.Locations = append(result.Locations, geocheck.Location{Country: country, IP: location.IP, Source: dbSource})
		hasOpinion = true
	}
	if len(addresses) == 0 {
		result.Failures = append(result.Failures, dbSource+": no reply gave the public IP address")
	}
	if !hasOpinion && onlineCount < minReplies {
		return geocheck.Result{}, fmt.Errorf("%d location services replied, and the local database gave no country, but the minimum is %d (%s)",
			onlineCount, minReplies, result.Describe())
	}
	return result, nil
}
