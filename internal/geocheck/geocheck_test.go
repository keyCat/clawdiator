package geocheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckLocation(t *testing.T) {
	cases := []struct {
		provider Provider
		country  string
		region   string
		blocked  []string
		want     Kind
	}{
		{Anthropic, "DE", "", nil, Allowed},
		{OpenAI, "DE", "", nil, Allowed},
		{Anthropic, "CN", "", nil, NotSupported},
		{OpenAI, "CN", "", nil, NotSupported},
		{Anthropic, "HK", "", nil, NotSupported},
		{Anthropic, "RU", "", nil, NotSupported},
		{OpenAI, "RU", "", nil, NotSupported},
		{Anthropic, "IR", "", nil, NotSupported},
		{Anthropic, "AF", "", nil, NotSupported},
		{OpenAI, "AF", "", nil, Allowed},
		{OpenAI, "AF", "", []string{"af"}, Blocked},
		{Anthropic, "PR", "", nil, Allowed},
		{OpenAI, "PR", "", nil, NotSupported},
		{Anthropic, "UA", "Kyiv City", nil, Allowed},
		{Anthropic, "UA", "Crimea", nil, RegionExcluded},
		{OpenAI, "UA", "Zaporizhzhia Oblast", nil, RegionExcluded},
		{Anthropic, "UA", "", nil, RegionUnknown},
		{Anthropic, "UA", "Luhans'k Oblast", nil, RegionExcluded},
		{OpenAI, "UA", "Donets'k", nil, RegionExcluded},
		{Anthropic, "UA", "Avtonomna Respublika Krym", nil, RegionExcluded},
		{Anthropic, "UA", "Запорожская область", nil, RegionExcluded},
	}
	for _, testCase := range cases {
		got := CheckLocation(testCase.provider, Location{Country: testCase.country, Region: testCase.region}, testCase.blocked)
		if got.Kind != testCase.want {
			t.Errorf("CheckLocation(%s, %s, %q) = %s, want %s", testCase.provider, testCase.country, testCase.region, got.Kind, testCase.want)
		}
	}
}

func TestCheckStrict(t *testing.T) {
	germany := Location{Country: "DE", Source: "a"}
	china := Location{Country: "CN", Source: "b"}
	netherlands := Location{Country: "NL", Source: "c"}
	if verdict := Check(Anthropic, Result{Locations: []Location{germany, china}}, nil, nil); verdict.Allowed() || verdict.Location.Country != "CN" {
		t.Errorf("1 reply with a blocked country must stop the request: %+v", verdict)
	}
	if verdict := Check(Anthropic, Result{Locations: []Location{germany, netherlands}}, nil, nil); !verdict.Allowed() || verdict.Location.Country != "DE" {
		t.Errorf("different allowed countries must allow the request: %+v", verdict)
	}
	ukraine := Location{Country: "UA", Source: "a"}
	kyiv := Location{Country: "UA", Region: "Kyiv City", Source: "b"}
	if verdict := Check(Anthropic, Result{Locations: []Location{ukraine, kyiv}}, nil, nil); !verdict.Allowed() {
		t.Errorf("a reply without a region must use the region from another reply: %+v", verdict)
	}
	if verdict := Check(Anthropic, Result{Locations: []Location{ukraine}}, nil, nil); verdict.Kind != RegionUnknown {
		t.Errorf("Ukraine without a region must stop the request: %+v", verdict)
	}
	if verdict := Check(OpenAI, Result{}, errors.New("offline"), nil); verdict.Kind != LookupFailed || verdict.Allowed() {
		t.Errorf("a failed lookup must stop the request: %+v", verdict)
	}
	if verdict := Check(OpenAI, Result{}, nil, nil); verdict.Kind != LookupFailed {
		t.Errorf("a result without replies must stop the request: %+v", verdict)
	}
	verdict := Check(Anthropic, Result{Locations: []Location{germany, china}, Failures: []string{"x: timeout"}}, nil, nil)
	if got := verdict.ReasonWithReplies(); got != "country CN is not supported by Anthropic (list from 2026-10-14) (a: DE; b: CN; x: timeout)" {
		t.Errorf("ReasonWithReplies = %q", got)
	}
}

