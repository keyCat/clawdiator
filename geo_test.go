package main

import (
	"slices"
	"testing"

	"geocheck"
)

func TestAddProbeService(t *testing.T) {
	selected, err := geocheck.FindServices([]string{"ipinfo.io", "ipwho.is"})
	if err != nil {
		t.Fatal(err)
	}
	got := geocheck.ServiceNames(addProbeService(selected))
	if !slices.Equal(got, []string{"ipinfo.io", "ipwho.is", probeServiceName}) {
		t.Errorf("the full check must also ask the probe service: %v", got)
	}
	if len(selected) != 2 {
		t.Error("the function must not change the slice of the caller")
	}
	defaults := geocheck.DefaultServices()
	if got := geocheck.ServiceNames(addProbeService(defaults)); !slices.Equal(got, geocheck.ServiceNames(defaults)) {
		t.Errorf("the default services already have the probe service: %v", got)
	}
}
