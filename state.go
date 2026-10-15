package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"geocheck"
)

// eventKind is the type of an event in state.json. The agent shows a notice for some kinds.
type eventKind string

const (
	// eventBlock: the service now blocks the providers of the event.
	eventBlock eventKind = "block"
	// eventUnblock: the service no longer blocks the providers of the event.
	eventUnblock eventKind = "unblock"
	// eventPreBlock: the network changed. The service blocks all providers until a full check passes.
	eventPreBlock eventKind = "pre_block"
	// eventPreBlockEnd: a full check passed after a pre-block.
	eventPreBlockEnd eventKind = "pre_block_end"
	// eventPause: a pause started. The service blocks nothing.
	eventPause eventKind = "pause"
	// eventPauseEnd: the pause ended. The block applies again.
	eventPauseEnd eventKind = "pause_end"
	// eventPauseBlock and eventPauseUnblock tell about a change of a verdict during a pause. The service does not apply it.
	eventPauseBlock   eventKind = "pause_block"
	eventPauseUnblock eventKind = "pause_unblock"
	// eventError: the service could not do a task, for example a change of the hosts file.
	eventError eventKind = "error"
)

// maxEvents is the number of the last events that state.json keeps.
const maxEvents = 50

// event is 1 change that the agent can show as a notice. The texts in it are in English, for the log.
// The agent writes the notice in the language of the user from the other fields.
type event struct {
	// Seq is the sequence number. It increases by 1 for each event, also after a restart of the service.
	Seq       int64               `json:"seq"`
	Time      time.Time           `json:"time"`
	Kind      eventKind           `json:"kind"`
	Providers []geocheck.Provider `json:"providers,omitempty"`
	// Country is the country of the first verdict of the event, for example "CN".
	Country string `json:"country,omitempty"`
	// Verdict is the kind of the first verdict of the event, for example "not_supported".
	Verdict string `json:"verdict,omitempty"`
	// Reasons holds the verdict of each provider in English, with the reply of each service.
	Reasons map[geocheck.Provider]string `json:"reasons,omitempty"`
	// Until is the end of a pause. A zero value means a pause with no end.
	Until time.Time `json:"until"`
	// User is the user who started a pause.
	User  string `json:"user,omitempty"`
	Error string `json:"error,omitempty"`
	// Cause tells why a pre-block started. preBlockCauseSleep is a pre-block before a sleep. An empty value is a change of the network.
	Cause string `json:"cause,omitempty"`
}

// preBlockCauseSleep is the cause of a pre-block before a sleep of the computer.
const preBlockCauseSleep = "sleep"

// providerState is the last verdict for 1 provider.
type providerState struct {
	// IsBlocked tells if the verdict does not allow the provider.
	IsBlocked bool `json:"is_blocked"`
	// Since is the time of the last change of IsBlocked.
	Since time.Time `json:"since"`
	// Verdict is the kind of the last verdict, for example "not_supported".
	Verdict string `json:"verdict,omitempty"`
	// Country is the country of the last verdict.
	Country string `json:"country,omitempty"`
	// Reason is the last verdict in English, with the reply of each service.
	Reason string `json:"reason,omitempty"`
}

// checkFailure is a failed full check. The error names each location service and its failure.
type checkFailure struct {
	Time  time.Time `json:"time"`
	Error string    `json:"error,omitempty"`
}

// localDBState is the file of the local database. An empty File means that the service has no correct file.
type localDBState struct {
	File      string    `json:"file,omitempty"`
	BuildTime time.Time `json:"build_time,omitzero"`
}

// firewallState is the state of the firewall rules. Only root can read the rules of pf, so the status command reads this state.
type firewallState struct {
	// IsBlocked tells if the service turned the rules on, or tried to turn them on.
	IsBlocked bool `json:"is_blocked,omitempty"`
	// Token is the reference of clawdiator to the switch-on of pf. uninstall releases it.
	Token string `json:"token,omitempty"`
	// Error is the last error. It is empty after a successful change or check.
	Error string `json:"error,omitempty"`
}

// pauseState tells if a pause is on.
type pauseState struct {
	IsOn  bool      `json:"is_on"`
	Since time.Time `json:"since"`
	// Until is the end of the pause. A zero value means a pause with no end.
	Until time.Time `json:"until"`
	User  string    `json:"user,omitempty"`
}

// serviceState is the content of state.json. Only the service writes it.
// The menu, the status command and the agent read it.
type serviceState struct {
	// Heartbeat is the time of the last loop of the service.
	Heartbeat time.Time `json:"heartbeat"`
	// IsPreBlocked tells if a pre-block is on: the network changed, and no full check passed after the change.
	IsPreBlocked  bool                                 `json:"is_pre_blocked"`
	PreBlockSince time.Time                            `json:"pre_block_since"`
	Pause         pauseState                           `json:"pause"`
	Providers     map[geocheck.Provider]*providerState `json:"providers"`
	// Country is the text of the countries of the last full check, for example "DE, Hesse".
	Country       string    `json:"country,omitempty"`
	LastFullCheck time.Time `json:"last_full_check"`
	// LastResult holds the replies of the last full check. The probe uses its regions.
	LastResult geocheck.Result `json:"last_result"`
	// PublicIP is the last known public IP address. A change to another network starts a pre-block.
	PublicIP string `json:"public_ip,omitempty"`
	// LastFailure is the last failed full check. A passed full check clears it.
	LastFailure checkFailure `json:"last_failure"`
	// LocalDB is the file of the local database that the service uses. The status command shows it.
	LocalDB localDBState `json:"local_db,omitzero"`
	// Firewall is the state of the firewall rules for the ranges of Anthropic. Only macOS has these rules now.
	Firewall firewallState `json:"firewall,omitzero"`
	// LastEventSeq is the sequence number of the last event.
	LastEventSeq int64   `json:"last_event_seq"`
	Events       []event `json:"events"`
}

// provider returns the state of the provider. It adds an empty state if the provider has no state.
func (s *serviceState) provider(provider geocheck.Provider) *providerState {
	if s.Providers == nil {
		s.Providers = map[geocheck.Provider]*providerState{}
	}
	if s.Providers[provider] == nil {
		s.Providers[provider] = &providerState{}
	}
	return s.Providers[provider]
}

// blockedProviders returns the providers that the service must block now.
// A pause blocks nothing. A pre-block blocks all providers.
func (s *serviceState) blockedProviders() []geocheck.Provider {
	if s.Pause.IsOn {
		return nil
	}
	var providers []geocheck.Provider
	for _, provider := range geocheck.Providers() {
		if s.IsPreBlocked || s.provider(provider).IsBlocked {
			providers = append(providers, provider)
		}
	}
	return providers
}

// isThirdPartyBlocked tells if the service must block the third-party platforms.
// The platforms have models of both providers, so 1 blocked provider blocks them.
func (s *serviceState) isThirdPartyBlocked() bool {
	return len(s.blockedProviders()) > 0
}

// loadServiceState reads state.json. If the file does not exist, it returns an empty state.
func loadServiceState(path string) (*serviceState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &serviceState{}, nil
	}
	if err != nil {
		return nil, err
	}
	state := &serviceState{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	return state, nil
}

// saveServiceState writes state.json through a temporary file, so a stop of the process cannot leave half a file.
// All users can read the file, because the menu and the agent run as the user.
func saveServiceState(path string, state *serviceState) error {
	data, err := json.MarshalIndent(state, "", "  ")
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
