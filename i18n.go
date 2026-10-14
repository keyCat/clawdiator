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
	msgFlagRemove
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
	msgOnlyMacOS
	msgNeedRoot
	msgFlagYes
	msgFlagGeoServices
	msgFlagGeoMinReplies
	msgFlagBlockedCountries
	msgCountryCodeInvalid
	msgUnknownGeoService
	msgMinRepliesInvalid
	msgCannotReadConfig
	msgConsentText
	msgConsentDeclined
	msgCannotCopyBinary
	msgCannotRegister
	msgInstalled
	msgLinkCreated
	msgLinkNotCreated
	msgLinkIsFile
	msgBlockedCountriesActive
	msgHintStatus
	msgStepDone
	msgStepFailed
	msgStepStopService
	msgStepRemoveBlock
	msgStepRemoveLink
	msgStepRemoveSockets
	msgStepRemoveFolder
	msgUninstallIncomplete
	msgUninstalled
	msgServiceWritesAgain
	msgBlockRemoved
	msgNoBlock
	msgCannotReadState
	msgStatusNotInstalled
	msgStatusInstalled
	msgStatusNeverRan
	msgStatusWorks
	msgStatusStopped
	msgStatusPausedForever
	msgStatusPausedUntil
	msgStatusPreBlocked
	msgStatusCountry
	msgStatusOpen
	msgStatusBlocked
	msgStatusHosts
	msgStatusLog
	msgServiceNotReachable
	msgCheckStarted
	msgCheckTooSoon
	msgUnexpectedAnswer
	msgFlagPauseFor
	msgFlagPauseForever
	msgPauseNeedsTime
	msgPauseTimeInvalid
	msgPausedForever
	msgPausedUntil
	msgResumed
	msgNoPauseActive
	messageCount
)

