package geocheck

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Kind is the type of the result of a check.
type Kind int

const (
	// Allowed: the rules allow the location.
	Allowed Kind = iota
	// NotSupported: the country is not on the list of the provider.
	NotSupported
	// Blocked: the country is in the list of blocked countries of the caller.
	Blocked
	// RegionExcluded: the provider excludes the region of the country.
	RegionExcluded
	// RegionUnknown: the provider excludes some regions of the country, and no service gives the region.
	RegionUnknown
	// LookupFailed: the lookup failed, so the country is unknown.
	LookupFailed
)

// String returns the name of the kind, for example "not_supported".
func (k Kind) String() string {
	switch k {
	case Allowed:
		return "allowed"
	case NotSupported:
		return "not_supported"
	case Blocked:
		return "blocked"
	case RegionExcluded:
		return "region_excluded"
	case RegionUnknown:
		return "region_unknown"
	case LookupFailed:
		return "lookup_failed"
	}
	return fmt.Sprintf("kind_%d", int(k))
}

// Verdict is the result of the check for 1 provider.
type Verdict struct {
	Kind     Kind
	Provider Provider
	// Location is the reply that gave the result. If the result is Allowed, it is the first reply.
	Location Location
	// Result holds the replies of all services.
	Result Result
	// Err is the error of the lookup if Kind is LookupFailed.
	Err error
}

// Allowed reports if the provider can get a request from the location.
func (v Verdict) Allowed() bool {
	return v.Kind == Allowed
}

// Reason returns the result in English, for example
// "country CN is not supported by Anthropic (list from 2026-10-14)".
func (v Verdict) Reason() string {
	country, provider := v.Location.Country, v.Provider.DisplayName()
	switch v.Kind {
	case NotSupported:
		return fmt.Sprintf("country %s is not supported by %s (list from %s)", country, provider, ListDate)
	case Blocked:
		return fmt.Sprintf("country %s is in the list of blocked countries", country)
	case RegionExcluded:
		return fmt.Sprintf("region %s of country %s is not supported by %s", displayRegion(v.Location.Region), country, provider)
	case RegionUnknown:
		return fmt.Sprintf("the region in country %s is unknown, and some regions of this country are not supported", country)
	case LookupFailed:
		return fmt.Sprintf("the country is unknown: %v", v.Err)
	}
	return fmt.Sprintf("country %s is supported by %s", country, provider)
}

// ReasonWithReplies returns Reason with the reply of each service in parentheses.
func (v Verdict) ReasonWithReplies() string {
	if v.Kind == LookupFailed {
		return v.Reason()
	}
	return v.Reason() + " (" + v.Result.Describe() + ")"
}

// Check applies the strict rule: the provider is allowed only if the rules allow the location from each reply.
// The first reply that is not allowed gives the result. If lookupErr is not nil, the result is LookupFailed.
// blockedCountries holds more country codes to block. The case of the letters is not important.
func Check(provider Provider, result Result, lookupErr error, blockedCountries []string) Verdict {
	if lookupErr != nil || len(result.Locations) == 0 {
		if lookupErr == nil {
			lookupErr = fmt.Errorf("no location service replied")
		}
		return Verdict{Kind: LookupFailed, Provider: provider, Result: result, Err: lookupErr}
	}
	var first Verdict
	for index, location := range result.Locations {
		// A service without the region can use the region that another service gives for the same country.
		if location.Region == "" {
			location.Region = result.Region(location.Country)
		}
		verdict := CheckLocation(provider, location, blockedCountries)
		verdict.Result = result
		if !verdict.Allowed() {
			return verdict
		}
		if index == 0 {
			first = verdict
		}
	}
	return first
}

// CheckLocation applies the rules to 1 location. The order of the rules:
// blocked countries, the list of the provider with the territory rule, the excluded regions.
func CheckLocation(provider Provider, location Location, blockedCountries []string) Verdict {
	verdict := Verdict{Provider: provider, Location: location}
	country := strings.ToUpper(strings.TrimSpace(location.Country))
	if slices.ContainsFunc(blockedCountries, func(code string) bool { return strings.EqualFold(strings.TrimSpace(code), country) }) {
		verdict.Kind = Blocked
		return verdict
	}
	supported := supportedCountries[provider]
	if !supported[country] {
		owner, isTerritory := territoryOwners[country]
		if !isTerritory || !territoryProviders[provider] || !supported[owner] {
			verdict.Kind = NotSupported
			return verdict
		}
	}
	if parts, hasExclusions := excludedRegions[country]; hasExclusions {
		region := normalizeRegion(location.Region)
		if region == "" || placeholderRegions[region] {
			verdict.Kind = RegionUnknown
			return verdict
		}
		for _, part := range parts {
			if strings.Contains(region, part) {
				verdict.Kind = RegionExcluded
				return verdict
			}
		}
	}
	verdict.Kind = Allowed
	return verdict
}

// placeholderRegions are normalized region values that some services give when they do not know the region.
var placeholderRegions = map[string]bool{"unknown": true, "na": true, "none": true, "null": true, "undefined": true}

// normalizeRegion keeps only the letters of the region name, in lowercase.
// Then "Luhans'k Oblast" and "Luhansk Oblast" both contain "luhansk".
func normalizeRegion(region string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, region)
}
