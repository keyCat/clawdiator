package geocheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Service is a location service that gives the country of the public IP address.
type Service struct {
	// Name is the ID of the service, for example "cloudflare".
	Name string
	URL  string
	// Parse reads the location from the body of the reply.
	Parse func(body []byte) (Location, error)
	// IsDefault marks the services of DefaultServices.
	IsDefault bool
	// HasRegion marks the services that give the region.
	HasRegion bool
	// IsUndocumented marks an endpoint without public documentation. It can stop working at any time.
	IsUndocumented bool
}

// allServices holds all known services. The tests on 2026-10-14 got a correct reply from each.
var allServices = []Service{
	{Name: "cloudflare", URL: "https://www.cloudflare.com/cdn-cgi/trace", Parse: ParseCloudflareTrace, IsDefault: true},
	{Name: "country.is", URL: "https://api.country.is/", Parse: ParseJSONFields("country", "")},
	{Name: "ipinfo.io", URL: "https://ipinfo.io/json", Parse: ParseJSONFields("country", "region"), HasRegion: true},
	{Name: "2ip.io", URL: "https://api.2ip.io/", Parse: ParseJSONFields("code", "region"), HasRegion: true, IsUndocumented: true},
	{Name: "ipapi.co", URL: "https://ipapi.co/json/", Parse: ParseJSONFields("country_code", "region"), HasRegion: true},
	{Name: "ipwho.is", URL: "https://ipwho.is/", Parse: ParseJSONFields("country_code", "region"), IsDefault: true, HasRegion: true},
	{Name: "freeipapi.com", URL: "https://freeipapi.com/api/json", Parse: ParseJSONFields("countryCode", "regionName"), HasRegion: true},
	{Name: "geojs.io", URL: "https://get.geojs.io/v1/ip/geo.json", Parse: ParseJSONFields("country_code", "region"), IsDefault: true, HasRegion: true},
	{Name: "ipquery.io", URL: "https://api.ipquery.io/?format=json", Parse: ParseJSONFields("location.country_code", "location.state"), HasRegion: true},
	{Name: "ip2location.io", URL: "https://api.ip2location.io/", Parse: ParseJSONFields("country_code", "region_name"), HasRegion: true},
	{Name: "ifconfig.co", URL: "https://ifconfig.co/json", Parse: ParseJSONFields("country_iso", "region_name"), HasRegion: true},
	{Name: "whoer.net", URL: "https://whoer.net/main/api/ip", Parse: ParseJSONFields("data.iso", "data.region"), HasRegion: true, IsUndocumented: true},
}

// Services returns all known services. The result is a copy, so a change to it has no effect on the module.
func Services() []Service {
	return append([]Service(nil), allServices...)
}

// DefaultServices returns the default services: cloudflare, ipwho.is and geojs.io.
// On 2026-10-15, only these services replied from all countries without a strict limit.
// The other services refuse requests from some unsupported countries, or they have strict request limits.
func DefaultServices() []Service {
	var services []Service
	for _, service := range allServices {
		if service.IsDefault {
			services = append(services, service)
		}
	}
	return services
}

// DefaultServiceNames returns the names of the default services.
func DefaultServiceNames() []string {
	return ServiceNames(DefaultServices())
}

// ServiceNames returns the names of the services.
func ServiceNames(services []Service) []string {
	names := make([]string, len(services))
	for index, service := range services {
		names[index] = service.Name
	}
	return names
}

// UnknownServiceError tells that no known service has the name.
type UnknownServiceError struct {
	Name string
}

func (e *UnknownServiceError) Error() string {
	return fmt.Sprintf("the location service %q is not known. Use %s", e.Name, strings.Join(ServiceNames(allServices), ", "))
}

// FindServices returns the services with the names, in the order of Services.
// The case of the letters is not important. An empty list gives the default services.
func FindServices(names []string) ([]Service, error) {
	if len(names) == 0 {
		return DefaultServices(), nil
	}
	isSelected := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		isKnown := false
		for _, service := range allServices {
			if service.Name == key {
				isKnown = true
			}
		}
		if !isKnown {
			return nil, &UnknownServiceError{Name: name}
		}
		isSelected[key] = true
	}
	var services []Service
	for _, service := range allServices {
		if isSelected[service.Name] {
			services = append(services, service)
		}
	}
	return services, nil
}

// reply is the result of the request to 1 service.
type reply struct {
	index    int
	location Location
	err      error
}

