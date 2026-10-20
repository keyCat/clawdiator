package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"clawdiator/internal/geocheck"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// testDBCountries are the networks of the test database. The first 2 networks hold the test addresses of openCountryDB.
// The other networks are documentation networks.
var testDBCountries = map[string]string{
	"8.8.8.0/24":       "US",
	"2001:4860::/32":   "US",
	"198.51.100.0/24":  "DE",
	"203.0.113.0/24":   "CN",
	"2001:db8:1::/48":  "UA",
	"2001:db8:2::/48":  "DE",
	"100.64.0.0/10":    "",
	"192.0.2.0/25":     "de",
	"2001:db8:ff::/48": "",
}

// newTestDBData returns the data of a small database. A network with an empty country has a record without a country.
func newTestDBData(t *testing.T, databaseType string, buildTime time.Time, countries map[string]string) []byte {
	t.Helper()
	return writeTestDB(t, mmdbwriter.Options{
		DatabaseType: databaseType, BuildEpoch: buildTime.Unix(), Description: map[string]string{"en": "the test database of clawdiator"},
	}, countries)
}

func writeTestDB(t *testing.T, options mmdbwriter.Options, countries map[string]string) []byte {
	t.Helper()
	options.IPVersion, options.RecordSize, options.IncludeReservedNetworks = 6, 24, true
	// A file can have no aliases of the IPv4 networks in the IPv6 tree. The lookup must work without them.
	options.DisableIPv4Aliasing = true
	tree, err := mmdbwriter.New(options)
	if err != nil {
		t.Fatal(err)
	}
	for network, country := range countries {
		_, ipNetwork, err := net.ParseCIDR(network)
		if err != nil {
			t.Fatal(err)
		}
		record := mmdbtype.Map{"continent": mmdbtype.Map{"code": mmdbtype.String("EU")}}
		if country != "" {
			record["country"] = mmdbtype.Map{"iso_code": mmdbtype.String(country)}
		}
		if err := tree.Insert(ipNetwork, record); err != nil {
			t.Fatal(err)
		}
	}
	var buffer bytes.Buffer
	if _, err := tree.WriteTo(&buffer); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// newTestDB returns an open test database of the month 2026-10, built 10 days before testStart.
func newTestDB(t *testing.T) *countryDB {
	t.Helper()
	db, err := openCountryDB(newTestDBData(t, dbType, testStart.Add(-10*24*time.Hour), testDBCountries), "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOpenCountryDB(t *testing.T) {
	db := newTestDB(t)
	for address, want := range map[string]string{
		"198.51.100.7":          "DE",
		"::ffff:198.51.100.7":   "DE",
		"203.0.113.9":           "CN",
		"2001:db8:1::5":         "UA",
		"192.0.2.1":             "DE",
		"2001:4860:4860::8888":  "US",
		"192.0.2.200":           "",
		"100.64.0.1":            "",
		"2001:db8:ff::1":        "",
		"not an address":        "",
		"2001:db8:ffff:ffff::1": "",
	} {
		got, err := db.lookupCountry(address)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("lookupCountry(%q) = %q, %v, want %q", address, got, err, want)
		}
	}
	if !db.buildTime.Equal(testStart.Add(-10*24*time.Hour)) || db.describe() != "dbip-country-lite-2026-10.mmdb, built 2026-10-04" {
		t.Errorf("the build time = %v, describe = %q", db.buildTime, db.describe())
	}

	withoutTestAddresses := map[string]string{"198.51.100.0/24": "DE"}
	for name, data := range map[string][]byte{
		"another type":          newTestDBData(t, "GeoLite2-City", testStart, testDBCountries),
		"no test addresses":     newTestDBData(t, dbType, testStart, withoutTestAddresses),
		"damaged data":          newTestDBData(t, dbType, testStart, testDBCountries)[100:],
		"not a database at all": []byte("<html>a page of an error</html>"),
		// Verify finds the error. The tree and the records are correct.
		"metadata without a description": writeTestDB(t, mmdbwriter.Options{DatabaseType: dbType, BuildEpoch: testStart.Unix()}, testDBCountries),
	} {
		if _, err := openCountryDB(data, "2026-10"); err == nil {
			t.Errorf("%s: openCountryDB must fail", name)
		}
	}
}

func TestCountryDBAge(t *testing.T) {
	db := newTestDB(t)
	if !db.isUsable(db.buildTime.Add(maxDBAge)) || db.isUsable(db.buildTime.Add(maxDBAge+time.Hour)) || (*countryDB)(nil).isUsable(testStart) {
		t.Error("the service must use only data that is not older than 60 days")
	}
	october := time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)
	if isDBDownloadDue(db, october) || !isDBDownloadDue(db, october.Add(2*time.Hour)) || !isDBDownloadDue(nil, october) {
		t.Error("the service must download the file of each new month, and a missing file")
	}
	if (*countryDB)(nil).describe() != "none" {
		t.Error("describe of no database must give none")
	}
}

