// Package geocheck finds the country of the public IP address and checks it
// against the supported countries of Anthropic and OpenAI.
//
// Lookup asks several location services at the same time. Check applies the strict rule:
// a provider is allowed only if the location from each service that replied is allowed.
// If the lookup fails, Check does not allow the provider.
package geocheck

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Provider is a company with an official list of supported countries.
type Provider string

const (
	Anthropic Provider = "anthropic"
	OpenAI    Provider = "openai"
)

// Providers returns all providers.
func Providers() []Provider {
	return []Provider{Anthropic, OpenAI}
}

// ParseProvider returns the provider with the name, for example "anthropic". The case of the letters is not important.
func ParseProvider(name string) (Provider, error) {
	for _, provider := range Providers() {
		if strings.EqualFold(strings.TrimSpace(name), string(provider)) {
			return provider, nil
		}
	}
	return "", fmt.Errorf("the provider %q is not known. Use anthropic or openai", name)
}

// DisplayName returns the name of the provider for text, for example "Anthropic".
func (p Provider) DisplayName() string {
	switch p {
	case Anthropic:
		return "Anthropic"
	case OpenAI:
		return "OpenAI"
	}
	return string(p)
}

// SupportedCountries returns the sorted ISO 3166-1 alpha-2 codes from the official list of the provider.
// The list does not include the territories that the territory rule allows.
func SupportedCountries(provider Provider) []string {
	var codes []string
	for code := range supportedCountries[provider] {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

const (
	// DefaultMinReplies is the default minimum number of correct replies. With the 3 default services, 1 service can fail.
	DefaultMinReplies = 2
	// DefaultTimeout is the default time limit for each request to a location service.
	DefaultTimeout = 5 * time.Second
	// DefaultRoundDelay is the default time between 2 lookups.
	DefaultRoundDelay = 10 * time.Second
	// DefaultUserAgent is the default User-Agent header of the requests.
	DefaultUserAgent = "geocheck"
)

// DefaultMinRepliesFor returns the default minimum for the number of services.
// With fewer services than DefaultMinReplies, the default is the number of services.
func DefaultMinRepliesFor(serviceCount int) int {
	return min(DefaultMinReplies, serviceCount)
}

// Options holds the settings of a lookup. The zero value gives the default settings.
type Options struct {
	// Services are the location services to ask. If the list is empty, Lookup uses DefaultServices.
	Services []Service
	// MinReplies is the minimum number of correct replies. If it is 0, Lookup uses DefaultMinRepliesFor.
	MinReplies int
	// Rounds is the number of lookups if a lookup fails. If it is 0, Lookup does 1 lookup.
	Rounds int
	// RoundDelay is the time between 2 lookups. If it is 0, Lookup uses DefaultRoundDelay.
	RoundDelay time.Duration
	// Timeout is the time limit for each request. If it is 0, Lookup uses DefaultTimeout.
	Timeout time.Duration
	// ExtraWait is the maximum time to wait for the other services after MinReplies correct replies.
	// Then Lookup stops the other requests, and these services count as failures.
	// If it is 0, Lookup waits for all services.
	ExtraWait time.Duration
	// WaitFor holds the names of the services that Lookup always waits for, also after ExtraWait.
	// Lookup waits for a correct reply, an error or the timeout. A name that is not in Services has no effect.
	WaitFor []string
	// OnReply runs for each correct reply when the reply comes. If it returns true, Lookup waits for all services,
	// also after ExtraWait. It runs in the goroutine of Lookup, 1 call at a time, so it must return fast.
	OnReply func(location Location) (isWaitForAll bool)
	// Client sends the requests. If it is nil, Lookup uses http.DefaultClient.
	Client *http.Client
	// UserAgent is the User-Agent header. If it is empty, Lookup uses DefaultUserAgent.
	UserAgent string
}

// MinRepliesError tells that the minimum is not from 1 to the number of services.
type MinRepliesError struct {
	MinReplies   int
	ServiceCount int
}

func (e *MinRepliesError) Error() string {
	return fmt.Sprintf("the minimum of replies (%d) must be from 1 to the number of services (%d)", e.MinReplies, e.ServiceCount)
}

// withDefaults returns the options with the default values in the empty fields.
func (o Options) withDefaults() (Options, error) {
	if len(o.Services) == 0 {
		o.Services = DefaultServices()
	}
	if o.MinReplies == 0 {
		o.MinReplies = DefaultMinRepliesFor(len(o.Services))
	}
	if o.MinReplies < 1 || o.MinReplies > len(o.Services) {
		return o, &MinRepliesError{MinReplies: o.MinReplies, ServiceCount: len(o.Services)}
	}
	if o.Rounds < 1 {
		o.Rounds = 1
	}
	if o.RoundDelay == 0 {
		o.RoundDelay = DefaultRoundDelay
	}
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Client == nil {
		o.Client = http.DefaultClient
	}
	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}
	return o, nil
}

// Lookup asks the services at the same time and returns their replies.
// It fails if fewer services reply correctly than the minimum. Then it tries again, up to Options.Rounds times.
func Lookup(ctx context.Context, options Options) (Result, error) {
	options, err := options.withDefaults()
	if err != nil {
		return Result{}, err
	}
	var lastErr error
	for round := 0; round < options.Rounds; round++ {
		if round > 0 {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(options.RoundDelay):
			}
		}
		result, err := lookupOnce(ctx, options)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return Result{}, lastErr
}

// Location is the location of the public IP address from 1 service.
type Location struct {
	Country string `json:"country"`
	Region  string `json:"region,omitempty"`
	// IP is the public IP address that the service saw. Only some services give it.
	IP string `json:"ip,omitempty"`
	// Source is the name of the service.
	Source string `json:"service"`
}

// Result holds the replies of all services. Different services can give different countries.
type Result struct {
	// Locations holds the correct replies, in the order of the services.
	Locations []Location `json:"locations"`
	// Failures holds a "name: reason" text for each service that did not reply correctly.
	Failures []string `json:"failures,omitempty"`
}

// Region returns the first region that a service gives for the country. Only some services give a region.
func (r Result) Region(country string) string {
	for _, location := range r.Locations {
		if location.Country == country && location.Region != "" {
			return location.Region
		}
	}
	return ""
}

// Countries returns the different countries of the replies, with the region if a service gives it.
// For example: "DE, Hesse".
func (r Result) Countries() []string {
	var countries, places []string
	for _, location := range r.Locations {
		if slices.Contains(countries, location.Country) {
			continue
		}
		countries = append(countries, location.Country)
		place := location.Country
		if region := r.Region(location.Country); region != "" {
			place += ", " + displayRegion(region)
		}
		places = append(places, place)
	}
	return places
}

// Describe returns the reply of each service, for example "cloudflare: DE; ipinfo.io: DE, Hesse".
// A service without a correct reply shows its error.
func (r Result) Describe() string {
	var parts []string
	for _, location := range r.Locations {
		part := location.Source + ": " + location.Country
		if location.Region != "" {
			part += ", " + displayRegion(location.Region)
		}
		parts = append(parts, part)
	}
	return strings.Join(append(parts, r.Failures...), "; ")
}