var messages = [messageCount][2]string{
	msgUsage: {
		`clawdiator blocks the access to Anthropic and OpenAI when this computer is in an unsupported country.
The work on the tool is not complete. Now the tool works only on macOS, without the menu.

Usage:
  clawdiator <command> [options]

Commands:
  install      Saves the settings and the consent. Registers and starts the service. Needs sudo.
  uninstall    Removes the service, the block and the files. Needs sudo.
  status       Shows the state of the service, the country and the block.
  check        Asks the service for a full check of the country now.
  pause        Stops the block for a time: --for 30m, 4h or 3d, or --forever. Needs sudo.
  resume       Stops the pause. Needs sudo.
  hosts        --dry-run shows the change of the hosts file. --remove removes the block without the service.
  help         Shows this help.
`,
		`clawdiator блокирует доступ к Anthropic и OpenAI, когда компьютер находится в неподдерживаемой стране.
Работа над программой не завершена. Сейчас программа работает только на macOS, без меню.

Использование:
  clawdiator <команда> [параметры]

Команды:
  install      Сохраняет настройки и согласие. Регистрирует и запускает службу. Нужен sudo.
  uninstall    Удаляет службу, блокировку и файлы. Нужен sudo.
  status       Показывает состояние службы, страну и блокировку.
  check        Просит службу проверить страну сейчас.
  pause        Снимает блокировку на время: --for 30m, 4h или 3d, или --forever. Нужен sudo.
  resume       Завершает паузу. Нужен sudo.
  hosts        --dry-run показывает изменение файла hosts. --remove удаляет блокировку без службы.
  help         Показывает эту справку.
`,
	},
	msgUnknownCommand: {"unknown command %q", "неизвестная команда %q"},
	msgErrorPrefix:    {"error:", "ошибка:"},
	msgFlagsHeader:    {"Options of the %s command:", "Параметры команды %s:"},
	msgFlagDryRun:     {"show the change, but change nothing", "показать изменение, но ничего не менять"},
	msgFlagRemove: {"remove the clawdiator block from the hosts file without the service. Needs sudo",
		"удалить блок clawdiator из файла hosts без службы. Нужен sudo"},
	msgFlagProviders: {"the providers to block, separated by commas: anthropic, openai. An empty value removes the block",
		"провайдеры для блокировки через запятую: anthropic, openai. Пустое значение снимает блокировку"},
	msgOnlyDryRun: {"add --dry-run to show the change, or --remove to remove the block",
		"добавьте --dry-run, чтобы показать изменение, или --remove, чтобы удалить блокировку"},
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
	msgOnlyMacOS: {"this version works only on macOS. Linux and Windows come in a later version",
		"эта версия работает только на macOS. Linux и Windows появятся в следующей версии"},
	msgNeedRoot:          {`the %s command needs administrator rights. Run it with sudo`, `команде %s нужны права администратора. Запустите её через sudo`},
	msgFlagYes:           {"agree to the consent text without a question", "согласиться с текстом согласия без вопроса"},
	msgFlagGeoServices:   {"the location services of the full check, separated by commas", "сервисы местоположения для полной проверки через запятую"},
	msgFlagGeoMinReplies: {"the minimum number of correct replies of the location services", "минимальное число верных ответов сервисов местоположения"},
	msgFlagBlockedCountries: {"more country codes to block for both providers, separated by commas, for example AF. An empty value removes them",
		"дополнительные коды стран для блокировки обоих провайдеров через запятую, например AF. Пустое значение удаляет их"},
	msgCountryCodeInvalid: {"blocked_countries: %q is not a 2-letter country code, for example AF",
		"blocked_countries: %q не является двухбуквенным кодом страны, например AF"},
	msgUnknownGeoService: {"the location service %q is not known. Use %s", "сервис местоположения %q неизвестен. Используйте %s"},
	msgMinRepliesInvalid: {"geo_min_replies must be from 1 to the number of selected services (%d)",
		"geo_min_replies должно быть от 1 до числа выбранных сервисов (%d)"},
	msgCannotReadConfig: {"cannot read %s: %v. Correct or delete the file", "не удалось прочитать %s: %v. Исправьте или удалите файл"},
	msgConsentText: {
		`clawdiator blocks Anthropic and OpenAI on this computer when the computer is in an unsupported country.
Read what the service does:

  1. It changes the hosts file. In an unsupported country, the sites of Anthropic and OpenAI do not open
     in any program.
  2. Every 30 seconds, it sends a request to Cloudflare. At each full check, it also sends a request
     to the location services. Each service sees your public IP address.
  3. After each wake from sleep and each change of the network, it blocks the access until the country
     check passes.
  4. An administrator can pause the service, or remove it with "sudo clawdiator uninstall".

Type "yes" to agree: `,
		`clawdiator блокирует Anthropic и OpenAI на этом компьютере, когда компьютер находится в неподдерживаемой стране.
Прочитайте, что делает служба:

  1. Она изменяет файл hosts. В неподдерживаемой стране сайты Anthropic и OpenAI не открываются
     ни в одной программе.
  2. Каждые 30 секунд она отправляет запрос в Cloudflare. При каждой полной проверке она также отправляет
     запрос сервисам местоположения. Каждый сервис видит ваш публичный IP-адрес.
  3. После каждого выхода из сна и каждой смены сети она блокирует доступ, пока проверка страны
     не пройдёт.
  4. Администратор может поставить службу на паузу или удалить её командой "sudo clawdiator uninstall".

Введите "да" или "yes", чтобы согласиться: `,
	},
	msgConsentDeclined:  {"you did not agree, so the tool changed nothing", "вы не согласились, поэтому программа ничего не изменила"},
	msgCannotCopyBinary: {"cannot copy the binary to the app folder: %w", "не удалось скопировать программу в папку приложения: %w"},
	msgCannotRegister:   {"cannot register the service: %w", "не удалось зарегистрировать службу: %w"},
	msgInstalled:        {"The service is installed and runs. App folder: %s", "Служба установлена и работает. Папка приложения: %s"},
	msgLinkCreated:      {"The command is available as %s.", "Команда доступна как %s."},
	msgLinkNotCreated:   {"The tool did not create the link for the command: %v", "Программа не создала ссылку для команды: %v"},
	msgLinkIsFile:       {"%s exists and is not a link, so the tool did not replace it", "%s существует и не является ссылкой, поэтому программа не заменила его"},
	msgBlockedCountriesActive: {"CAUTION: blocked_countries has %s. The service blocks the providers in these countries.",
		"ВНИМАНИЕ: в blocked_countries есть %s. Служба блокирует провайдеров в этих странах."},
	msgHintStatus:        {`Run "clawdiator status" to see the state.`, `Выполните "clawdiator status", чтобы увидеть состояние.`},
	msgStepDone:          {"OK      %s", "ГОТОВО  %s"},
	msgStepFailed:        {"FAILED  %s: %v", "ОШИБКА  %s: %v"},
	msgStepStopService:   {"stop and remove the service", "остановить и удалить службу"},
	msgStepRemoveBlock:   {"remove the block from the hosts file", "удалить блокировку из файла hosts"},
	msgStepRemoveLink:    {"remove the link of the command", "удалить ссылку команды"},
	msgStepRemoveSockets: {"remove the sockets", "удалить сокеты"},
	msgStepRemoveFolder:  {"remove the app folder", "удалить папку приложения"},
	msgUninstallIncomplete: {"some steps failed. Correct the problem and run the command again",
		"некоторые шаги не выполнены. Устраните проблему и выполните команду снова"},
	msgUninstalled: {"clawdiator is removed.", "clawdiator удалён."},
	msgServiceWritesAgain: {`CAUTION: the service works. It writes the block again. To stop the service, run "sudo clawdiator uninstall".`,
		`ВНИМАНИЕ: служба работает. Она снова запишет блокировку. Чтобы остановить службу, выполните "sudo clawdiator uninstall".`},
	msgBlockRemoved:       {"The block is removed from %s. The DNS cache is flushed.", "Блокировка удалена из %s. Кэш DNS очищен."},
	msgNoBlock:            {"%s has no clawdiator block.", "В %s нет блока clawdiator."},
	msgCannotReadState:    {"cannot read %s: %v", "не удалось прочитать %s: %v"},
	msgStatusNotInstalled: {"Service: not installed.", "Служба: не установлена."},
	msgStatusInstalled:    {"Service: installed (%s).", "Служба: установлена (%s)."},
	msgStatusNeverRan:     {"The service did not run yet.", "Служба ещё не запускалась."},
	msgStatusWorks:        {"The service works. The last pass was %v ago.", "Служба работает. Последний проход был %v назад."},
	msgStatusStopped: {"CAUTION: the service does not work. The last pass was at %s. The block of that time stays.",
		"ВНИМАНИЕ: служба не работает. Последний проход был в %s. Блокировка того времени остаётся."},
	msgStatusPausedForever: {"Pause: on, with no end (user %s). The service blocks nothing.", "Пауза: включена, без конца (пользователь %s). Служба ничего не блокирует."},
	msgStatusPausedUntil:   {"Pause: on until %s (user %s). The service blocks nothing.", "Пауза: включена до %s (пользователь %s). Служба ничего не блокирует."},
	msgStatusPreBlocked: {"Pre-block: since %s. The network changed, and the full check did not pass yet.",
		"Предварительная блокировка: с %s. Сеть изменилась, а полная проверка ещё не прошла."},
	msgStatusCountry: {"Country: %s (last full check at %s)", "Страна: %s (последняя полная проверка в %s)"},
	msgStatusOpen:    {"open", "открыт"},
	msgStatusBlocked: {"BLOCKED", "ЗАБЛОКИРОВАН"},
	msgStatusHosts:   {"Hosts file %s: %d hosts are blocked.", "Файл hosts %s: заблокировано хостов: %d."},
	msgStatusLog:     {"Last lines of %s:", "Последние строки %s:"},
	msgServiceNotReachable: {"cannot reach the service. Make sure that it is installed and runs: %v",
		"не удалось связаться со службой. Убедитесь, что она установлена и работает: %v"},
	msgCheckStarted: {`The service started a full check. Run "clawdiator status" after some seconds.`,
		`Служба начала полную проверку. Выполните "clawdiator status" через несколько секунд.`},
	msgCheckTooSoon:     {"the service accepts 1 check in %d seconds. Try again later", "служба принимает 1 проверку за %d секунд. Попробуйте позже"},
	msgUnexpectedAnswer: {"the service gave the answer %q", "служба дала ответ %q"},
	msgFlagPauseFor:     {"the time of the pause, for example 30m, 4h or 3d", "длительность паузы, например 30m, 4h или 3d"},
	msgFlagPauseForever: {"a pause with no end", "пауза без конца"},
	msgPauseNeedsTime:   {"use --for with a time, or --forever", "используйте --for со временем или --forever"},
	msgPauseTimeInvalid: {"%q is not a correct time. Use a time from 1 minute to 365 days, for example 30m, 4h or 3d",
		"%q не является правильным временем. Используйте время от 1 минуты до 365 дней, например 30m, 4h или 3d"},
	msgPausedForever: {`The block is paused with no end. To stop the pause, run "sudo clawdiator resume".`,
		`Блокировка на паузе без конца. Чтобы завершить паузу, выполните "sudo clawdiator resume".`},
	msgPausedUntil: {"The block is paused until %s.", "Блокировка на паузе до %s."},
	msgResumed: {"The pause stopped. The block applies until the full check passes.",
		"Пауза завершена. Блокировка действует, пока полная проверка не пройдёт."},
	msgNoPauseActive: {"no pause is on", "пауза не включена"},
}
