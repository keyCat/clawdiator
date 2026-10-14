package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"geocheck"
)

// maxLogSize is the size of the log that starts a new file. The old lines move to clawdiator.log.1.
const maxLogSize = 1 << 20

// appendLog adds the lines to the log. All users can read the log.
func appendLog(path string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(strings.Join(lines, "\n") + "\n")
	return joinErrors(writeErr, file.Close())
}

// formatEventLog returns the log lines of the event, in English. A block event gives 1 line for each provider.
func formatEventLog(change event) []string {
	stamp := change.Time.Format(time.RFC3339)
	words := map[eventKind]string{eventBlock: "blocked", eventUnblock: "open", eventPauseBlock: "paused-blocked", eventPauseUnblock: "paused-open"}
	switch change.Kind {
	case eventBlock, eventUnblock, eventPauseBlock, eventPauseUnblock:
		var lines []string
		for _, provider := range change.Providers {
			lines = append(lines, fmt.Sprintf("%s %s %s %s", stamp, provider, words[change.Kind], cleanLogText(change.Reasons[provider])))
		}
		return lines
	case eventPreBlock:
		return []string{stamp + " all pre-block the network changed, so the block stays until a full check passes"}
	case eventPreBlockEnd:
		return []string{stamp + " all pre-block-end the full check passed"}
	case eventPause:
		end := "with no end"
		if !change.Until.IsZero() {
			end = "until " + change.Until.Format(time.RFC3339)
		}
		return []string{fmt.Sprintf("%s all paused %s by user %s", stamp, end, cleanLogText(change.User))}
	case eventPauseEnd:
		return []string{stamp + " all resumed"}
	case eventError:
		return []string{stamp + " service error " + cleanLogText(change.Error)}
	}
	return nil
}

// getProviderNames returns the names of the providers for a text, for example "Anthropic, OpenAI".
func getProviderNames(providers []geocheck.Provider) string {
	var names []string
	for _, provider := range providers {
		names = append(names, provider.DisplayName())
	}
	return strings.Join(names, ", ")
}

// cleanLogText puts a text on 1 line, so that a text cannot add a false line to the log.
func cleanLogText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// joinErrors returns the first error that is not nil.
func joinErrors(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
