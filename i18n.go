package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type language int

const (
	languageEnglish language = iota
	languageRussian
)

// currentLanguage is the language of the UI and of the command output. The log is always in English.
var currentLanguage = languageEnglish

// detectLanguage reads CLAWDIATOR_LANG, then the locale variables, then the system language.
// The language is Russian if the value starts with "ru". Otherwise, it is English.
func detectLanguage() language {
	for _, name := range []string{"CLAWDIATOR_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		value := os.Getenv(name)
		// "C" and "POSIX" are neutral locales. They do not select a language.
		if value == "" || value == "C" || value == "POSIX" || strings.HasPrefix(value, "C.") {
			continue
		}
		return parseLanguage(value)
	}
	return parseLanguage(getSystemLanguage())
}

// parseAppleLanguages returns the first item of a list such as ("ru-RU", "en-US").
func parseAppleLanguages(output string) string {
	output = strings.Trim(strings.TrimSpace(output), "()")
	for _, item := range strings.Split(output, ",") {
		if item = strings.Trim(strings.TrimSpace(item), `"`); item != "" {
			return item
		}
	}
	return ""
}

func parseLanguage(value string) language {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "ru") {
		return languageRussian
	}
	return languageEnglish
}

// text returns the message in the current language. With arguments, it formats the message like fmt.Sprintf.
func text(key messageKey, args ...any) string {
	return textIn(currentLanguage, key, args...)
}

// textIn returns the message in the given language. The log uses it, because the log is always in English.
func textIn(lang language, key messageKey, args ...any) string {
	format := messages[key][lang]
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// errorf returns the message in the current language as an error. The message can wrap an error with %w.
func errorf(key messageKey, args ...any) error {
	format := messages[key][currentLanguage]
	if len(args) == 0 {
		return errors.New(format)
	}
	return fmt.Errorf(format, args...)
}

type messageKey int

const (
	msgUsage messageKey = iota
	msgUnknownCommand
	msgErrorPrefix
	msgFlagsHeader
	msgFlagDryRun
	msgFlagProviders
	msgOnlyDryRun
	msgHostsNoEnd
	msgHostsNoBegin
	msgDryRunHeader
	msgDryRunHostsFile
	msgDryRunNoChange
	msgDryRunNewBlock
	msgDryRunRemoveBlock
	msgDryRunFlush
	messageCount
)

var messages = [messageCount][2]string{
	msgUsage: {
		`clawdiator blocks the access to Anthropic and OpenAI when this computer is in an unsupported country.
The work on the tool is not complete. Now the tool has only these commands.

Usage:
  clawdiator help                 Shows this help.
  clawdiator hosts --dry-run      Shows the change of the hosts file for a block. The command changes nothing.
`,
		`clawdiator блокирует доступ к Anthropic и OpenAI, когда компьютер находится в неподдерживаемой стране.
Работа над программой не завершена. Сейчас у программы есть только эти команды.

Использование:
  clawdiator help                 Показывает эту справку.
  clawdiator hosts --dry-run      Показывает изменение файла hosts для блокировки. Команда ничего не меняет.
`,
	},
	msgUnknownCommand: {"unknown command %q", "неизвестная команда %q"},
	msgErrorPrefix:    {"error:", "ошибка:"},
	msgFlagsHeader:    {"Options of the %s command:", "Параметры команды %s:"},
	msgFlagDryRun:     {"show the change, but change nothing", "показать изменение, но ничего не менять"},
	msgFlagProviders: {"the providers to block, separated by commas: anthropic, openai. An empty value removes the block",
		"провайдеры для блокировки через запятую: anthropic, openai. Пустое значение снимает блокировку"},
	msgOnlyDryRun: {"this version can only show the change. Add --dry-run", "эта версия может только показать изменение. Добавьте --dry-run"},
	msgHostsNoEnd: {"the hosts file has a BEGIN line of clawdiator without an END line. Correct the file manually",
		"в файле hosts есть строка BEGIN от clawdiator без строки END. Исправьте файл вручную"},
	msgHostsNoBegin: {"the hosts file has an END line of clawdiator without a BEGIN line. Correct the file manually",
		"в файле hosts есть строка END от clawdiator без строки BEGIN. Исправьте файл вручную"},
	msgDryRunHeader:      {"Dry run. The tool changes nothing.", "Пробный запуск. Программа ничего не меняет."},
	msgDryRunHostsFile:   {"Hosts file: %s (list of hosts from %s)", "Файл hosts: %s (список хостов от %s)"},
	msgDryRunNoChange:    {"The hosts file already has this content. The tool would write nothing.", "В файле hosts уже такое содержимое. Программа ничего бы не записала."},
	msgDryRunNewBlock:    {"The tool would write this block at the end of the file:", "Программа записала бы этот блок в конец файла:"},
	msgDryRunRemoveBlock: {"The tool would remove the clawdiator block from the file.", "Программа удалила бы блок clawdiator из файла."},
	msgDryRunFlush:       {"Then the tool would flush the DNS cache with these commands:", "Затем программа очистила бы кэш DNS этими командами:"},
}