func TestLoadCountryDB(t *testing.T) {
	dir := t.TempDir()
	if db, err := loadCountryDB(dir); db != nil || err != nil {
		t.Errorf("a folder without a file must give no database and no error: %v, %v", db, err)
	}
	september := newTestDBData(t, dbType, testStart.Add(-40*24*time.Hour), testDBCountries)
	for name, data := range map[string][]byte{
		"dbip-country-lite-2026-09.mmdb":  september,
		"dbip-country-lite-2026-10.mmdb":  []byte("damaged"),
		"dbip-country-lite-latest.mmdb":   september,
		"dbip-country-lite-2026-08.mmdb~": september,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := loadCountryDB(dir)
	if db == nil || db.month != "2026-09" {
		t.Fatalf("the newest correct file must open: %+v, %v", db, err)
	}
	if err == nil || !strings.Contains(err.Error(), "dbip-country-lite-2026-10.mmdb") {
		t.Errorf("the error must name the damaged newer file: %v", err)
	}
}

func gzipData(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestDownloadCountryDB(t *testing.T) {
	valid := gzipData(t, newTestDBData(t, dbType, testStart, testDBCountries))
	tooLarge := gzipData(t, make([]byte, maxDBSize+1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/valid":
			if r.Header.Get("User-Agent") != appName {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Write(valid)
		case "/too-large":
			w.Write(tooLarge)
		case "/not-gzip":
			w.Write([]byte("<html>maintenance</html>"))
		case "/damaged":
			w.Write(gzipData(t, []byte("damaged")))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	other := filepath.Join(dir, "state.json")
	old := filepath.Join(dir, getDBFileName("2026-09"))
	for _, path := range []string{other, old} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for path, want := range map[string]string{
		"/missing":   "HTTP 404",
		"/too-large": "larger than 64 MB",
		"/not-gzip":  "gzip",
		"/damaged":   "",
	} {
		if _, err := downloadCountryDB(context.Background(), server.Client(), server.URL+path, dir, "2026-10"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the download must fail with %q: %v", path, want, err)
		}
	}
	if files := findDBFiles(dir); !slices.Equal(files, []string{old}) {
		t.Errorf("a failed download must change no file: %v", files)
	}
	if _, err := downloadCountryDB(context.Background(), server.Client(), server.URL+"/missing", dir, "2026-11"); !errors.Is(err, errDBNotPublished) {
		t.Errorf("HTTP 404 must tell that DB-IP has not published the file: %v", err)
	}

	db, err := downloadCountryDB(context.Background(), server.Client(), server.URL+"/valid", dir, "2026-10")
	if err != nil || db.month != "2026-10" {
		t.Fatalf("the download = %+v, %v", db, err)
	}
	if files := findDBFiles(dir); !slices.Equal(files, []string{filepath.Join(dir, "dbip-country-lite-2026-10.mmdb")}) {
		t.Errorf("the download must save the new file and remove the old file: %v", files)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the download must not remove other files: %v", err)
	}
	if loaded, err := loadCountryDB(dir); err != nil || loaded.month != "2026-10" {
		t.Errorf("the saved file must open: %+v, %v", loaded, err)
	}
}

func TestAddDBLocations(t *testing.T) {
	db := newTestDB(t)
	cloudflare := geocheck.Location{Country: "DE", IP: "198.51.100.7", Source: "cloudflare"}
	ipinfo := geocheck.Location{Country: "DE", Region: "Hesse", Source: "ipinfo.io"}
	countryIs := geocheck.Location{Country: "DE", IP: "198.51.100.7", Source: "country.is"}

	result, err := addDBLocations(geocheck.Result{Locations: []geocheck.Location{cloudflare, ipinfo}}, db, 3)
	if err != nil || result.Describe() != "cloudflare: DE; ipinfo.io: DE, Hesse; dbip: DE" || result.Locations[2].IP != "198.51.100.7" {
		t.Errorf("the database must add 1 reply for the minimum: %q, %v", result.Describe(), err)
	}
	result, _ = addDBLocations(geocheck.Result{Locations: []geocheck.Location{cloudflare, countryIs}}, db, 3)
	if len(result.Locations) != 3 {
		t.Errorf("the database must look up each address 1 time: %q", result.Describe())
	}

	withoutIP := geocheck.Result{Locations: []geocheck.Location{ipinfo, ipinfo}, Failures: []string{"cloudflare: HTTP 500"}}
	if _, err := addDBLocations(withoutIP, db, 3); err == nil ||
		!strings.Contains(err.Error(), "2 location services replied, and the local database gave no country, but the minimum is 3") ||
		!strings.Contains(err.Error(), "cloudflare: HTTP 500; dbip: no reply gave the public IP address") {
		t.Errorf("without an address, the online replies must meet the minimum: %v", err)
	}
	if result, err := addDBLocations(withoutIP, db, 2); err != nil || len(result.Locations) != 2 {
		t.Errorf("enough online replies must pass without the database: %+v, %v", result, err)
	}
	unknown := geocheck.Location{Country: "DE", IP: "100.64.0.1", Source: "cloudflare"}
	if _, err := addDBLocations(geocheck.Result{Locations: []geocheck.Location{unknown, ipinfo}}, db, 3); err == nil || !strings.Contains(err.Error(), "dbip: no country for 100.64.0.1") {
		t.Errorf("an address without a country must give no opinion: %v", err)
	}
	if result, err := addDBLocations(geocheck.Result{Locations: []geocheck.Location{cloudflare}}, nil, 1); err != nil || len(result.Locations) != 1 {
		t.Errorf("without the database, the result must not change: %+v, %v", result, err)
	}

	// The strict rule applies to the opinion of the database too.
	china := geocheck.Location{Country: "DE", IP: "203.0.113.9", Source: "cloudflare"}
	result, _ = addDBLocations(geocheck.Result{Locations: []geocheck.Location{china, ipinfo}}, db, 3)
	if verdict := geocheck.Check(geocheck.Anthropic, result, nil, nil); verdict.Allowed() || verdict.Location.Source != dbSource {
		t.Errorf("an opinion of a blocked country must block: %+v", verdict)
	}
}

func TestDownloadNewestDB(t *testing.T) {
	db := newTestDB(t)
	firstDay := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	var months []string
	download := func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		if month == "2026-11" {
			return nil, errDBNotPublished
		}
		return db, nil
	}
	if got, err := downloadNewestDB(context.Background(), download, firstDay, false); got != db || err != nil || !slices.Equal(months, []string{"2026-11", "2026-10"}) {
		t.Errorf("without a usable file, the download must take the file of the month before: %v, %v", months, err)
	}
	months = nil
	if _, err := downloadNewestDB(context.Background(), download, firstDay, true); !errors.Is(err, errDBNotPublished) || len(months) != 1 {
		t.Errorf("with a usable file, the service must wait for the file of the current month: %v, %v", months, err)
	}
	months = nil
	offline := func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		return nil, errOffline
	}
	if _, err := downloadNewestDB(context.Background(), offline, firstDay, false); !errors.Is(err, errOffline) || len(months) != 1 {
		t.Errorf("only HTTP 404 must start the download of the month before: %v, %v", months, err)
	}
	january := time.Date(2027, 1, 1, 0, 30, 0, 0, time.UTC)
	months = nil
	_, _ = downloadNewestDB(context.Background(), func(_ context.Context, month string) (*countryDB, error) {
		months = append(months, month)
		return nil, errDBNotPublished
	}, january, false)
	if !slices.Equal(months, []string{"2027-01", "2026-12"}) {
		t.Errorf("the month before January is December of the year before: %v", months)
	}
}

func TestSaveDBFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, getDBFileName("2026-10"))
	stale := filepath.Join(dir, ".dbip-download-123")
	fresh := filepath.Join(dir, ".dbip-download-456")
	for _, temporary := range []string{stale, fresh} {
		if err := os.WriteFile(temporary, []byte("part of a file"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, testStart, time.Now().Add(-2*staleTemporaryAge)); err != nil {
		t.Fatal(err)
	}
	if err := saveDBFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := saveDBFile(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "second" {
		t.Errorf("the second write must replace the file: %q, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("all users must read the file: %v, %v", info.Mode(), err)
	}
	if files := findDBFiles(dir); !slices.Equal(files, []string{path}) {
		t.Errorf("findDBFiles must not see the temporary files: %v", files)
	}
	removeStaleTemporaryFiles(dir)
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{".dbip-download-456", "dbip-country-lite-2026-10.mmdb"}) {
		t.Errorf("only the old temporary file must go, because a new one can belong to a running download: %v", names)
	}
}

// At the start of a month, the install and the service can write the file of the same month at the same time.
// Each writer must have its own temporary file. Then each write passes, and the file is always complete.
func TestSaveDBFileWithTwoWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), getDBFileName("2026-10"))
	first, second := bytes.Repeat([]byte("a"), 1<<20), bytes.Repeat([]byte("b"), 1<<20)
	for range 20 {
		errs := make(chan error, 2)
		for _, data := range [][]byte{first, second} {
			go func() { errs <- saveDBFile(path, data) }()
		}
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("each write must pass: %v", err)
			}
		}
		if data, _ := os.ReadFile(path); !bytes.Equal(data, first) && !bytes.Equal(data, second) {
			t.Fatal("the file must hold the complete data of 1 writer")
		}
	}
}