func TestParseReplies(t *testing.T) {
	trace, err := ParseCloudflareTrace([]byte("fl=1\nip=192.0.2.1\nloc=DE\ntls=TLSv1.3\n"))
	if err != nil || trace.Country != "DE" || trace.IP != "192.0.2.1" {
		t.Errorf("ParseCloudflareTrace = %+v, %v", trace, err)
	}
	// The order of the lines is not important.
	trace, err = ParseCloudflareTrace([]byte("loc=NL\nip=2001:db8::1\n"))
	if err != nil || trace.Country != "NL" || trace.IP != "2001:db8::1" {
		t.Errorf("ParseCloudflareTrace with ip after loc = %+v, %v", trace, err)
	}
	if _, err := ParseCloudflareTrace([]byte("ip=192.0.2.1\n")); err == nil {
		t.Error("a trace without loc must fail")
	}
	nested, err := ParseJSONFields("location.country_code", "location.state")([]byte(`{"location":{"country_code":"UA","state":"Kyiv City"}}`))
	if err != nil || nested.Country != "UA" || nested.Region != "Kyiv City" {
		t.Errorf("ParseJSONFields with nested fields = %+v, %v", nested, err)
	}
	if _, err := ParseJSONFields("country", "")([]byte("<html>")); err == nil {
		t.Error("a reply that is not JSON must fail")
	}
}

// TestParseEachService checks the parser of each service with the shape of a real reply from 2026-10-14.
func TestParseEachService(t *testing.T) {
	replies := map[string]string{
		"cloudflare":     "ip=192.0.2.1\nloc=DE\n",
		"country.is":     `{"ip":"192.0.2.1","country":"DE"}`,
		"ipinfo.io":      `{"ip":"192.0.2.1","country":"DE","region":"Hesse"}`,
		"2ip.io":         `{"ip":"192.0.2.1","region":"Hesse","country":"Germany","code":"DE"}`,
		"ipapi.co":       `{"ip":"192.0.2.1","region":"Hesse","country_code":"DE"}`,
		"ipwho.is":       `{"ip":"192.0.2.1","success":true,"region":"Hessen","country_code":"DE"}`,
		"freeipapi.com":  `{"ipAddress":"192.0.2.1","countryCode":"DE","regionName":"Hesse"}`,
		"geojs.io":       `{"ip":"192.0.2.1","country_code":"DE","region":"Hesse"}`,
		"ipquery.io":     `{"ip":"192.0.2.1","location":{"country_code":"DE","state":"Hesse"}}`,
		"ip2location.io": `{"ip":"192.0.2.1","country_code":"DE","region_name":"Hessen"}`,
		"ifconfig.co":    `{"ip":"192.0.2.1","country_iso":"DE","region_name":"Hesse"}`,
		"whoer.net":      `{"data":{"iso":"de","region":"Hesse","country":"Germany"},"status":"OK"}`,
	}
	for _, service := range Services() {
		reply, hasReply := replies[service.Name]
		if !hasReply {
			t.Errorf("the test has no reply for %s", service.Name)
			continue
		}
		location, err := service.Parse([]byte(reply))
		if err != nil || !strings.EqualFold(location.Country, "DE") {
			t.Errorf("%s: Parse = %+v, %v", service.Name, location, err)
		}
		if service.HasRegion != (location.Region != "") {
			t.Errorf("%s: HasRegion is %t, but the region is %q", service.Name, service.HasRegion, location.Region)
		}
	}
}

func TestFindServices(t *testing.T) {
	if got := strings.Join(DefaultServiceNames(), ","); got != "cloudflare,ipwho.is,geojs.io" {
		t.Errorf("DefaultServiceNames = %s", got)
	}
	services, err := FindServices([]string{"WHOER.NET", " ipwho.is "})
	if err != nil || strings.Join(ServiceNames(services), ",") != "ipwho.is,whoer.net" {
		t.Errorf("FindServices = %v, %v", ServiceNames(services), err)
	}
	var unknown *UnknownServiceError
	if _, err := FindServices([]string{"example.com"}); !errors.As(err, &unknown) || unknown.Name != "example.com" {
		t.Errorf("an unknown service must give UnknownServiceError: %v", err)
	}
	if services, err := FindServices(nil); err != nil || len(services) != 3 {
		t.Errorf("an empty list must give the default services: %v, %v", ServiceNames(services), err)
	}
	all := Services()
	all[0].Name = "changed"
	if Services()[0].Name != "cloudflare" {
		t.Error("Services must return a copy")
	}
}

