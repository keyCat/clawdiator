package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"geocheck"
)

const (
	// maxLogSize is the size of the log that starts a new file. The old lines move to clawdiator.log.1.
	maxLogSize = 1 << 20
	// maxDebugLogSize is the size of the verbose log that starts a new file.
	// A probe every 30 seconds gives about 350 KB each day, so 1 file keeps about 2 weeks.
	maxDebugLogSize = 5 << 20
	// debugTimeFormat has milliseconds, so that the verbose log shows the duration of each step.
	debugTimeFormat = "2006-01-02T15:04:05.000Z07:00"
)

// appendLog adds the lines to the normal log. All users can read the log.
func appendLog(path string, lines []string) error {
	return appendLogLimit(path, lines, maxLogSize)
}

// appendLogLimit adds the lines to a log. At the size limit, it moves the log to the same name with ".1".
func appendLogLimit(path string, lines []string, maxSize int64) error {
	if len(lines) == 0 {
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= maxSize {
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

// formatEventLog returns the log lines of the event, in English, with the time format of the normal log.
// A block event gives 1 line for each provider.
func formatEventLog(change event) []string {
	return formatEventLogWith(change, time.RFC3339)
}

// formatEventLogWith returns the log lines of the event with the time format. The verbose log uses milliseconds.
func formatEventLogWith(change event, timeFormat string) []string {
	stamp := change.Time.Format(timeFormat)
	words := map[eventKind]string{eventBlock: "blocked", eventUnblock: "open", eventPauseBlock: "paused-blocked", eventPauseUnblock: "paused-open"}
	switch change.Kind {
	case eventBlock, eventUnblock, eventPauseBlock, eventPauseUnblock:
		var lines []string
		for _, provider := range change.Providers {
			lines = append(lines, fmt.Sprintf("%s %s %s %s", stamp, provider, words[change.Kind], cleanLogText(change.Reasons[provider])))
		}
		return lines
	case eventPreBlock:
		if change.Cause == preBlockCauseSleep {
			return []string{stamp + " all pre-block the computer goes to sleep, so the block stays until a full check after the wake passes"}
		}
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
