package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var formatVerbPattern = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func TestMessagesComplete(t *testing.T) {
	for key := messageKey(0); key < messageCount; key++ {
		english, russian := messages[key][languageEnglish], messages[key][languageRussian]
		if english == "" || russian == "" {
			t.Errorf("message %d has no text in 1 language", key)
			continue
		}
		englishVerbs := formatVerbPattern.FindAllString(english, -1)
		russianVerbs := formatVerbPattern.FindAllString(russian, -1)
		if !slices.Equal(englishVerbs, russianVerbs) {
			t.Errorf("message %d: the format verbs are different: %v and %v", key, englishVerbs, russianVerbs)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		appLang, lcAll, lcMessages, lang string
		want                             language
	}{
		{"ru", "", "", "en_US.UTF-8", languageRussian},
		{"en", "", "", "ru_RU.UTF-8", languageEnglish},
		{"", "", "", "ru_RU.UTF-8", languageRussian},
		{"", "C", "", "ru_RU.UTF-8", languageRussian},
		{"", "C.UTF-8", "", "ru_RU.UTF-8", languageRussian},
		{"", "en_US.UTF-8", "", "ru_RU.UTF-8", languageEnglish},
		{"", "", "ru_RU.UTF-8", "en_US.UTF-8", languageRussian},
		{"", "", "", "de_DE.UTF-8", languageEnglish},
	}
	for _, testCase := range cases {
		t.Setenv("CLAWDIATOR_LANG", testCase.appLang)
		t.Setenv("LC_ALL", testCase.lcAll)
		t.Setenv("LC_MESSAGES", testCase.lcMessages)
		t.Setenv("LANG", testCase.lang)
		if got := detectLanguage(); got != testCase.want {
			t.Errorf("detectLanguage() with %+v = %d, want %d", testCase, got, testCase.want)
		}
	}
}

func TestParseAppleLanguages(t *testing.T) {
	cases := map[string]string{
		"(\n    \"ru-RU\",\n    \"en-US\"\n)\n": "ru-RU",
		"(\n    en,\n    ru\n)\n":               "en",
		"":                                      "",
	}
	for output, want := range cases {
		if got := parseAppleLanguages(output); got != want {
			t.Errorf("parseAppleLanguages(%q) = %q, want %q", output, got, want)
		}
	}
}

func useLanguage(t *testing.T, value language) {
	previous := currentLanguage
	currentLanguage = value
	t.Cleanup(func() { currentLanguage = previous })
}

// The help lists the version command in both languages. The release script checks the value of the version in the binary.
func TestHelpListsVersion(t *testing.T) {
	for _, language := range []language{languageEnglish, languageRussian} {
		useLanguage(t, language)
		if !strings.Contains(text(msgUsage), "\n  version ") {
			t.Errorf("the help in language %v must list the version command", language)
		}
	}
	if version != "dev" {
		t.Errorf("a build without -ldflags must have the version dev, not %q", version)
	}
}
