package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"clawdiator/internal/geocheck"
)

func TestGetGeoOptions(t *testing.T) {
	options, err := getGeoOptions(&Config{})
	if err != nil || !slices.Equal(geocheck.ServiceNames(options.Services), []string{"cloudflare", "ipwho.is", "geojs.io"}) || options.MinReplies != 2 || options.UserAgent != appName {
		t.Errorf("the default options = %v, %d, %q, %v", geocheck.ServiceNames(options.Services), options.MinReplies, options.UserAgent, err)
	}
	options, err = getGeoOptions(&Config{GeoServices: []string{"ipinfo.io", "ipwho.is"}, GeoMinReplies: 2})
	if err != nil || !slices.Equal(geocheck.ServiceNames(options.Services), []string{"ipinfo.io", "ipwho.is", probeServiceName}) || options.MinReplies != 2 {
		t.Errorf("the full check must also ask the probe service: %v, %d, %v", geocheck.ServiceNames(options.Services), options.MinReplies, err)
	}
	useLanguage(t, languageRussian)
	for _, cfg := range []*Config{
		{GeoServices: []string{"example.com"}},
		{GeoServices: []string{"ipwho.is"}, GeoMinReplies: 2},
		{BlockedCountries: []string{"Germany"}},
	} {
		if _, err := getGeoOptions(cfg); err == nil || !strings.ContainsAny(err.Error(), "абвгдеёжзийклмнопрстуфхцчшщыэюя") {
			t.Errorf("%+v must give a translated error: %v", cfg, err)
		}
	}
	if options.Timeout != lookupTimeout || options.ExtraWait != lookupExtraWait || !slices.Equal(options.WaitFor, []string{probeServiceName}) {
		t.Errorf("the full check must stop the slow services, but wait for the probe service: %v, %v, %v", options.Timeout, options.ExtraWait, options.WaitFor)
	}
	probe := getProbeOptions()
	if len(probe.Services) != 1 || probe.Services[0].Name != probeServiceName || probe.MinReplies != 1 || probe.Rounds != 1 || probe.Timeout != lookupTimeout {
		t.Errorf("the probe options = %+v", probe)
	}
}

// TestLookupClientOpensNewConnections checks that a check after a change of the network cannot use an old connection.
func TestLookupClientOpensNewConnections(t *testing.T) {
	options, _ := getGeoOptions(&Config{})
	if options.Client != lookupClient || getProbeOptions().Client != lookupClient {
		t.Fatal("the full check and the probe must use lookupClient")
	}
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	for range 2 {
		response, err := lookupClient.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	if count := connections.Load(); count != 2 {
		t.Errorf("2 requests opened %d connections, want 2", count)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := loadConfig(path); !os.IsNotExist(err) {
		t.Errorf("a missing config must give ErrNotExist: %v", err)
	}
	cfg := &Config{GeoServices: []string{"cloudflare"}, BlockedCountries: []string{"AF"}, Consent: consent{Time: testStart, Version: consentVersion, User: "ivan"}}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(path)
	if err != nil || !slices.Equal(loaded.BlockedCountries, []string{"AF"}) || loaded.Consent.User != "ivan" || !loaded.Consent.Time.Equal(testStart) {
		t.Errorf("the loaded config = %+v, %v", loaded, err)
	}
}