// lookupOnce asks the services at the same time. Each location stays as the service sent it.
// With ExtraWait, it stops the requests that are not done at ExtraWait after MinReplies correct replies.
// It does not stop the requests to the WaitFor services. If OnReply returns true, it stops no request.
func lookupOnce(ctx context.Context, options Options) (Result, error) {
	services := options.Services
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The buffer has a place for each reply, so a request that ends after the return does not block.
	replies := make(chan reply, len(services))
	for index, service := range services {
		go func(index int, service Service) {
			location, err := fetchLocation(ctx, service, options)
			replies <- reply{index: index, location: location, err: err}
		}(index, service)
	}

	locations := make([]Location, len(services))
	errs := make([]error, len(services))
	isDone := make([]bool, len(services))
	doneCount, correctCount := 0, 0
	// A nil channel never sends, so the extra wait has no end until the timer starts.
	var extraWaitEnd <-chan time.Time
	isExtraWaitOver, isWaitForAll := false, false
	for doneCount < len(services) {
		if isExtraWaitOver && !isWaitForAll && isWaitForDone(services, options.WaitFor, isDone) {
			break
		}
		select {
		case next := <-replies:
			locations[next.index], errs[next.index] = next.location, next.err
			isDone[next.index] = true
			doneCount++
			if next.err != nil {
				continue
			}
			correctCount++
			if options.OnReply != nil && options.OnReply(next.location) {
				isWaitForAll = true
			}
			if correctCount == options.MinReplies && options.ExtraWait > 0 {
				timer := time.NewTimer(options.ExtraWait)
				defer timer.Stop()
				extraWaitEnd = timer.C
			}
		case <-extraWaitEnd:
			isExtraWaitOver = true
		}
	}
	for index := range services {
		if !isDone[index] {
			errs[index] = fmt.Errorf("no reply in %v after the minimum of replies", options.ExtraWait)
		}
	}

	var result Result
	for index, service := range services {
		if errs[index] != nil {
			result.Failures = append(result.Failures, service.Name+": "+cleanText(errs[index].Error()))
			continue
		}
		result.Locations = append(result.Locations, locations[index])
	}
	if len(result.Locations) == 0 {
		return Result{}, fmt.Errorf("no location service replied (%s)", strings.Join(result.Failures, "; "))
	}
	if len(result.Locations) < options.MinReplies {
		return Result{}, fmt.Errorf("%d of %d location services replied, but the minimum is %d (%s)",
			len(result.Locations), len(services), options.MinReplies, result.Describe())
	}
	return result, nil
}

// isWaitForDone tells if each service with a name in waitFor is done.
func isWaitForDone(services []Service, waitFor []string, isDone []bool) bool {
	for index, service := range services {
		if !isDone[index] && slices.Contains(waitFor, service.Name) {
			return false
		}
	}
	return true
}

func fetchLocation(ctx context.Context, service Service, options Options) (Location, error) {
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, service.URL, nil)
	if err != nil {
		return Location{}, err
	}
	request.Header.Set("User-Agent", options.UserAgent)
	response, err := options.Client.Do(request)
	if err != nil {
		return Location{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Location{}, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Location{}, err
	}
	location, err := service.Parse(body)
	if err != nil {
		return Location{}, err
	}
	location.Country = strings.ToUpper(strings.TrimSpace(location.Country))
	if !isCountryCode(location.Country) {
		return Location{}, fmt.Errorf("the reply has no country code")
	}
	// The rules read the full region. Only the texts for people cut a long region, see displayRegion.
	location.Region = strings.TrimSpace(cleanText(location.Region))
	// A value that is not an IP address can contain any text, so the lookup removes it.
	if _, err := netip.ParseAddr(location.IP); err != nil {
		location.IP = ""
	}
	location.Source = service.Name
	return location, nil
}

// cleanText changes each space character to 1 space and removes the characters that are not printable.
// A service can send an escape sequence that changes the terminal, or a line break that adds a false log line.
func cleanText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, text)
}

// maxRegionRunes is the maximum length of a region in a text for people.
const maxRegionRunes = 64

// displayRegion cuts a long region for a text for people. Do not use the result for the rules:
// a cut region can lose the part that the rules exclude, for example "Crimea".
func displayRegion(region string) string {
	runes := []rune(region)
	if len(runes) <= maxRegionRunes {
		return region
	}
	return string(runes[:maxRegionRunes]) + "…"
}

func isCountryCode(code string) bool {
	return len(code) == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z'
}

// ParseCloudflareTrace reads the "loc=" and "ip=" lines of the Cloudflare trace. The "loc=" line is necessary.
func ParseCloudflareTrace(body []byte) (Location, error) {
	var location Location
	hasCountry := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if value, found := strings.CutPrefix(line, "loc="); found {
			location.Country, hasCountry = value, true
		}
		if value, found := strings.CutPrefix(line, "ip="); found {
			location.IP = value
		}
	}
	if !hasCountry {
		return Location{}, fmt.Errorf("the reply has no loc line")
	}
	return location, nil
}

// ParseJSONFields returns a parser that reads the country and the region from JSON fields.
// A path such as "location.country_code" goes into nested objects. An empty region path gives no region.
func ParseJSONFields(countryPath, regionPath string) func(body []byte) (Location, error) {
	return func(body []byte) (Location, error) {
		var data any
		if err := json.Unmarshal(body, &data); err != nil {
			return Location{}, err
		}
		return Location{Country: getJSONString(data, countryPath), Region: getJSONString(data, regionPath)}, nil
	}
}

func getJSONString(data any, path string) string {
	if path == "" {
		return ""
	}
	for _, key := range strings.Split(path, ".") {
		object, isObject := data.(map[string]any)
		if !isObject {
			return ""
		}
		data = object[key]
	}
	value, _ := data.(string)
	return strings.TrimSpace(value)
}
