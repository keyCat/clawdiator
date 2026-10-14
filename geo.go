package main

import (
	"slices"
	"time"

	"geocheck"
)

const (
	// probeServiceName is the location service of the probe. Its trace gives the public IP address and the country.
	probeServiceName = "cloudflare"
	// lookupTimeout is the time limit for each request of the probe and of the full check.
	// A pre-block lasts until a full check passes, so a slow service must not stop the check for long.
	lookupTimeout = 3 * time.Second
	// lookupExtraWait is the maximum time to wait for the other services after the minimum of correct replies.
	// The full check always waits for the probe service, because the local database needs its IP address.
	lookupExtraWait = time.Second
)

// addProbeService adds the probe service to the services of the full check, if it is not there.
// If the full check does not ask the probe service, the probe and the full check can give different countries.
// Then the probe blocks, the full check unblocks, and the block starts and stops every 30 seconds.
func addProbeService(services []geocheck.Service) []geocheck.Service {
	for _, service := range services {
		if service.Name == probeServiceName {
			return services
		}
	}
	probe, err := geocheck.FindServices([]string{probeServiceName})
	if err != nil {
		return services
	}
	return append(slices.Clone(services), probe...)
}
