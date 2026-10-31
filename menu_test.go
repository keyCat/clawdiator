package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"clawdiator/internal/geocheck"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	keyDown  = tea.KeyMsg{Type: tea.KeyDown}
	keyRight = tea.KeyMsg{Type: tea.KeyRight}
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	keySpace = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
)

func typeText(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func press(model tea.Model, keys ...tea.KeyMsg) {
	for _, keyMsg := range keys {
		model.Update(keyMsg)
	}
}

// focusRow presses the down key until the focused row has the kind.
func focusRow(t *testing.T, m *formModel, kind rowKind) {
	t.Helper()
	for range m.page.rows {
		if m.page.rows[m.page.focus].kind == kind {
			return
		}
		press(m, keyDown)
	}
	t.Fatalf("no shown row of the kind %d", kind)
}

// TestConfigScreenSaves checks the way from the form to the options of install: the consent page only for a new consent,
// then the summary. The default services and the default minimum stay empty, so a new version can change them.
func TestConfigScreenSaves(t *testing.T) {
	for _, testCase := range []struct {
		consent   int
		wantSteps int
	}{
		{0, 2},
		{consentVersion, 1},
	} {
		cfg := &Config{Consent: consent{Version: testCase.consent}}
		form, values := newConfigScreen(cfg)
		press(form, typeText("af, mm"))
		focusRow(t, form, rowButtons)
		press(form, keyEnter)
		if len(form.steps) != testCase.wantSteps || form.isSaved {
			t.Fatalf("consent %d: the steps after Save = %d, want %d: %q", testCase.consent, len(form.steps), testCase.wantSteps, form.errorText)
		}
		// Esc goes back to the form, and a second Save starts at the first step again.
		press(form, keyEsc)
		if len(form.steps) != 0 {
			t.Fatal("Esc on a step must go back to the form")
		}
		press(form, keyEnter)
		for range testCase.wantSteps {
			press(form, keyEnter)
		}
		if !form.isSaved {
			t.Fatalf("consent %d: the last step must save", testCase.consent)
		}
		isConsentShown := testCase.wantSteps == 2
		want := []string{"--blocked-countries=AF,MM", "--geo-services=", "--geo-min-replies=0", "--auto-update=off"}
		if isConsentShown {
			want = append(want, "--yes")
		}
		if got := values.installArgs(isConsentShown); !slices.Equal(got, want) {
			t.Errorf("consent %d: the install options = %q, want %q", testCase.consent, got, want)
		}
	}
}

// TestConfigScreenChecks checks that Save stops at a wrong value and gives the focus to its row.
func TestConfigScreenChecks(t *testing.T) {
	form, values := newConfigScreen(&Config{})
	press(form, typeText("Germany"))
	focusRow(t, form, rowButtons)
	press(form, keyEnter)
	if len(form.steps) != 0 || form.page.focus != 0 || !strings.Contains(form.errorText, "GERMANY") {
		t.Errorf("a wrong country code must stop Save on its row: focus %d, %q", form.page.focus, form.errorText)
	}

	// No selected service: the page of the services shows the error, and Save on the main page goes to the link.
	form, values = newConfigScreen(&Config{})
	focusRow(t, form, rowLink)
	press(form, keyEnter)
	if form.page == form.mainPage {
		t.Fatal("Enter on the link must open the page of the services")
	}
	for index := range values.GeoServices {
		values.GeoServices[index] = false
	}
	press(form, keyEsc)
	focusRow(t, form, rowButtons)
	press(form, keyEnter)
	if len(form.steps) != 0 || form.mainPage.rows[form.page.focus].kind != rowLink || form.errorText == "" {
		t.Errorf("Save without a service must go to the link: %d, %q", form.page.focus, form.errorText)
	}

	// The minimum cannot go above the number of selected services.
	form, values = newConfigScreen(&Config{})
	focusRow(t, form, rowLink)
	press(form, keyEnter)
	focusRow(t, form, rowStepper)
	press(form, keyRight, keyRight, keyRight, keyRight, keyRight)
	if values.GeoMinReplies != len(values.getSelectedServiceNames()) {
		t.Errorf("the minimum = %d, want the number of services %d", values.GeoMinReplies, len(values.getSelectedServiceNames()))
	}
}

// TestConfigLocationLine checks the line above the separator: the country and the result for the values of the form.
func TestConfigLocationLine(t *testing.T) {
	original := lookupLocation
	t.Cleanup(func() { lookupLocation = original })
	lookupLocation = func(context.Context, geocheck.Options) (geocheck.Result, error) {
		return geocheck.Result{Locations: []geocheck.Location{{Country: "DE", Source: probeServiceName}}}, nil
	}
	form, _ := newConfigScreen(&Config{})
	if line := form.mainPage.preview(); line != text(msgCountryLookup) {
		t.Errorf("before the result, the line = %q", line)
	}
	form.Update(form.initCmd()())
	if line, want := form.mainPage.preview(), text(msgCountryNow, "DE")+" · "+text(msgCountryOpen, "Anthropic, OpenAI"); line != want {
		t.Errorf("the line = %q, want %q", line, want)
	}
	press(form, typeText("de"))
	if line, want := form.mainPage.preview(), text(msgCountryNow, "DE")+" · "+text(msgCountryBlocked, "Anthropic, OpenAI"); line != want {
		t.Errorf("a blocked country in the form must change the line at once: %q, want %q", line, want)
	}
}

func TestNoticesScreen(t *testing.T) {
	form, values := newNoticesScreen(agentSettings{}.withDefaults())
	press(form, keySpace)
	if !values.IsAll || form.mainPage.rows[1].isShown() {
		t.Fatalf("Space must select all notices and hide the seconds: %+v", values)
	}
	press(form, keySpace, keyDown, tea.KeyMsg{Type: tea.KeyBackspace}, tea.KeyMsg{Type: tea.KeyBackspace}, typeText("3"))
	focusRow(t, form, rowButtons)
	press(form, keyEnter)
	if form.isSaved || form.errorText == "" {
		t.Fatalf("3 seconds must stop Save: %+v", values)
	}
	focusRow(t, form, rowText)
	press(form, typeText("0"))
	focusRow(t, form, rowButtons)
	press(form, keyEnter)
	settings := agentSettings{Version: "v0.1.1"}
	values.applyTo(&settings)
	if !form.isSaved || settings.PreBlockNotices != preBlockNoticesLong || settings.PreBlockNoticeSeconds != 30 || settings.Version != "v0.1.1" {
		t.Errorf("Save must write only the fields of the screen: saved %v, %+v", form.isSaved, settings)
	}
}

func TestMainMenu(t *testing.T) {
	now := testStart
	menu, actions := newMainMenu(nil, now)
	if !slices.Equal(menu.description, []string{text(msgNoticeServiceDown)}) || actions[2] != actionPause {
		t.Errorf("without a state: %q, %v", menu.description, actions)
	}
	state := &serviceState{Heartbeat: now, Country: "RU"}
	state.provider(geocheck.Anthropic).IsBlocked = true
	menu, _ = newMainMenu(state, now)
	wantLine := text(msgCountryNow, "RU") + " · " + text(msgCountryOpen, "OpenAI") + " · " + text(msgCountryBlocked, "Anthropic")
	if !slices.Equal(menu.description, []string{wantLine}) {
		t.Errorf("the description = %q, want %q", menu.description, wantLine)
	}
	// During a pause, the service blocks nothing, the same as the tray shows.
	state.Pause = pauseState{IsOn: true}
	menu, actions = newMainMenu(state, now)
	if menu.description[1] != text(msgMenuPausedForever) || strings.Contains(menu.description[0], text(msgCountryBlocked, "Anthropic")) {
		t.Errorf("the description during a pause = %q", menu.description)
	}
	if menu.labels[2] != text(msgMenuResume) || actions[2] != actionResume {
		t.Errorf("a pause must change the item to Resume: %q, %v", menu.labels[2], actions[2])
	}
	press(menu, keyDown, keyEnter)
	if actions[menu.selected] != actionNotices {
		t.Errorf("the selected action = %v", actions[menu.selected])
	}
	if menu, _ := newMainMenu(&serviceState{Heartbeat: now.Add(-time.Hour)}, now); menu.description[0] != text(msgNoticeServiceDown) {
		t.Errorf("an old heartbeat must tell that the service does not work: %q", menu.description)
	}
}

// TestInstallMenuOptions checks the option --auto-update and the consent of the menu with --yes.
func TestInstallMenuOptions(t *testing.T) {
	in, _, stdout := newTestInstaller(t, "")
	if err := in.commandInstall([]string{"--yes", "--auto-update", "on"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(in.paths.Config); !cfg.AutoUpdate {
		t.Error("--auto-update on must turn on the automatic updates")
	}
	if !strings.Contains(stdout.String(), strings.TrimSpace(text(msgConsentText))) || strings.Contains(stdout.String(), text(msgConsentPrompt)) {
		t.Errorf("with --yes, install must show the consent text without the question:\n%s", stdout.String())
	}
	if err := in.commandInstall([]string{"--auto-update", "maybe"}); err == nil {
		t.Error("--auto-update must take only on or off")
	}
	if err := in.commandInstall([]string{"--auto-update", "off"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(in.paths.Config); cfg.AutoUpdate {
		t.Error("--auto-update off must turn off the automatic updates")
	}
}

// TestMenuViews renders each screen, also in a small terminal, where the rows scroll.
func TestMenuViews(t *testing.T) {
	menu, _ := newMainMenu(&serviceState{Heartbeat: testStart, Country: "DE"}, testStart)
	if view := menu.View(); !strings.Contains(view, text(msgMenuConfigure)) {
		t.Errorf("the menu view has no items:\n%s", view)
	}
	configForm, _ := newConfigScreen(&Config{})
	noticesForm, _ := newNoticesScreen(agentSettings{}.withDefaults())
	for _, form := range []*formModel{configForm, noticesForm} {
		for _, height := range []int{0, 8, 40} {
			form.Update(tea.WindowSizeMsg{Width: 60, Height: height})
			for range form.mainPage.rows {
				if view := form.View(); !strings.Contains(view, form.mainPage.title) {
					t.Fatalf("height %d: the view has no title:\n%s", height, view)
				}
				press(form, keyDown)
			}
		}
	}
	// The page of the services and the steps after Save.
	focusRow(t, configForm, rowLink)
	press(configForm, keyEnter)
	if view := configForm.View(); !strings.Contains(view, "cloudflare") {
		t.Errorf("the page of the services has no services:\n%s", view)
	}
	press(configForm, keyEsc)
	focusRow(t, configForm, rowButtons)
	press(configForm, keyEnter)
	if view := configForm.View(); !strings.Contains(view, text(msgAgree)) {
		t.Errorf("the consent step has no button:\n%s", view)
	}
	dialog := &confirmModel{title: "t", question: "q", description: "d", buttons: [2]string{"a", "b"}}
	if view := dialog.View(); !strings.Contains(view, "q") {
		t.Errorf("the dialog view = %q", view)
	}
}

// menuScript replaces the screens, the commands with root rights and the wait for Enter.
// Each step of the script gets 1 screen and presses its keys.
type menuScript struct {
	steps    []func(tea.Model)
	screens  []string
	commands []string
	waits    int
	// rootErr is the result of the next command with root rights.
	rootErr error
	// onInstall runs at the command install, for example to write config.json.
	onInstall func()
}

func newMenuScript(t *testing.T, steps ...func(tea.Model)) *menuScript {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	script := &menuScript{steps: steps}
	originalProgram, originalRoot, originalWait := runProgram, runRootCommand, waitForEnter
	t.Cleanup(func() { runProgram, runRootCommand, waitForEnter = originalProgram, originalRoot, originalWait })
	runProgram = func(model tea.Model) (tea.Model, error) {
		if len(script.steps) == 0 {
			t.Fatalf("an extra screen opened: %T, screens %v", model, script.screens)
		}
		script.screens = append(script.screens, strings.TrimPrefix(fmt.Sprintf("%T", model), "*main."))
		step := script.steps[0]
		script.steps = script.steps[1:]
		step(model)
		return model, nil
	}
	runRootCommand = func(args ...string) error {
		script.commands = append(script.commands, strings.Join(args, " "))
		if args[0] == "install" && script.onInstall != nil {
			script.onInstall()
		}
		err := script.rootErr
		script.rootErr = nil
		return err
	}
	waitForEnter = func() { script.waits++ }
	return script
}

// selectItem moves to the item with the label and pushes Enter.
func selectItem(t *testing.T, label string) func(tea.Model) {
	return func(model tea.Model) {
		menu := model.(*listModel)
		index := slices.Index(menu.labels, label)
		if index < 0 {
			t.Fatalf("no item %q in %q", label, menu.labels)
		}
		menu.cursor = index
		press(menu, keyEnter)
	}
}

func pushKeys(keys ...tea.KeyMsg) func(tea.Model) {
	return func(model tea.Model) { press(model, keys...) }
}

// TestMenuGoesBack checks that each cancel and each action goes back to the menu, and that Esc in the menu closes the tool.
func TestMenuGoesBack(t *testing.T) {
	paths := newAppPaths(t.TempDir())
	if err := saveConfig(paths.Config, &Config{Consent: consent{Version: consentVersion}}); err != nil {
		t.Fatal(err)
	}
	var messages []string
	keepMessage := func(model tea.Model) { messages = append(messages, model.(*listModel).message) }
	script := newMenuScript(t,
		selectItem(t, text(msgMenuConfigure)), pushKeys(keyEsc),
		selectItem(t, text(msgMenuNotices)), pushKeys(keyEsc),
		selectItem(t, text(msgMenuPause)), pushKeys(keyEsc),
		selectItem(t, text(msgMenuUninstall)), pushKeys(keyEnter),
		selectItem(t, text(msgMenuPause)), selectItem(t, text(msgTrayPause30m)),
		func(model tea.Model) { keepMessage(model); selectItem(t, text(msgMenuNotices))(model) },
		func(model tea.Model) {
			form := model.(*formModel)
			focusRow(t, form, rowButtons)
			press(form, keyEnter)
		},
		func(model tea.Model) { keepMessage(model); press(model, keyEsc) },
	)
	if err := runUI(paths); err != nil {
		t.Fatal(err)
	}
	wantScreens := []string{
		"listModel", "formModel", "listModel", "formModel", "listModel", "listModel", "listModel", "confirmModel",
		"listModel", "listModel", "listModel", "formModel", "listModel",
	}
	if !slices.Equal(script.screens, wantScreens) {
		t.Errorf("the screens = %v, want %v", script.screens, wantScreens)
	}
	if !slices.Equal(script.commands, []string{"pause --for 30m"}) || script.waits != 1 {
		t.Errorf("only the pause must run with root rights, with 1 wait for Enter: %q, waits %d", script.commands, script.waits)
	}
	if !slices.Equal(messages, []string{"", text(msgNoticesSaved)}) {
		t.Errorf("the menu must show the result of the notices: %q", messages)
	}
}

// TestMenuFailedAction checks that a failed command goes back to the menu with a line about the failure.
func TestMenuFailedAction(t *testing.T) {
	paths := newAppPaths(t.TempDir())
	if err := saveConfig(paths.Config, &Config{Consent: consent{Version: consentVersion}}); err != nil {
		t.Fatal(err)
	}
	if err := saveServiceState(paths.State, &serviceState{Heartbeat: time.Now(), Pause: pauseState{IsOn: true}}); err != nil {
		t.Fatal(err)
	}
	var message string
	script := newMenuScript(t,
		selectItem(t, text(msgMenuResume)),
		func(model tea.Model) { message = model.(*listModel).message; press(model, keyEsc) },
	)
	script.rootErr = errOffline
	if err := runUI(paths); err != nil {
		t.Fatal(err)
	}
	if message != text(msgActionFailed, errOffline) || script.waits != 1 {
		t.Errorf("a failed command must show its error in the menu: %q, waits %d", message, script.waits)
	}
}

// TestMenuUninstallAndFirstInstall checks the 2 ends: the removal closes the tool, and the first install opens the menu.
func TestMenuUninstallAndFirstInstall(t *testing.T) {
	paths := newAppPaths(t.TempDir())
	if err := saveConfig(paths.Config, &Config{Consent: consent{Version: consentVersion}}); err != nil {
		t.Fatal(err)
	}
	script := newMenuScript(t, selectItem(t, text(msgMenuUninstall)), pushKeys(tea.KeyMsg{Type: tea.KeyLeft}, keyEnter))
	if err := runUI(paths); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(script.commands, []string{"uninstall"}) || len(script.screens) != 2 {
		t.Errorf("the removal must close the tool: %q, %v", script.commands, script.screens)
	}

	// Without an installation, a cancel of the configuration closes the tool.
	paths = newAppPaths(t.TempDir())
	script = newMenuScript(t, pushKeys(keyEsc))
	if err := runUI(paths); err != nil || !slices.Equal(script.screens, []string{"formModel"}) {
		t.Errorf("a cancel without an installation must close the tool: %v, %v", script.screens, err)
	}

	// The first install opens the menu.
	script = newMenuScript(t,
		func(model tea.Model) {
			form := model.(*formModel)
			focusRow(t, form, rowButtons)
			press(form, keyEnter, keyEnter, keyEnter)
		},
		pushKeys(keyEsc),
	)
	script.onInstall = func() { _ = saveConfig(paths.Config, &Config{Consent: consent{Version: consentVersion}}) }
	if err := runUI(paths); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(script.screens, []string{"formModel", "listModel"}) || len(script.commands) != 1 || !strings.HasPrefix(script.commands[0], "install ") {
		t.Errorf("the first install must open the menu: %v, %q", script.screens, script.commands)
	}
}

// TestWaitModel checks that the wait after a command ends with Enter and with Esc, and not with another key.
func TestWaitModel(t *testing.T) {
	for _, keyMsg := range []tea.KeyMsg{keyEnter, keyEsc} {
		if _, cmd := (waitModel{}).Update(keyMsg); cmd == nil || cmd() != tea.Quit() {
			t.Errorf("%s must close the wait", keyMsg)
		}
	}
	if _, cmd := (waitModel{}).Update(typeText("x")); cmd != nil {
		t.Error("another key must not close the wait")
	}
	if view := (waitModel{}).View(); !strings.Contains(view, "Esc") {
		t.Errorf("the line must name Esc: %q", view)
	}
}
