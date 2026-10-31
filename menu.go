package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"clawdiator/internal/geocheck"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// The colors and the styles are the same as in warmup.
var (
	accentColor = lipgloss.AdaptiveColor{Light: "#4F4BD6", Dark: "#8B88FA"}
	mutedColor  = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#8E8E8E"}
	errorColor  = lipgloss.AdaptiveColor{Light: "#B3132B", Dark: "#FF6B8B"}
	buttonColor = lipgloss.AdaptiveColor{Light: "#E4E4E4", Dark: "#3A3A3A"}

	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	focusStyle        = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	mutedStyle        = lipgloss.NewStyle().Foreground(mutedColor)
	errorStyle        = lipgloss.NewStyle().Foreground(errorColor)
	headerStyle       = lipgloss.NewStyle().Bold(true)
	buttonStyle       = lipgloss.NewStyle().Padding(0, 1).Background(buttonColor)
	activeButtonStyle = lipgloss.NewStyle().Padding(0, 1).Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.AdaptiveColor{Light: "#4F4BD6", Dark: "#5A56E0"})
)

type menuAction int

const (
	actionConfigure menuAction = iota
	actionNotices
	actionPause
	actionResume
	actionUninstall
	actionExit
)

// lookupLocation is the location check of the configuration screen. The tests replace it.
var lookupLocation = geocheck.Lookup

// runRootCommand runs a command of this binary with root rights in the terminal, after the menu closes.
// sudo asks for the password, and the user sees the output. The tests replace it.
var runRootCommand = func(args ...string) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	name, commandArgs := "sudo", append([]string{binary}, args...)
	if os.Geteuid() == 0 {
		name, commandArgs = binary, args
	}
	command := exec.Command(name, commandArgs...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}

// isTerminal tells if the input and the output are a terminal. The menu needs a terminal.
func isTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// waitForEnter keeps the output of a command on the screen until the user pushes Enter or Esc. The tests replace it.
var waitForEnter = func() {
	_, _ = tea.NewProgram(waitModel{}).Run()
}

// waitModel shows 1 line and waits for 1 key. It does not use the alternate screen, so the output of the command stays visible.
// A line read of the terminal cannot see Esc, so the menu uses this screen.
type waitModel struct{}

func (waitModel) Init() tea.Cmd {
	return nil
}

func (m waitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter", "esc", "ctrl+c", "q", " ":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (waitModel) View() string {
	return "\n" + mutedStyle.Render(text(msgPressEnter)) + "\n"
}

// runProgram shows 1 screen until it closes. The tests replace it.
var runProgram = func(model tea.Model) (tea.Model, error) {
	return tea.NewProgram(model, tea.WithAltScreen()).Run()
}