func TestOptionsDefaults(t *testing.T) {
	options, err := Options{}.withDefaults()
	if err != nil || len(options.Services) != 3 || options.MinReplies != 2 || options.Rounds != 1 ||
		options.Timeout != DefaultTimeout || options.RoundDelay != DefaultRoundDelay || options.UserAgent != DefaultUserAgent {
		t.Errorf("Options{}.withDefaults() = %+v, %v", options, err)
	}
	two, _ := FindServices([]string{"ipwho.is", "geojs.io"})
	if options, err := (Options{Services: two}).withDefaults(); err != nil || options.MinReplies != 2 {
		t.Errorf("with 2 services, the default minimum must be 2: %d, %v", options.MinReplies, err)
	}
	var minErr *MinRepliesError
	if _, err := (Options{Services: two, MinReplies: 3}).withDefaults(); !errors.As(err, &minErr) || minErr.ServiceCount != 2 {
		t.Errorf("a minimum above the number of services must give MinRepliesError: %v", err)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		case "/country":
			w.Write([]byte(`{"country":"ua"}`))
		case "/region":
			if r.Header.Get("User-Agent") != "test-agent" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Write([]byte(`{"country":"UA","region":"Kyiv City"}`))
		case "/other":
			w.Write([]byte(`{"country":"PL"}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLookup(t *testing.T) {
	server := newTestServer(t)
	countryParser, regionParser := ParseJSONFields("country", ""), ParseJSONFields("country", "region")
	services := []Service{
		{Name: "broken", URL: server.URL + "/broken", Parse: countryParser},
		{Name: "country", URL: server.URL + "/country", Parse: countryParser},
		{Name: "region", URL: server.URL + "/region", Parse: regionParser},
		{Name: "other", URL: server.URL + "/other", Parse: countryParser},
	}
	options := Options{Services: services, MinReplies: 1, UserAgent: "test-agent"}
	result, err := Lookup(context.Background(), options)
	if err != nil || len(result.Locations) != 3 || len(result.Failures) != 1 {
		t.Fatalf("Lookup = %+v, %v", result, err)
	}
	if result.Locations[0].Source != "country" || result.Locations[0].Region != "" {
		t.Errorf("each reply must stay as the service sent it: %+v", result.Locations[0])
	}
	if got := strings.Join(result.Countries(), " / "); got != "UA, Kyiv City / PL" {
		t.Errorf("Countries = %q", got)
	}
	if got := result.Describe(); got != "country: UA; region: UA, Kyiv City; other: PL; broken: HTTP 500" {
		t.Errorf("Describe = %q", got)
	}

	options.MinReplies = 3
	if _, err := Lookup(context.Background(), options); err != nil {
		t.Errorf("3 correct replies must meet the minimum of 3: %v", err)
	}
	options.MinReplies = 4
	if _, err := Lookup(context.Background(), options); err == nil || !strings.Contains(err.Error(), "3 of 4 location services replied, but the minimum is 4") {
		t.Errorf("3 correct replies must not meet the minimum of 4: %v", err)
	}
	options.Services, options.MinReplies = services[:1], 1
	if _, err := Lookup(context.Background(), options); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("Lookup must fail with the reason: %v", err)
	}
}

func TestLookupRounds(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callCount.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"country":"DE"}`))
	}))
	defer server.Close()
	options := Options{
		Services: []Service{{Name: "flaky", URL: server.URL, Parse: ParseJSONFields("country", "")}},
		Rounds:   3, RoundDelay: time.Millisecond,
	}
	result, err := Lookup(context.Background(), options)
	if err != nil || result.Locations[0].Country != "DE" || callCount.Load() != 3 {
		t.Errorf("the third round must succeed: %+v, %v, %d calls", result, err, callCount.Load())
	}
	callCount.Store(-10)
	if _, err := Lookup(context.Background(), options); err == nil || callCount.Load() != -7 {
		t.Errorf("after 3 failed rounds, Lookup must fail: %v, %d", err, callCount.Load())
	}
}

func TestProviders(t *testing.T) {
	if provider, err := ParseProvider(" OpenAI "); err != nil || provider != OpenAI {
		t.Errorf("ParseProvider = %s, %v", provider, err)
	}
	if _, err := ParseProvider("google"); err == nil {
		t.Error("an unknown provider must fail")
	}
	anthropic, openai := SupportedCountries(Anthropic), SupportedCountries(OpenAI)
	if len(anthropic) != 185 || len(openai) != 208 || !slices.IsSorted(anthropic) {
		t.Errorf("the lists have %d and %d countries", len(anthropic), len(openai))
	}
	if Blocked.String() != "blocked" || LookupFailed.String() != "lookup_failed" {
		t.Error("Kind.String must give the names for JSON")
	}
}

