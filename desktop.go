package main

import "strings"

// quoteAppleScript returns the text as an AppleScript string with quotes.
func quoteAppleScript(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

// quoteShell returns the text as a shell word in single quotes.
func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
