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
	msgStepRemoveUserFiles
	msgStepRemoveFirewall
	msgFirewallRemoved
	msgStatusFirewallOn
	msgStatusFirewallError
	msgDryRunFirewall
	msgUserFolderKept
	msgUserFolderNotRemoved
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
	msgStatusLastFailure
	msgLocalDBDownload
	msgLocalDBReady
	msgLocalDBNotDownloaded
	msgStatusLocalDB
	msgStatusLocalDBOld
	msgStatusLocalDBNone
	msgStatusOpen
	msgStatusBlocked
	msgStatusHosts
	msgStatusLog
	msgFlagVerbose
	msgFlagLines
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
	msgFlagAgentShow
	msgTrayNotBuilt
	msgTrayMissing
	msgAgentsStarted
	msgAgentsNotStarted
	msgStepStopAgents
	msgNoticeServiceDown
	msgNoticeServiceBack
	msgNoticeWelcome
	msgNoticeWelcomeNoCountry
	msgNoticePreBlock
	msgNoticeBlockedOne
	msgNoticeBlockedMany
	msgNoticeBlockedUnknownOne
	msgNoticeBlockedUnknownMany
	msgNoticeAvailableOne
	msgNoticeAvailableMany
	msgNoticeAvailableCountryOne
	msgNoticeAvailableCountryMany
	msgNoticePausedForever
	msgNoticePausedUntil
	msgNoticePauseEnd
	msgNoticePauseBlock
	msgNoticeServiceError
	msgAnd
	msgTrayCountry
	msgTrayCountryUnknown
	msgTrayLastCheck
	msgTrayTooltipOpen
	msgTrayTooltipPreBlocked
	msgTrayTooltipBlocked
	msgTrayTooltipPaused
	msgTrayCheckNow
	msgTrayPause
	msgTrayPause30m
	msgTrayPause1h
	msgTrayPause4h
	msgTrayPause1d
	msgTrayPause3d
	msgTrayPauseForever
	msgTrayResume
	msgTrayNotices
	msgTrayNoticesLong
	msgTrayNoticesAll
	msgTrayLog
	msgTrayShowLog
	msgTrayShowVerboseLog
	msgTrayHide
	msgTrayUpdate
	msgFlagUnattended
	msgConsentNeeded
	msgUpdateNewest
	msgUpdateDownload
	msgUpdateCheckFailed
	msgUpdateDownloadFailed
	msgNoticeUpdated
	msgNoticeUpdateAvailable
	msgStatusUpdate
	msgTrayCheckUpdate
	msgFlagUpdateCheck
	msgUpdateInstalling
	msgUpdateCheckError
	msgUpdateLocalBuild
	msgUpdateCheckTimeout
	msgTrayWebsite
	msgTrayAutoUpdate
	msgFlagAutoInstall
	msgAutoInstallValue
	msgAutoUpdateOn
	msgAutoUpdateOff
	msgStatusAutoUpdateOn
	msgStatusAutoUpdateOff
	msgUpdateRunning
	msgTerminalNotOpened
	messageCount
)