// TestCheckLocationUkraineRegions checks finding C3 of the review: the excluded regions of Ukraine
// in Latin, Russian and Ukrainian letters, and the names of allowed regions.
func TestCheckLocationUkraineRegions(t *testing.T) {
	excluded := []string{
		"Crimea", "Autonomous Republic of Crimea", "Avtonomna Respublika Krym", "Sevastopol", "Sevastopol' City",
		"Donetsk Oblast", "Donets'ka Oblast'", "Luhansk", "Lugansk", "Luhans'ka Oblast'",
		"Kherson Oblast", "Khersons'ka", "Zaporizhzhia Oblast", "Zaporizhia", "Zaporozhye", "Zaporiz'ka Oblast'",
		"Республика Крым", "Севастополь", "Донецкая область", "Луганская область", "Херсонская область", "Запорожская область",
		"Автономна Республіка Крим", "Донецька область", "Луганська область", "Херсонська область", "Запорізька область",
	}
	for _, region := range excluded {
		for _, provider := range Providers() {
			if got := CheckLocation(provider, Location{Country: "UA", Region: region}, nil).Kind; got != RegionExcluded {
				t.Errorf("%s, %q = %s, want region_excluded", provider, region, got)
			}
		}
	}
	for _, region := range []string{"Unknown", "N/A", "none", "-", ""} {
		if got := CheckLocation(Anthropic, Location{Country: "UA", Region: region}, nil).Kind; got != RegionUnknown {
			t.Errorf("the placeholder %q = %s, want region_unknown", region, got)
		}
	}
	for _, region := range []string{"Kyiv City", "Lviv Oblast", "Київ", "Харківська область", "Одесская область", "Dnipropetrovsk Oblast", "Mykolaiv"} {
		if got := CheckLocation(Anthropic, Location{Country: "UA", Region: region}, nil).Kind; got != Allowed {
			t.Errorf("the allowed region %q = %s, want allowed", region, got)
		}
	}
}

