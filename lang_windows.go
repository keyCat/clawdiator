package main

import "syscall"

// getSystemLanguage returns "ru" if the Windows user interface language is Russian.
func getSystemLanguage() string {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")
	if proc.Find() != nil {
		return ""
	}
	languageID, _, _ := proc.Call()
	// The low 10 bits are the primary language. The value 0x19 is Russian.
	if languageID&0x3ff == 0x19 {
		return "ru"
	}
	return "en"
}