var messages = [messageCount][2]string{
	msgUsage: {
		`clawdiator blocks the access to Anthropic and OpenAI when this computer is in an unsupported country.
The work on the tool is not complete. Now the tool works only on macOS.

Usage:
  clawdiator <command> [options]

Commands:
  install      Saves the settings and the consent. Registers and starts the service. Needs sudo.
  uninstall    Removes the service, the block and the files. Needs sudo.
  update       Installs the newest version from GitHub. Needs sudo.
               --check only asks the service to check now. It needs no sudo.
               --auto-install on or off turns the automatic updates on or off.
  status       Shows the state of the service, the country and the block.
  check        Asks the service for a full check of the country now.
  pause        Stops the block for a time: --for 30m, 4h or 3d, or --forever. Needs sudo.
  resume       Stops the pause. Needs sudo.
  hosts        --dry-run shows the change of the hosts file. --remove removes the block without the service.
  version      Shows the version of the tool.
  help         Shows this help.

The local country database: ` + dbCredit + `, license CC BY 4.0.
`,
		`clawdiator блокирует доступ к Anthropic и OpenAI, когда компьютер находится в неподдерживаемой стране.
Работа над программой не завершена. Сейчас программа работает только на macOS.

Использование:
  clawdiator <команда> [параметры]

Команды:
  install      Сохраняет настройки и согласие. Регистрирует и запускает службу. Нужен sudo.
  uninstall    Удаляет службу, блокировку и файлы. Нужен sudo.
  update       Устанавливает новейшую версию с GitHub. Нужен sudo.
               --check только просит службу проверить сейчас. Не требует sudo.
               --auto-install on или off включает или выключает автоматические обновления.
  status       Показывает состояние службы, страну и блокировку.
  check        Просит службу проверить страну сейчас.
  pause        Снимает блокировку на время: --for 30m, 4h или 3d, или --forever. Нужен sudo.
  resume       Завершает паузу. Нужен sudo.
  hosts        --dry-run показывает изменение файла hosts. --remove удаляет блокировку без службы.
  version      Показывает версию программы.
  help         Показывает эту справку.

Локальная база стран: ` + dbCredit + `, лицензия CC BY 4.0.
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
	msgNeedRoot: {`the %s command needs administrator rights. Run it with sudo`, `команде %s нужны права администратора. Запустите её через sudo`},
	msgFlagYes:  {"agree to the consent text without a question", "согласиться с текстом согласия без вопроса"},
	msgFlagGeoServices: {"the location services of the full check, separated by commas. The default is %s",
		"сервисы местоположения для полной проверки через запятую. По умолчанию %s"},
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
     in any program. During a block, it also turns on the firewall of macOS and blocks the addresses
     of Anthropic.
  2. Every 30 seconds, it sends a request to Cloudflare. At each full check, it also sends a request
     to the location services. Each service sees your public IP address.
     Each month, it downloads the country database of DB-IP from db-ip.com.
  3. After each wake from sleep and each change of the network, it blocks the access until the country
     check passes.
  4. An administrator can pause the service, or remove it with "sudo clawdiator uninstall".
  5. Every 6 hours, it asks github.com for the newest version. GitHub sees your public IP address.
     It installs a new version only if you turn on the automatic updates in the menu, or if you run
     "sudo clawdiator update". An automatic update never installs a version with a new consent text.

Type "yes" to agree: `,
		`clawdiator блокирует Anthropic и OpenAI на этом компьютере, когда компьютер находится в неподдерживаемой стране.
Прочитайте, что делает служба:

  1. Она изменяет файл hosts. В неподдерживаемой стране сайты Anthropic и OpenAI не открываются
     ни в одной программе. Во время блокировки она также включает брандмауэр macOS и блокирует адреса
     Anthropic.
  2. Каждые 30 секунд она отправляет запрос в Cloudflare. При каждой полной проверке она также отправляет
     запрос сервисам местоположения. Каждый сервис видит ваш публичный IP-адрес.
     Каждый месяц она скачивает базу стран DB-IP с сайта db-ip.com.
  3. После каждого выхода из сна и каждой смены сети она блокирует доступ, пока проверка страны
     не пройдёт.
  4. Администратор может поставить службу на паузу или удалить её командой "sudo clawdiator uninstall".
  5. Каждые 6 часов она запрашивает новейшую версию на github.com. GitHub видит ваш публичный IP-адрес.
     Она устанавливает новую версию, только если вы включите автоматические обновления в меню
     или выполните "sudo clawdiator update". Автоматическое обновление никогда не устанавливает версию
     с новым текстом согласия.

Введите "да" или "yes", чтобы согласиться: `,
	},
	msgConsentDeclined:  {"you did not agree, so the tool changed nothing", "вы не согласились, поэтому программа ничего не изменила"},
	msgCannotCopyBinary: {"cannot copy the binary to the app folder: %w", "не удалось скопировать программу в папку приложения: %w"},
	msgCannotRegister:   {"cannot register the service: %w", "не удалось зарегистрировать службу: %w"},
	msgInstalled:        {"The service is installed and runs. App folder: %s", "Служба установлена и работает. Папка приложения: %s"},
	msgLinkCreated:      {"The command is available as %s.", "Команда доступна как %s."},
	msgLocalDBDownload: {"The tool downloads the local country database from db-ip.com.",
		"Программа скачивает локальную базу стран с сайта db-ip.com."},
	msgLocalDBReady: {"The local country database %s is in the app folder.", "Локальная база стран %s находится в папке приложения."},
	msgLocalDBNotDownloaded: {"CAUTION: the tool did not download the local country database: %v. The service tries again at its start, then every 6 hours.",
		"ВНИМАНИЕ: программа не скачала локальную базу стран: %v. Служба попробует снова при запуске, затем каждые 6 часов."},
	msgLinkNotCreated: {"The tool did not create the link for the command: %v", "Программа не создала ссылку для команды: %v"},
	msgLinkIsFile:     {"%s exists and is not a link, so the tool did not replace it", "%s существует и не является ссылкой, поэтому программа не заменила его"},
	msgBlockedCountriesActive: {"CAUTION: blocked_countries has %s. The service blocks the providers in these countries.",
		"ВНИМАНИЕ: в blocked_countries есть %s. Служба блокирует провайдеров в этих странах."},
	msgHintStatus:          {`Run "clawdiator status" to see the state.`, `Выполните "clawdiator status", чтобы увидеть состояние.`},
	msgStepDone:            {"OK      %s", "ГОТОВО  %s"},
	msgStepFailed:          {"FAILED  %s: %v", "ОШИБКА  %s: %v"},
	msgStepStopService:     {"stop and remove the service", "остановить и удалить службу"},
	msgStepRemoveBlock:     {"remove the block from the hosts file", "удалить блокировку из файла hosts"},
	msgStepRemoveLink:      {"remove the link of the command", "удалить ссылку команды"},
	msgStepRemoveSockets:   {"remove the sockets", "удалить сокеты"},
	msgStepRemoveFolder:    {"remove the app folder", "удалить папку приложения"},
	msgStepRemoveFirewall:  {"remove the firewall rules and release the switch-on of pf", "удалить правила брандмауэра и освободить включение pf"},
	msgFirewallRemoved:     {"The firewall rules of the pf anchor %s are removed.", "Правила брандмауэра в якоре pf %s удалены."},
	msgStatusFirewallOn:    {"Firewall: the address ranges of Anthropic are blocked.", "Брандмауэр: диапазоны адресов Anthropic заблокированы."},
	msgStatusFirewallError: {"CAUTION: the firewall has a problem: %s", "ВНИМАНИЕ: у брандмауэра проблема: %s"},
	msgDryRunFirewall: {"During a block of Anthropic, the service also loads this rule into the pf anchor %s (ranges from %s):",
		"Во время блокировки Anthropic служба также загружает это правило в якорь pf %s (диапазоны от %s):"},
	msgStepRemoveUserFiles: {"remove the files of the notices in the home folders of the users",
		"удалить файлы уведомлений в домашних папках пользователей"},
	msgUserFolderKept: {"        %s has other files, so the tool kept the folder. Remove it manually if you do not need the files.",
		"        в %s есть другие файлы, поэтому программа оставила папку. Удалите её вручную, если файлы не нужны."},
	msgUserFolderNotRemoved: {"        The tool did not remove %s: %v",
		"        Программа не удалила %s: %v"},
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
	msgStatusCountry:     {"Country: %s (last full check at %s)", "Страна: %s (последняя полная проверка в %s)"},
	msgStatusLastFailure: {"The last full check failed at %s: %s", "Последняя полная проверка не прошла в %s: %s"},
	msgStatusLocalDB: {"Local database: %s, built %s. " + dbCredit + ".",
		"Локальная база: %s, создана %s. " + dbCredit + "."},
	msgStatusLocalDBOld: {"Local database: %s, built %s. It is older than 60 days, so the checks do not use it.",
		"Локальная база: %s, создана %s. Она старше 60 дней, поэтому проверки её не используют."},
	msgStatusLocalDBNone: {"Local database: none. The checks use only the online services.",
		"Локальная база: нет. Проверки используют только онлайн-сервисы."},
	msgStatusOpen:    {"open", "открыт"},
	msgStatusBlocked: {"BLOCKED", "ЗАБЛОКИРОВАН"},
	msgStatusHosts:   {"Hosts file %s: %d hosts are blocked.", "Файл hosts %s: заблокировано хостов: %d."},
	msgStatusLog:     {"Last lines of %s:", "Последние строки %s:"},
	msgFlagVerbose: {"show the end of the verbose log instead of the normal log",
		"показать конец подробного журнала вместо обычного журнала"},
	msgFlagLines: {"the number of log lines. The default is 10, or 50 with --verbose",
		"число строк журнала. По умолчанию 10, или 50 с --verbose"},
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
	msgNoPauseActive:              {"no pause is on", "пауза не включена"},
	msgFlagAgentShow:              {"show the tray icon again after \"Hide the icon\", and stop", "снова показать значок в трее после «Скрыть значок» и завершить работу"},
	msgTrayNotBuilt:               {"this binary has no tray. Use clawdiator-tray", "в этой программе нет значка в трее. Используйте clawdiator-tray"},
	msgTrayMissing:                {"%s is not next to this binary, so the installation has no tray icon. The notices work.", "%s нет рядом с этой программой, поэтому установка без значка в трее. Уведомления работают."},
	msgAgentsStarted:              {"The notices and the tray icon start in your session. Other users get them at their next login.", "Уведомления и значок в трее запускаются в вашем сеансе. Другие пользователи получат их при следующем входе."},
	msgAgentsNotStarted:           {"CAUTION: the notices and the tray icon did not start: %v. They start at your next login.", "ВНИМАНИЕ: уведомления и значок в трее не запустились: %v. Они запустятся при следующем входе."},
	msgStepStopAgents:             {"stop and remove the notices and the tray icon", "остановить и удалить уведомления и значок в трее"},
	msgNoticeServiceDown:          {"The clawdiator service does not work. The block can be missing.", "Служба clawdiator не работает. Блокировки может не быть."},
	msgNoticeServiceBack:          {"The clawdiator service works again.", "Служба clawdiator снова работает."},
	msgNoticeWelcome:              {"clawdiator works. Country: %s.", "clawdiator работает. Страна: %s."},
	msgNoticeWelcomeNoCountry:     {"clawdiator works. The country check is not complete yet.", "clawdiator работает. Проверка страны ещё не завершена."},
	msgNoticePreBlock:             {"%s are blocked until the country check ends.", "%s заблокированы, пока проверка страны не завершится."},
	msgNoticeBlockedOne:           {"%s is blocked. Country: %s.", "%s заблокирован. Страна: %s."},
	msgNoticeBlockedMany:          {"%s are blocked. Country: %s.", "%s заблокированы. Страна: %s."},
	msgNoticeBlockedUnknownOne:    {"%s is blocked. The country is unknown.", "%s заблокирован. Страна неизвестна."},
	msgNoticeBlockedUnknownMany:   {"%s are blocked. The country is unknown.", "%s заблокированы. Страна неизвестна."},
	msgNoticeAvailableOne:         {"%s is available again.", "%s снова доступен."},
	msgNoticeAvailableMany:        {"%s are available again.", "%s снова доступны."},
	msgNoticeAvailableCountryOne:  {"%s is available again. Country: %s.", "%s снова доступен. Страна: %s."},
	msgNoticeAvailableCountryMany: {"%s are available again. Country: %s.", "%s снова доступны. Страна: %s."},
	msgNoticePausedForever:        {"The block is paused with no end.", "Блокировка на паузе без конца."},
	msgNoticePausedUntil:          {"The block is paused until %s.", "Блокировка на паузе до %s."},
	msgNoticePauseEnd:             {"The pause ended. The country check applies again.", "Пауза закончилась. Проверка страны снова действует."},
	msgNoticePauseBlock:           {"%s: not available in %s. The pause is on.", "%s: недоступно в %s. Пауза включена."},
	msgNoticeServiceError:         {"The clawdiator service has a problem: %s", "У службы clawdiator проблема: %s"},
	msgAnd:                        {" and ", " и "},
	msgTrayCountry:                {"Country: %s", "Страна: %s"},
	msgTrayCountryUnknown:         {"Country: unknown", "Страна: неизвестна"},
	msgTrayLastCheck:              {"Last check: %s", "Последняя проверка: %s"},
	msgTrayTooltipOpen:            {"clawdiator: Anthropic and OpenAI are open", "clawdiator: Anthropic и OpenAI открыты"},
	msgTrayTooltipPreBlocked:      {"clawdiator: blocked until the country check ends", "clawdiator: заблокировано до конца проверки страны"},
	msgTrayTooltipBlocked:         {"clawdiator: blocked", "clawdiator: заблокировано"},
	msgTrayTooltipPaused:          {"clawdiator: the block is paused", "clawdiator: блокировка на паузе"},
	msgTrayCheckNow:               {"Check now", "Проверить сейчас"},
	msgTrayPause:                  {"Pause", "Пауза"},
	msgTrayPause30m:               {"30 minutes", "30 минут"},
	msgTrayPause1h:                {"1 hour", "1 час"},
	msgTrayPause4h:                {"4 hours", "4 часа"},
	msgTrayPause1d:                {"1 day", "1 день"},
	msgTrayPause3d:                {"3 days", "3 дня"},
	msgTrayPauseForever:           {"Until I resume", "Пока я не продолжу"},
	msgTrayResume:                 {"Resume", "Продолжить"},
	msgTrayNotices:                {"Notices after a wake", "Уведомления после выхода из сна"},
	msgTrayNoticesLong:            {"Only long blocks", "Только долгие блокировки"},
	msgTrayNoticesAll:             {"All", "Все"},
	msgTrayLog:                    {"Log", "Журнал"},
	msgTrayShowLog:                {"Show the log", "Показать журнал"},
	msgTrayShowVerboseLog:         {"Show the verbose log", "Показать подробный журнал"},
	msgTrayHide:                   {"Hide the icon", "Скрыть значок"},
	msgTrayUpdate:                 {"Install %s…", "Установить %s…"},
	msgFlagUnattended: {"ask no questions. The automatic update uses it. If the consent text changed, the command stops with an error",
		"не задавать вопросов. Это нужно для автоматического обновления. Если текст согласия изменился, команда завершается с ошибкой"},
	msgConsentNeeded: {"the consent text changed, so the automatic update cannot install this version. Run \"sudo clawdiator update\"",
		"текст согласия изменился, поэтому автоматическое обновление не может установить эту версию. Выполните \"sudo clawdiator update\""},
	msgUpdateNewest:         {"clawdiator %s is the newest version.", "clawdiator %s — новейшая версия."},
	msgUpdateDownload:       {"The tool downloads clawdiator %s from GitHub.", "Программа скачивает clawdiator %s с GitHub."},
	msgUpdateCheckFailed:    {"the tool did not find the newest version: %v", "программа не нашла новейшую версию: %v"},
	msgUpdateDownloadFailed: {"the tool did not download the new version: %v", "программа не скачала новую версию: %v"},
	msgNoticeUpdated:        {"clawdiator now has the version %s.", "Теперь у clawdiator версия %s."},
	msgNoticeUpdateAvailable: {"clawdiator %[1]s is available. To install it, select \"Install %[1]s…\" in the menu of the icon, or run \"sudo clawdiator update\" in Terminal. More: %[2]s",
		"Доступен clawdiator %[1]s. Чтобы установить его, выберите \"Установить %[1]s…\" в меню значка или выполните \"sudo clawdiator update\" в Терминале. Подробнее: %[2]s"},
	msgStatusUpdate: {"New version: %s. To install it, run \"sudo clawdiator update\". More: %s",
		"Новая версия: %s. Чтобы установить её, выполните \"sudo clawdiator update\". Подробнее: %s"},
	msgTrayCheckUpdate: {"Check for updates", "Проверить обновления"},
	msgFlagUpdateCheck: {"only ask the service to check for a new version now. It needs no sudo",
		"только попросить службу проверить новую версию сейчас. Не требует sudo"},
	msgUpdateInstalling: {"New version: %s. The service installs it now. A notice comes after the update.",
		"Новая версия: %s. Служба устанавливает её сейчас. После обновления придёт уведомление."},
	msgUpdateCheckError: {"the check for a new version failed: %s", "проверка новой версии не прошла: %s"},
	msgUpdateLocalBuild: {"the service is a local build, so it does not update itself", "служба — локальная сборка, поэтому она не обновляется сама"},
	msgUpdateCheckTimeout: {"the service did not finish the check in %d seconds. Run \"clawdiator status --verbose\" to see the log",
		"служба не завершила проверку за %d секунд. Выполните \"clawdiator status --verbose\", чтобы увидеть журнал"},
	msgTrayWebsite:    {"Open the website…", "Открыть сайт…"},
	msgTrayAutoUpdate: {"Install updates automatically", "Устанавливать обновления автоматически"},
	msgFlagAutoInstall: {"on or off: turn the automatic install of new versions on or off. Needs sudo",
		"on или off: включить или выключить автоматическую установку новых версий. Нужен sudo"},
	msgAutoInstallValue: {"--auto-install takes on or off", "--auto-install принимает on или off"},
	msgAutoUpdateOn: {"Automatic updates are on. The service installs each new version without a question, if its consent text is the same.",
		"Автоматические обновления включены. Служба устанавливает каждую новую версию без вопроса, если текст согласия не изменился."},
	msgAutoUpdateOff: {"Automatic updates are off. The service tells about a new version, but it does not install it.",
		"Автоматические обновления выключены. Служба сообщает о новой версии, но не устанавливает её."},
	msgStatusAutoUpdateOn: {"Automatic updates: on.", "Автоматические обновления: включены."},
	msgStatusAutoUpdateOff: {"Automatic updates: off. To turn them on, run \"sudo clawdiator update --auto-install on\".",
		"Автоматические обновления: выключены. Чтобы включить их, выполните \"sudo clawdiator update --auto-install on\"."},
	msgUpdateRunning: {"The service installs a new version now. A notice comes after the update.",
		"Служба устанавливает новую версию сейчас. После обновления придёт уведомление."},
	msgTerminalNotOpened: {"The tray did not open Terminal: %v. Run \"sudo clawdiator update\" in Terminal.",
		"Значок не открыл Терминал: %v. Выполните \"sudo clawdiator update\" в Терминале."},
}
