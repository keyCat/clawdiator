package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"clawdiator/internal/geocheck"
)

// consentVersion is the version of the consent text. Increase it when the text changes. Then install asks again.
const consentVersion = 4

// Config is the content of config.json.
type Config struct {
	// GeoServices holds the names of the location services of the full check. An empty list means the default services of geocheck.
	GeoServices []string `json:"geo_services,omitempty"`
	// GeoMinReplies is the minimum number of correct replies. 0 means the default value.
	GeoMinReplies int `json:"geo_min_replies,omitempty"`
	// BlockedCountries holds more country codes to block for both providers, for example ["AF"].
	BlockedCountries []string `json:"blocked_countries,omitempty"`
	// AutoUpdate lets the service install a new version without the user. It is off until a user turns it on.
	AutoUpdate bool    `json:"auto_update,omitempty"`
	Consent    consent `json:"consent"`
}

// consent records the agreement of the user to the block.
type consent struct {
	Time    time.Time `json:"time"`
	Version int       `json:"version"`
	User    string    `json:"user,omitempty"`
}

// loadConfig reads config.json.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// saveConfig writes config.json through a temporary file. All users can read it.
func saveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.Join(err, os.Remove(temporaryPath))
	}
	return nil
}

// getGeoOptions returns the options of the full check. The full check always asks the probe service too.
// The errors are in the current language.
func getGeoOptions(cfg *Config) (geocheck.Options, error) {
	for _, code := range cfg.BlockedCountries {
		if !isCountryCode(strings.ToUpper(strings.TrimSpace(code))) {
			return geocheck.Options{}, errorf(msgCountryCodeInvalid, code)
		}
	}
	services, err := geocheck.FindServices(cfg.GeoServices)
	var unknown *geocheck.UnknownServiceError
	if errors.As(err, &unknown) {
		return geocheck.Options{}, errorf(msgUnknownGeoService, unknown.Name, strings.Join(geocheck.ServiceNames(geocheck.Services()), ", "))
	}
	if err != nil {
		return geocheck.Options{}, err
	}
	minReplies := cfg.GeoMinReplies
	if minReplies == 0 {
		minReplies = geocheck.DefaultMinRepliesFor(len(services))
	}
	if minReplies < 1 || minReplies > len(services) {
		return geocheck.Options{}, errorf(msgMinRepliesInvalid, len(services))
	}
	return geocheck.Options{
		Services: addProbeService(services), MinReplies: minReplies, UserAgent: appName, Client: lookupClient,
		Timeout: lookupTimeout, ExtraWait: lookupExtraWait, WaitFor: []string{probeServiceName},
	}, nil
}

// getProbeOptions returns the options of the probe: 1 request to the probe service, without a retry.
func getProbeOptions() geocheck.Options {
	services, _ := geocheck.FindServices([]string{probeServiceName})
	return geocheck.Options{Services: services, MinReplies: 1, Rounds: 1, UserAgent: appName, Client: lookupClient, Timeout: lookupTimeout}
}

// lookupClient opens a new connection for each request. A kept connection can stay on the route from before
// a change of the network. Then the checks after a start of a VPN still go out without the VPN.
var lookupClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	return &http.Client{Transport: transport}
}()

func isCountryCode(code string) bool {
	return len(code) == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z'
}
