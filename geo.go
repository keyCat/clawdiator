package main

import (
	"slices"

	"geocheck"
)

// probeServiceName is the location service of the probe. Its trace gives the public IP address and the country.
const probeServiceName = "cloudflare"

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