// runUI opens the menu. Each action and each cancel goes back to the menu. Esc in the menu closes the tool.
// Without an installation, the configuration screen opens first. Its cancel closes the tool, because the menu needs an installation.
func runUI(paths appPaths) error {
	message := ""
	for {
		cfg, err := loadConfig(paths.Config)
		if errors.Is(err, os.ErrNotExist) {
			isSaved, err := runConfiguration(&Config{})
			if err == nil && !isSaved {
				fmt.Println(text(msgNoChanges))
			}
			if err != nil || !isSaved {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		menu, actions := newMainMenu(readServiceState(paths.State), time.Now())
		menu.message, message = message, ""
		finalModel, err := runProgram(menu)
		if err != nil {
			return err
		}
		selected := finalModel.(*listModel).selected
		if selected < 0 || actions[selected] == actionExit {
			return nil
		}
		switch actions[selected] {
		case actionConfigure:
			_, err = runConfiguration(cfg)
		case actionNotices:
			message, err = runNotices()
		case actionPause:
			err = runPause()
		case actionResume:
			err = runRootStep("resume")
		case actionUninstall:
			var isRemoved bool
			if isRemoved, err = runUninstall(); isRemoved {
				return nil
			}
		}
		if err != nil {
			message = text(msgActionFailed, err)
		}
	}
}

// runRootStep runs a command with root rights. Its output stays on the screen until the user pushes Enter.
func runRootStep(args ...string) error {
	err := runRootCommand(args...)
	if err != nil {
		fmt.Println(text(msgErrorPrefix), err)
	}
	waitForEnter()
	return err
}

// runConfiguration shows the configuration screen. After "Save", it runs install with the new settings.
func runConfiguration(cfg *Config) (bool, error) {
	form, values := newConfigScreen(cfg)
	finalModel, err := runProgram(form)
	if err != nil || !finalModel.(*formModel).isSaved {
		return false, err
	}
	return true, runRootStep(append([]string{"install"}, values.installArgs(cfg.Consent.Version < consentVersion)...)...)
}

// runNotices shows the notices screen. It returns the line for the menu after "Save".
func runNotices() (string, error) {
	userDir, err := getUserAppDir()
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(userDir, "agent.json")
	settings, _ := loadAgentSettings(settingsPath)
	form, values := newNoticesScreen(settings.withDefaults())
	finalModel, err := runProgram(form)
	if err != nil || !finalModel.(*formModel).isSaved {
		return "", err
	}
	// The tray can change the file at the same time, so the menu reads it again and changes only its fields.
	settings, _ = loadAgentSettings(settingsPath)
	values.applyTo(&settings)
	if err := saveAgentSettings(settingsPath, settings); err != nil {
		return "", err
	}
	return text(msgNoticesSaved), nil
}

func runPause() error {
	labels := make([]string, len(pauseChoices))
	for index, choice := range pauseChoices {
		labels[index] = text(choice.label)
	}
	finalModel, err := runProgram(&listModel{title: "clawdiator · " + text(msgMenuPause), description: []string{text(msgPauseQuestion)}, labels: labels})
	if err != nil {
		return err
	}
	selected := finalModel.(*listModel).selected
	if selected < 0 {
		return nil
	}
	if value := pauseChoices[selected].value; value != "" {
		return runRootStep("pause", "--for", value)
	}
	return runRootStep("pause", "--forever")
}

// runUninstall asks a question and removes the tool. It returns true after a removal. Then the menu closes.
func runUninstall() (bool, error) {
	dialog := &confirmModel{
		title:       "clawdiator · " + text(msgMenuUninstall),
		question:    text(msgUninstallQuestion),
		description: text(msgUninstallDescription),
		buttons:     [2]string{text(msgRemove), text(msgCancel)},
		cursor:      1,
	}
	finalModel, err := runProgram(dialog)
	if err != nil || !finalModel.(*confirmModel).isConfirmed {
		return false, err
	}
	// After the removal, the menu has nothing to show, so the tool closes and the output stays in the terminal.
	err = runRootCommand("uninstall")
	return err == nil, err
}

// newMainMenu returns the main menu and the action of each item. A pause changes "Pause…" to "Resume".
func newMainMenu(state *serviceState, now time.Time) (*listModel, []menuAction) {
	pauseLabel, pauseAction := text(msgMenuPause), actionPause
	if state != nil && state.Pause.IsOn {
		pauseLabel, pauseAction = text(msgMenuResume), actionResume
	}
	return &listModel{
		title:       "clawdiator " + version,
		description: describeServiceState(state, now),
		labels:      []string{text(msgMenuConfigure), text(msgMenuNotices), pauseLabel, text(msgMenuUninstall), text(msgMenuExit)},
	}, []menuAction{actionConfigure, actionNotices, pauseAction, actionUninstall, actionExit}
}

// describeServiceState returns the lines about the service: the country, the pause and a new version.
func describeServiceState(state *serviceState, now time.Time) []string {
	if state == nil || now.Sub(state.Heartbeat) > heartbeatTimeout {
		return []string{text(msgNoticeServiceDown)}
	}
	lines := []string{describeVerdicts(state.Country, state.blockedProviders())}
	switch {
	case state.Pause.IsOn && state.Pause.Until.IsZero():
		lines = append(lines, text(msgMenuPausedForever))
	case state.Pause.IsOn:
		lines = append(lines, text(msgMenuPausedUntil, state.Pause.Until.Local().Format("2006-01-02 15:04")))
	}
	if isNewerVersion(state.Update.Latest, version) {
		lines = append(lines, text(msgStatusUpdate, state.Update.Latest, websiteURL))
	}
	return lines
}

// describeVerdicts returns the country line, for example "Now: DE · open: Anthropic, OpenAI".
func describeVerdicts(country string, blocked []geocheck.Provider) string {
	if country == "" {
		return text(msgCountryUnknown)
	}
	var open, closed []string
	for _, provider := range geocheck.Providers() {
		if slices.Contains(blocked, provider) {
			closed = append(closed, provider.DisplayName())
		} else {
			open = append(open, provider.DisplayName())
		}
	}
	parts := []string{text(msgCountryNow, country)}
	if len(open) > 0 {
		parts = append(parts, text(msgCountryOpen, strings.Join(open, ", ")))
	}
	if len(closed) > 0 {
		parts = append(parts, text(msgCountryBlocked, strings.Join(closed, ", ")))
	}
	return strings.Join(parts, " · ")
}

// listModel is a list of choices. Enter selects 1 choice. Esc selects nothing.
type listModel struct {
	title       string
	description []string
	// message is the result of the last action. It shows under the description.
	message string
	labels  []string
	cursor  int
	// selected is the index of the selected label, or -1 after Esc.
	selected int
}

func (m *listModel) Init() tea.Cmd {
	return nil
}

func (m *listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "up", "shift+tab":
		m.cursor = wrapIndex(m.cursor-1, len(m.labels))
	case "down", "tab":
		m.cursor = wrapIndex(m.cursor+1, len(m.labels))
	case "enter":
		m.selected = m.cursor
		return m, tea.Quit
	case "esc", "ctrl+c", "q":
		m.selected = -1
		return m, tea.Quit
	}
	return m, nil
}

func (m *listModel) View() string {
	lines := []string{titleStyle.Render(m.title), ""}
	lines = append(lines, m.description...)
	if m.message != "" {
		lines = append(lines, "", headerStyle.Render(m.message))
	}
	lines = append(lines, "")
	for index, label := range m.labels {
		if index == m.cursor {
			lines = append(lines, focusStyle.Render("› "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", mutedStyle.Render(text(msgKeysMenu)))
	return renderScreen(lines)
}

// confirmModel is a question with 2 buttons. The first button confirms.
type confirmModel struct {
	title       string
	question    string
	description string
	buttons     [2]string
	cursor      int
	isConfirmed bool
}

func (m *confirmModel) Init() tea.Cmd {
	return nil
}

func (m *confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "left", "right", "up", "down", "tab", "shift+tab":
		m.cursor = 1 - m.cursor
	case "enter":
		m.isConfirmed = m.cursor == 0
		return m, tea.Quit
	case "esc", "ctrl+c":
		m.isConfirmed = false
		return m, tea.Quit
	}
	return m, nil
}

func (m *confirmModel) View() string {
	return renderScreen([]string{
		titleStyle.Render(m.title), "",
		headerStyle.Render(m.question),
		mutedStyle.Render(m.description), "",
		renderButtons(m.buttons[:], m.cursor, true), "",
		mutedStyle.Render(text(msgKeysButtons)),
	})
}

type rowKind int

const (
	rowText rowKind = iota
	rowToggle
	rowCheck
	rowStepper
	rowLink
	rowButtons
)

// formRow is 1 line of a form.
type formRow struct {
	kind   rowKind
	label  string
	hint   string
	indent int
	// rowText uses input and text. The form copies the input value to text after each key.
	input *textinput.Model
	text  *string
	// rowToggle and rowCheck use value.
	value *bool
	// rowStepper uses number, minNumber and maxNumber. ←/→ change the number in these limits.
	number    *int
	minNumber func() int
	maxNumber func() int
	// buttons holds the labels of a rowButtons row.
	buttons []string
	// target is the page that a rowLink row opens. infoText returns the value that the row shows.
	target   *formPage
	infoText func() string
	// check returns an error if the value of the row is not correct.
	check     func() error
	isVisible func() bool
}

func (row *formRow) isShown() bool {
	return row.isVisible == nil || row.isVisible()
}

func newTextRow(label, hint string, indent int, value *string, validate func(string) error) *formRow {
	input := textinput.New()
	input.Prompt = ""
	input.Width = 40
	input.SetValue(*value)
	row := &formRow{kind: rowText, label: label, hint: hint, indent: indent, input: &input, text: value}
	if validate != nil {
		row.check = func() error { return validate(input.Value()) }
	}
	return row
}

func newToggleRow(label, hint string, value *bool) *formRow {
	return &formRow{kind: rowToggle, label: label, hint: hint, value: value}
}

func (row *formRow) visibleIf(isVisible func() bool) *formRow {
	row.isVisible = isVisible
	return row
}

// formPage is 1 page of a form. Each page has its own rows, focus and scroll position.
type formPage struct {
	title        string
	rows         []*formRow
	focus        int
	offset       int
	buttonCursor int
	// preview returns the line above the separator.
	preview func() string
	keys    string
	// onClose runs when a page that a link opened goes back to the main page.
	onClose func() tea.Cmd
}

// formStep is 1 screen after "Save", for example the consent or the summary. The first button goes on, the second goes back.
type formStep struct {
	title   string
	lines   []string
	buttons [2]string
}

// formModel is a form with a main page and the pages of its links. "Save" checks all pages.
// Then confirm gives the screens to confirm. After the last screen, the form closes with isSaved.
type formModel struct {
	mainPage  *formPage
	page      *formPage
	errorText string
	width     int
	height    int
	isSaved   bool
	confirm   func() ([]formStep, error)
	steps     []formStep
	step      int
	// stepCursor is the selected button of the current step.
	stepCursor int
	// initCmd and onMessage connect a form to a background task, for example the location check.
	initCmd   func() tea.Cmd
	onMessage func(tea.Msg)
}

func newFormModel(mainPage *formPage, confirm func() ([]formStep, error)) *formModel {
	m := &formModel{mainPage: mainPage, page: mainPage, confirm: confirm}
	m.setFocus(0)
	return m
}

func (m *formModel) Init() tea.Cmd {
	if m.initCmd != nil {
		return tea.Batch(textinput.Blink, m.initCmd())
	}
	return textinput.Blink
}

func (m *formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if len(m.steps) > 0 {
			return m, m.updateStep(msg)
		}
		return m, m.updateForm(msg)
	}
	if m.onMessage != nil {
		m.onMessage(msg)
	}
	// Other messages, for example the cursor blink, go to the focused text input.
	if row := m.page.rows[m.page.focus]; row.kind == rowText && len(m.steps) == 0 {
		var cmd tea.Cmd
		*row.input, cmd = row.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *formModel) updateForm(msg tea.KeyMsg) tea.Cmd {
	m.errorText = ""
	page := m.page
	row := page.rows[page.focus]
	switch msg.String() {
	case "esc":
		if page != m.mainPage {
			return m.closePage()
		}
		return tea.Quit
	case "up", "shift+tab":
		return m.moveFocus(-1)
	case "down", "tab":
		return m.moveFocus(1)
	case "enter":
		switch row.kind {
		case rowButtons:
			return m.pushButton()
		case rowLink:
			m.page = row.target
			return m.setFocus(row.target.focus)
		}
		return m.moveFocus(1)
	}
	switch row.kind {
	case rowText:
		var cmd tea.Cmd
		*row.input, cmd = row.input.Update(msg)
		*row.text = row.input.Value()
		return cmd
	case rowToggle:
		switch msg.String() {
		case "left":
			*row.value = true
		case "right":
			*row.value = false
		case " ":
			*row.value = !*row.value
		}
	case rowCheck:
		if msg.String() == " " {
			*row.value = !*row.value
		}
	case rowStepper:
		switch msg.String() {
		case "left":
			*row.number = max(row.minNumber(), min(row.maxNumber(), *row.number-1))
		case "right":
			*row.number = max(row.minNumber(), min(row.maxNumber(), *row.number+1))
		}
	case rowButtons:
		switch msg.String() {
		case "left":
			page.buttonCursor = wrapIndex(page.buttonCursor-1, len(row.buttons))
		case "right":
			page.buttonCursor = wrapIndex(page.buttonCursor+1, len(row.buttons))
		}
	}
	return nil
}

// closePage goes back from a page of a link to the main page.
func (m *formModel) closePage() tea.Cmd {
	closed := m.page
	m.page = m.mainPage
	cmd := m.setFocus(m.mainPage.focus)
	if closed.onClose != nil {
		return tea.Batch(cmd, closed.onClose())
	}
	return cmd
}

func (m *formModel) updateStep(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "left", "right", "up", "down", "tab", "shift+tab":
		m.stepCursor = 1 - m.stepCursor
	case "enter":
		if m.stepCursor == 1 {
			m.steps = nil
			return nil
		}
		if m.step+1 < len(m.steps) {
			m.step++
			return nil
		}
		m.isSaved = true
		return tea.Quit
	case "esc":
		m.steps = nil
	}
	return nil
}

// findError returns the index of the first shown row of the page with an error, and the error.
func findError(page *formPage) (int, error) {
	for index, row := range page.rows {
		if row.check == nil || !row.isShown() {
			continue
		}
		if err := row.check(); err != nil {
			return index, err
		}
	}
	return -1, nil
}

// pushButton runs the action of the selected button. "Done" on a page of a link checks the page and goes back.
// "Save" checks all pages and opens the screens to confirm. "Cancel" closes the form.
func (m *formModel) pushButton() tea.Cmd {
	if m.page != m.mainPage {
		if index, err := findError(m.page); err != nil {
			m.errorText = err.Error()
			return m.setFocus(index)
		}
		return m.closePage()
	}
	if m.page.buttonCursor == 1 {
		return tea.Quit
	}
	if index, err := findError(m.mainPage); err != nil {
		m.errorText = err.Error()
		return m.setFocus(index)
	}
	// A page of a link can have an error after Esc. Then the link gets the focus.
	for index, row := range m.mainPage.rows {
		if row.kind != rowLink || !row.isShown() {
			continue
		}
		if _, err := findError(row.target); err != nil {
			m.errorText = err.Error()
			return m.setFocus(index)
		}
	}
	steps, err := m.confirm()
	if err != nil {
		m.errorText = err.Error()
		return nil
	}
	if len(steps) == 0 {
		m.isSaved = true
		return tea.Quit
	}
	m.steps, m.step, m.stepCursor = steps, 0, 0
	return nil
}

// setFocus gives the focus to a row of the current page.
func (m *formModel) setFocus(index int) tea.Cmd {
	page := m.page
	if row := page.rows[page.focus]; row.kind == rowText {
		row.input.Blur()
	}
	page.focus = index
	if row := page.rows[index]; row.kind == rowText {
		row.input.CursorEnd()
		return row.input.Focus()
	}
	return nil
}

// moveFocus moves to the next shown row. At the first and the last row, it wraps.
func (m *formModel) moveFocus(step int) tea.Cmd {
	page := m.page
	for distance := 1; distance <= len(page.rows); distance++ {
		index := wrapIndex(page.focus+step*distance, len(page.rows))
		if page.rows[index].isShown() {
			return m.setFocus(index)
		}
	}
	return nil
}

func (m *formModel) View() string {
	if len(m.steps) > 0 {
		step := m.steps[m.step]
		lines := []string{titleStyle.Render(step.title), ""}
		lines = append(lines, step.lines...)
		lines = append(lines, "", renderButtons(step.buttons[:], m.stepCursor, true), "", mutedStyle.Render(text(msgKeysButtons)))
		return renderScreen(lines)
	}
	page := m.page
	header := []string{titleStyle.Render(page.title)}
	footer := m.viewFooter()
	rowLines, focusLine := m.viewRows()
	// The screen has 1 empty line at the top, 1 line before the rows and 1 line after the rows.
	available := m.height - 3 - len(header) - len(footer)
	topMark, bottomMark := "", ""
	if m.height > 0 && available < len(rowLines) {
		available = max(available, 1)
		if focusLine < page.offset {
			page.offset = focusLine
		}
		if focusLine >= page.offset+available {
			page.offset = focusLine - available + 1
		}
		page.offset = min(page.offset, len(rowLines)-available)
		if page.offset > 0 {
			topMark = mutedStyle.Render("  ▲")
		}
		if page.offset+available < len(rowLines) {
			bottomMark = mutedStyle.Render("  ▼")
		}
		rowLines = rowLines[page.offset : page.offset+available]
	} else {
		page.offset = 0
	}
	lines := append(header, topMark)
	lines = append(lines, rowLines...)
	lines = append(lines, bottomMark)
	lines = append(lines, footer...)
	return renderScreen(lines)
}

// viewRows returns the lines of the shown rows of the current page and the line number of the focused row.
func (m *formModel) viewRows() ([]string, int) {
	page := m.page
	labelWidth := 0
	for _, row := range page.rows {
		if row.isShown() && row.kind != rowCheck && row.kind != rowButtons {
			labelWidth = max(labelWidth, lipgloss.Width(strings.Repeat("  ", row.indent)+row.label))
		}
	}
	labelWidth += 2
	var lines []string
	focusLine := 0
	for index, row := range page.rows {
		if !row.isShown() {
			continue
		}
		isFocused := index == page.focus
		if isFocused {
			focusLine = len(lines)
		}
		marker := "  "
		if isFocused {
			marker = focusStyle.Render("›") + " "
		}
		indent := strings.Repeat("  ", row.indent)
		label := styleLabel(padRight(indent+row.label, labelWidth), isFocused)
		var line string
		switch row.kind {
		case rowText:
			// The screen has 1 space at the left and 2 characters for the marker.
			if m.width > 0 {
				row.input.Width = min(40, max(8, m.width-3-labelWidth-1))
			}
			line = marker + label + row.input.View()
		case rowToggle:
			line = marker + label + renderChoice(*row.value, isFocused)
		case rowStepper:
			line = marker + label + styleLabel(fmt.Sprintf("◀ %d ▶", *row.number), isFocused)
		case rowLink:
			line = marker + label + styleLabel(row.infoText(), isFocused)
		case rowCheck:
			box := "[ ]"
			if *row.value {
				box = "[x]"
			}
			line = marker + indent + styleLabel(box+" "+row.label, isFocused)
		case rowButtons:
			line = marker + renderButtons(row.buttons, page.buttonCursor, isFocused)
		}
		lines = append(lines, line)
	}
	return lines, focusLine
}

func (m *formModel) viewFooter() []string {
	page := m.page
	row := page.rows[page.focus]
	lines := []string{headerStyle.Render(page.preview())}
	width := m.width - 2
	if width <= 0 || width > 80 {
		width = 80
	}
	lines = append(lines, mutedStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, strings.Split(mutedStyle.Width(width).Render(row.hint), "\n")...)
	errorText := m.errorText
	if errorText == "" && row.kind == rowText && row.check != nil {
		if err := row.check(); err != nil {
			errorText = err.Error()
		}
	}
	if errorText != "" {
		lines = append(lines, strings.Split(errorStyle.Width(width).Render(capitalizeFirst(errorText)), "\n")...)
	}
	return append(lines, mutedStyle.Render(page.keys))
}

// configForm holds the values of the configuration screen.
type configForm struct {
	BlockedCountries string
	// GeoServices has 1 value for each service of geocheck.Services, in the same order.
	GeoServices   []bool
	GeoMinReplies int
	AutoUpdate    bool
}

func newConfigForm(cfg *Config) *configForm {
	values := &configForm{
		BlockedCountries: strings.Join(cfg.BlockedCountries, ", "),
		GeoServices:      make([]bool, len(geocheck.Services())),
		GeoMinReplies:    cfg.GeoMinReplies,
		AutoUpdate:       cfg.AutoUpdate,
	}
	names := cfg.GeoServices
	if len(names) == 0 {
		names = geocheck.DefaultServiceNames()
	}
	for index, service := range geocheck.Services() {
		values.GeoServices[index] = slices.ContainsFunc(names, func(name string) bool {
			return strings.EqualFold(strings.TrimSpace(name), service.Name)
		})
	}
	if values.GeoMinReplies == 0 {
		values.GeoMinReplies = geocheck.DefaultMinRepliesFor(len(values.getSelectedServiceNames()))
	}
	return values
}

// getSelectedServiceNames returns the names of the selected location services, in the order of geocheck.Services.
func (values *configForm) getSelectedServiceNames() []string {
	var names []string
	for index, isSelected := range values.GeoServices {
		if isSelected {
			names = append(names, geocheck.Services()[index].Name)
		}
	}
	return names
}

// getGeoOptions returns the options of the location check of the form, or an error if they are not correct.
func (values *configForm) getGeoOptions() (geocheck.Options, error) {
	names := values.getSelectedServiceNames()
	if len(names) == 0 {
		return geocheck.Options{}, errorf(msgSelectOneService)
	}
	return getGeoOptions(&Config{GeoServices: names, GeoMinReplies: values.GeoMinReplies})
}

func (values *configForm) checkGeoServices() error {
	_, err := values.getGeoOptions()
	return err
}

// applyTo copies the form to the config. The default values stay empty, so a new version of the tool can change them.
func (values *configForm) applyTo(cfg *Config) {
	cfg.BlockedCountries = splitList(values.BlockedCountries, true)
	names := values.getSelectedServiceNames()
	cfg.GeoServices = names
	if slices.Equal(names, geocheck.DefaultServiceNames()) {
		cfg.GeoServices = nil
	}
	cfg.GeoMinReplies = values.GeoMinReplies
	if cfg.GeoMinReplies == geocheck.DefaultMinRepliesFor(len(names)) {
		cfg.GeoMinReplies = 0
	}
	cfg.AutoUpdate = values.AutoUpdate
}

// installArgs returns the options of "install" for the form. After the consent page of the form, it adds --yes.
func (values *configForm) installArgs(isConsentShown bool) []string {
	cfg := &Config{}
	values.applyTo(cfg)
	autoUpdate := "off"
	if cfg.AutoUpdate {
		autoUpdate = "on"
	}
	args := []string{
		"--blocked-countries=" + strings.Join(cfg.BlockedCountries, ","),
		"--geo-services=" + strings.Join(cfg.GeoServices, ","),
		"--geo-min-replies=" + strconv.Itoa(cfg.GeoMinReplies),
		"--auto-update=" + autoUpdate,
	}
	if isConsentShown {
		args = append(args, "--yes")
	}
	return args
}

// validateCountries checks the codes of the field "Blocked countries", for example "AF, MM".
func validateCountries(value string) error {
	for _, code := range splitList(value, true) {
		if !isCountryCode(code) {
			return errorf(msgCountryCodeNotValid, code)
		}
	}
	return nil
}

// configLocation is the location check of the configuration screen. It uses the selected services of the form.
type configLocation struct {
	values   *configForm
	result   geocheck.Result
	err      error
	isReady  bool
	id       int
	services string
}

// locationMsg carries the result of the location check to the screen.
type locationMsg struct {
	id     int
	result geocheck.Result
	err    error
}

// start starts a check with the selected services. The screen ignores the result of an older check.
func (location *configLocation) start() tea.Cmd {
	location.id++
	location.isReady = false
	location.services = strings.Join(location.values.getSelectedServiceNames(), ",")
	id := location.id
	options, err := location.values.getGeoOptions()
	return func() tea.Msg {
		if err != nil {
			return locationMsg{id: id, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := lookupLocation(ctx, options)
		return locationMsg{id: id, result: result, err: err}
	}
}

func (location *configLocation) handle(msg tea.Msg) {
	if msg, ok := msg.(locationMsg); ok && msg.id == location.id {
		location.result, location.err, location.isReady = msg.result, msg.err, true
	}
}

// describe returns the country line for the values of the form, also for the blocked countries that the user types now.
func (location *configLocation) describe() string {
	if !location.isReady {
		return text(msgCountryLookup)
	}
	if location.err != nil {
		return text(msgCountryUnknown)
	}
	countries := splitList(location.values.BlockedCountries, true)
	var blocked []geocheck.Provider
	for _, provider := range geocheck.Providers() {
		if !geocheck.Check(provider, location.result, nil, countries).Allowed() {
			blocked = append(blocked, provider)
		}
	}
	return describeVerdicts(strings.Join(location.result.Countries(), " / "), blocked)
}

// newConfigScreen returns the configuration screen and its values. The main page has all settings.
// The page of the location services has the services and the minimum of replies.
func newConfigScreen(cfg *Config) (*formModel, *configForm) {
	values := newConfigForm(cfg)
	location := &configLocation{values: values}
	servicesLine := func() string {
		return text(msgGeoServicesLink, len(values.getSelectedServiceNames()), values.GeoMinReplies)
	}
	var serviceRows []*formRow
	for index, service := range geocheck.Services() {
		row := &formRow{kind: rowCheck, label: service.Name, hint: describeGeoService(service), value: &values.GeoServices[index]}
		if index == 0 {
			row.check = func() error {
				if len(values.getSelectedServiceNames()) == 0 {
					return errorf(msgSelectOneService)
				}
				return nil
			}
		}
		serviceRows = append(serviceRows, row)
	}
	serviceRows = append(serviceRows,
		&formRow{
			kind: rowStepper, label: text(msgFieldGeoMinReplies), hint: text(msgHintGeoMinReplies), number: &values.GeoMinReplies,
			minNumber: func() int { return 1 },
			maxNumber: func() int { return max(1, len(values.getSelectedServiceNames())) },
			check:     values.checkGeoServices,
		},
		&formRow{kind: rowButtons, hint: text(msgHintDoneButton), buttons: []string{text(msgDone)}},
	)
	servicesPage := &formPage{title: "clawdiator · " + text(msgFieldGeoServices), rows: serviceRows, keys: text(msgKeysServices), preview: servicesLine}
	// A new selection of services can give another country, so the screen checks the location again.
	servicesPage.onClose = func() tea.Cmd {
		if strings.Join(values.getSelectedServiceNames(), ",") != location.services {
			return location.start()
		}
		return nil
	}
	mainPage := &formPage{
		title: "clawdiator · " + text(msgMenuConfigure), keys: text(msgKeysForm), preview: location.describe,
		rows: []*formRow{
			newTextRow(text(msgFieldBlockedCountries), text(msgHintBlockedCountries), 0, &values.BlockedCountries, validateCountries),
			{kind: rowLink, label: text(msgFieldGeoServices), hint: text(msgHintGeoServicesLink), target: servicesPage, infoText: servicesLine},
			newToggleRow(text(msgTrayAutoUpdate), text(msgHintAutoUpdate), &values.AutoUpdate),
			{kind: rowButtons, hint: text(msgHintSaveButtons), buttons: []string{text(msgSave), text(msgCancel)}},
		},
	}
	confirm := func() ([]formStep, error) {
		summary := *cfg
		values.applyTo(&summary)
		options, err := getGeoOptions(&summary)
		if err != nil {
			return nil, err
		}
		var steps []formStep
		if cfg.Consent.Version < consentVersion {
			steps = append(steps, formStep{
				title:   "clawdiator · " + text(msgConsentTitle),
				lines:   strings.Split(strings.TrimRight(text(msgConsentText), "\n"), "\n"),
				buttons: [2]string{text(msgAgree), text(msgBack)},
			})
		}
		blocked := strings.Join(summary.BlockedCountries, ", ")
		if blocked == "" {
			blocked = text(msgNone)
		}
		steps = append(steps, formStep{
			title: "clawdiator · " + text(msgSummaryTitle),
			lines: []string{
				text(msgSummaryBlockedCountries, blocked),
				text(msgSummaryGeoServices, strings.Join(geocheck.ServiceNames(options.Services), ", "), options.MinReplies),
				text(msgSummaryAutoUpdate, yesNo(summary.AutoUpdate)),
				"", headerStyle.Render(text(msgSummaryConfirm)),
			},
			buttons: [2]string{text(msgSave), text(msgBack)},
		})
		return steps, nil
	}
	form := newFormModel(mainPage, confirm)
	form.initCmd, form.onMessage = location.start, location.handle
	return form, values
}

// describeGeoService returns the hint of a location service on its page.
func describeGeoService(service geocheck.Service) string {
	hint := text(msgHintGeoService, service.URL, yesNo(service.HasRegion))
	if service.IsUndocumented {
		hint += " " + text(msgGeoServiceUndocumented)
	}
	return hint
}

// noticesForm holds the values of the notices screen.
type noticesForm struct {
	IsAll   bool
	Seconds string
}

func (values *noticesForm) applyTo(settings *agentSettings) {
	settings.PreBlockNotices = preBlockNoticesLong
	if values.IsAll {
		settings.PreBlockNotices = preBlockNoticesAll
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(values.Seconds)); err == nil {
		settings.PreBlockNoticeSeconds = seconds
	}
}

// validateNoticeSeconds checks the time of a long pre-block. The agent accepts from 5 to 300 seconds.
func validateNoticeSeconds(value string) error {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err != nil || seconds < 5 || seconds > 300 {
		return errorf(msgNoticeSecondsInvalid)
	}
	return nil
}

// newNoticesScreen returns the screen of the notices after a change of the network and after a wake.
// The settings are in agent.json of the user, so the screen needs no administrator rights.
func newNoticesScreen(settings agentSettings) (*formModel, *noticesForm) {
	values := &noticesForm{IsAll: settings.PreBlockNotices == preBlockNoticesAll, Seconds: strconv.Itoa(settings.PreBlockNoticeSeconds)}
	isLong := func() bool { return !values.IsAll }
	mainPage := &formPage{
		title: "clawdiator · " + text(msgMenuNotices), keys: text(msgKeysForm),
		preview: func() string { return text(msgNoticesPreview) },
		rows: []*formRow{
			newToggleRow(text(msgFieldNoticesAll), text(msgHintNoticesAll), &values.IsAll),
			newTextRow(text(msgFieldNoticeSeconds), text(msgHintNoticeSeconds), 1, &values.Seconds, validateNoticeSeconds).visibleIf(isLong),
			{kind: rowButtons, hint: text(msgHintNoticesButtons), buttons: []string{text(msgSave), text(msgCancel)}},
		},
	}
	return newFormModel(mainPage, func() ([]formStep, error) { return nil, nil }), values
}

// renderScreen adds a margin of 1 line at the top and 1 space at the left.
func renderScreen(lines []string) string {
	return "\n " + strings.Join(lines, "\n ")
}

func renderButtons(labels []string, cursor int, isActive bool) string {
	buttons := make([]string, len(labels))
	for index, label := range labels {
		if isActive && index == cursor {
			buttons[index] = activeButtonStyle.Render(label)
		} else {
			buttons[index] = buttonStyle.Render(label)
		}
	}
	return strings.Join(buttons, "  ")
}

func renderChoice(isYes bool, isFocused bool) string {
	yes, no := "○ "+text(msgYes), "○ "+text(msgNo)
	if isYes {
		yes = "● " + text(msgYes)
	} else {
		no = "● " + text(msgNo)
	}
	if isFocused {
		if isYes {
			yes = focusStyle.Render(yes)
		} else {
			no = focusStyle.Render(no)
		}
	}
	return yes + "   " + no
}

func yesNo(value bool) string {
	if value {
		return text(msgYes)
	}
	return text(msgNo)
}

// capitalizeFirst changes the first letter to uppercase. The Go errors start with a lowercase letter.
func capitalizeFirst(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func styleLabel(label string, isFocused bool) string {
	if isFocused {
		return focusStyle.Render(label)
	}
	return label
}

func padRight(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func wrapIndex(index, count int) int {
	return (index%count + count) % count
}