// TestLookupCleansReplies checks finding W16 of the review: a service cannot send control characters
// to the terminal or to the log. A long region stays complete for the rules, so a cut cannot remove an excluded region.
func TestLookupCleansReplies(t *testing.T) {
	longRegion := strings.Repeat("a", 70) + " Crimea"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/escape":
			w.Write([]byte(`{"country":"DE","region":"Hesse\u001b[2J\nFAKE LOG LINE\r"}`))
		case "/long":
			w.Write([]byte(`{"country":"UA","region":"` + longRegion + `"}`))
		}
	}))
	defer server.Close()
	parser := ParseJSONFields("country", "region")
	services := []Service{{Name: "escape", URL: server.URL + "/escape", Parse: parser}, {Name: "long", URL: server.URL + "/long", Parse: parser}}
	result, err := Lookup(context.Background(), Options{Services: services, MinReplies: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Locations[0].Region; got != "Hesse[2J FAKE LOG LINE" {
		t.Errorf("the region must have no control characters: %q", got)
	}
	texts := result.Describe() + " " + strings.Join(result.Countries(), " ")
	if strings.ContainsAny(texts, "\x1b\n\r") || strings.Contains(texts, longRegion) || !strings.Contains(texts, "…") {
		t.Errorf("the texts must be clean, and a long region must be cut: %q", texts)
	}
	if result.Locations[1].Region != longRegion {
		t.Errorf("the rules must get the full region: %q", result.Locations[1].Region)
	}
	verdict := Check(Anthropic, result, nil, nil)
	if verdict.Kind != RegionExcluded || strings.Contains(verdict.Reason(), longRegion) {
		t.Errorf("an excluded region at the end of a long region must give RegionExcluded with a cut text: %v, %q", verdict.Kind, verdict.Reason())
	}
}

func TestLookupKeepsOnlyCorrectIP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			w.Write([]byte("ip=192.0.2.7\nloc=DE\n"))
		case "/bad":
			w.Write([]byte("ip=192.0.2.7\x1b[2J FAKE\nloc=DE\n"))
		}
	}))
	defer server.Close()
	services := []Service{
		{Name: "good", URL: server.URL + "/good", Parse: ParseCloudflareTrace},
		{Name: "bad", URL: server.URL + "/bad", Parse: ParseCloudflareTrace},
	}
	result, err := Lookup(context.Background(), Options{Services: services, MinReplies: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Locations[0].IP != "192.0.2.7" || result.Locations[1].IP != "" {
		t.Errorf("the lookup must keep a correct IP and remove an incorrect value: %q, %q", result.Locations[0].IP, result.Locations[1].IP)
	}
}

// newDelayServer returns a server with 3 paths: /fast replies at once, /late replies after 100 ms,
// and /never replies only after the client stops the request.
func newDelayServer(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
			return
		case "/late":
			time.Sleep(100 * time.Millisecond)
		case "/never":
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"country":"DE"}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func newDelayServices(server *httptest.Server, paths ...string) []Service {
	var services []Service
	for _, path := range paths {
		services = append(services, Service{Name: path, URL: server.URL + "/" + path, Parse: ParseJSONFields("country", "")})
	}
	return services
}

func TestLookupExtraWait(t *testing.T) {
	server := newDelayServer(t)
	// The guard stops a lookup that waits for /never. Then the test fails, but it does not hang.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	options := Options{Services: newDelayServices(server, "fast", "late"), MinReplies: 1}
	if result, err := Lookup(ctx, options); err != nil || len(result.Locations) != 2 {
		t.Errorf("without ExtraWait, Lookup must wait for all services: %+v, %v", result, err)
	}

	options = Options{Services: newDelayServices(server, "never", "fast", "fast"), MinReplies: 2, ExtraWait: 20 * time.Millisecond}
	result, err := Lookup(ctx, options)
	if err != nil || len(result.Locations) != 2 || ctx.Err() != nil {
		t.Fatalf("after ExtraWait, Lookup must stop the other requests: %+v, %v", result, err)
	}
	if want := []string{"never: no reply in 20ms after the minimum of replies"}; !slices.Equal(result.Failures, want) {
		t.Errorf("a stopped service must count as a failure with its reason: %q", result.Failures)
	}

	start := time.Now()
	options = Options{Services: newDelayServices(server, "fast", "late"), MinReplies: 1, ExtraWait: 5 * time.Second}
	if result, err := Lookup(ctx, options); err != nil || len(result.Locations) != 2 || time.Since(start) > 2*time.Second {
		t.Errorf("a reply in ExtraWait must count, and Lookup must return when all services are done: %+v, %v, %v", result, err, time.Since(start))
	}

	// The broken service replies first, but an error does not start the extra wait.
	options = Options{Services: newDelayServices(server, "broken", "late", "fast"), MinReplies: 2, ExtraWait: 20 * time.Millisecond}
	if result, err := Lookup(ctx, options); err != nil || len(result.Locations) != 2 {
		t.Errorf("only correct replies must count for the minimum: %+v, %v", result, err)
	}
}

func TestLookupWaitFor(t *testing.T) {
	server := newDelayServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := Options{
		Services:   newDelayServices(server, "late", "never", "fast"),
		MinReplies: 1, ExtraWait: 10 * time.Millisecond, WaitFor: []string{"late", "unknown"},
	}
	result, err := Lookup(ctx, options)
	if err != nil || ctx.Err() != nil {
		t.Fatalf("Lookup = %+v, %v", result, err)
	}
	if sources := []string{result.Locations[0].Source, result.Locations[len(result.Locations)-1].Source}; len(result.Locations) != 2 || sources[0] != "late" || sources[1] != "fast" {
		t.Errorf("Lookup must wait for the WaitFor service and keep the order of the services: %+v", result.Locations)
	}
	if len(result.Failures) != 1 || !strings.HasPrefix(result.Failures[0], "never: no reply") {
		t.Errorf("Lookup must stop the other services after ExtraWait: %q", result.Failures)
	}
}

func TestLookupCanceled(t *testing.T) {
	server := newDelayServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := Options{Services: newDelayServices(server, "fast", "never"), MinReplies: 1, ExtraWait: time.Second}
	if _, err := Lookup(ctx, options); err == nil || !strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "no reply in") {
		t.Errorf("a stop from the caller must not look like the end of the extra wait: %v", err)
	}
}

func TestLookupOnReply(t *testing.T) {
	server := newDelayServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var replies []string
	options := Options{
		Services:   newDelayServices(server, "broken", "fast", "late"),
		MinReplies: 1, ExtraWait: 10 * time.Millisecond,
		OnReply: func(location Location) bool {
			replies = append(replies, location.Source+": "+location.Country)
			return location.Source == "fast"
		},
	}
	result, err := Lookup(ctx, options)
	if err != nil || len(result.Locations) != 2 {
		t.Errorf("if OnReply returns true, Lookup must wait for all services: %+v, %v", result, err)
	}
	if !slices.Equal(replies, []string{"fast: DE", "late: DE"}) {
		t.Errorf("OnReply must run for each correct reply only: %q", replies)
	}
	replies = nil
	options.OnReply = func(location Location) bool {
		replies = append(replies, location.Source)
		return false
	}
	if result, err := Lookup(ctx, options); err != nil || len(result.Locations) != 1 || !slices.Equal(replies, []string{"fast"}) {
		t.Errorf("if OnReply returns false, the extra wait must stop the late service: %+v, %v, %q", result, err, replies)
	}
}
