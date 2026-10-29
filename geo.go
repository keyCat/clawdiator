package main

import (
	"slices"
	"time"

	"clawdiator/internal/geocheck"
)

const (
	// probeServiceName is the location service of the probe. Its trace gives the public IP address and the country.
	probeServiceName = "cloudflare"
	// lookupTimeout is the time limit for each request of a full check outside a pre-block.
	// The access stays open during this check, so a long limit costs the user nothing. A failed check blocks,
	// and a request on a new connection needs about 4 round trips. So a short limit can block a user on a slow network.
	lookupTimeout = 3 * time.Second
	// lookupExtraWait is the maximum time to wait for the other services after the minimum of correct replies.
	// A late reply can name a blocked country, so a normal check waits for it. The full check always waits
	// for the probe service, because the local database needs its IP address.
	lookupExtraWait = time.Second
	// quickLookupTimeout and quickExtraWait replace the 2 values above for the probe and for the full checks
	// during a pre-block. The pre-block stops all access, and some networks and VPNs drop the requests to a service.
	// 1.5 seconds allow a round trip of about 350 ms.
	quickLookupTimeout = 1500 * time.Millisecond
	quickExtraWait     = 300 * time.Millisecond
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
