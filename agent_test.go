package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"geocheck"
)

// agentRun drives an agent model with a state that the test changes, the same as the service.
type agentRun struct {
	t     *testing.T
	model *agentModel
	state *serviceState
	core  *core
	now   time.Time
}

// newAgentRun returns an agent model after its first update. The service state is open in Germany.
func newAgentRun(t *testing.T, settings agentSettings) *agentRun {
	t.Helper()
	s := newOpenSimulation(t)
	run := &agentRun{t: t, model: newAgentModel(settings), state: s.c.state, core: s.c, now: testStart.Add(2 * time.Second)}
	if notices := run.update(); len(notices) != 0 {
		t.Fatalf("an open state at the start must give no notice: %v", notices)
	}
	return run
}

// update moves the heartbeat and the clock by 2 seconds, then updates the model.
func (r *agentRun) update() []string {
	r.now = r.now.Add(2 * time.Second)
	r.state.Heartbeat = r.now
	var texts []string
	for _, next := range r.model.update(r.now, r.state, false) {
		texts = append(texts, next.Text)
	}
	return texts
}

func TestAgentSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	settings, isFirstRun := loadAgentSettings(path)
	if !isFirstRun || settings.PreBlockNotices != preBlockNoticesLong || settings.PreBlockNoticeSeconds != 20 {
		t.Errorf("a missing file must give the defaults and the first run: %+v, %v", settings, isFirstRun)
	}
	if err := saveAgentSettings(path, agentSettings{PreBlockNotices: preBlockNoticesAll, PreBlockNoticeSeconds: 1000}); err != nil {
		t.Fatal(err)
	}
	settings, isFirstRun = loadAgentSettings(path)
	if isFirstRun || settings.PreBlockNotices != preBlockNoticesAll || settings.PreBlockNoticeSeconds != 20 {
		t.Errorf("the saved file must keep the mode and correct a wrong number: %+v, %v", settings, isFirstRun)
	}
}

func TestHeartbeatWatcher(t *testing.T) {
	var watcher heartbeatWatcher
	beat := testStart
	if watcher.isServiceDown(testStart, beat) {
		t.Error("the first value must not count as down")
	}
	if watcher.isServiceDown(testStart.Add(heartbeatTimeout-time.Second), beat) {
		t.Error("the same value for less than 2 minutes must not count as down")
	}
	if !watcher.isServiceDown(testStart.Add(heartbeatTimeout), beat) {
		t.Error("the same value for 2 minutes must count as down")
	}
	if watcher.isServiceDown(testStart.Add(heartbeatTimeout+time.Second), beat.Add(time.Second)) {
		t.Error("a new value must count as up")
	}
	// After a sleep, the old value can be 8 hours old. Only the own clock of the watcher counts.
	var afterSleep heartbeatWatcher
	if afterSleep.isServiceDown(testStart.Add(8*time.Hour), testStart) {
		t.Error("an old value at the first read must not count as down")
	}
	if afterSleep.isServiceDown(testStart.Add(8*time.Hour+2*time.Second), testStart) {
		t.Error("the same old value 2 seconds later must not count as down: the age of the value does not count")
	}
}

func TestAgentLoginNotices(t *testing.T) {
	useLanguage(t, languageEnglish)
	open := newOpenSimulation(t).c.state
	if got := newAgentModel(agentSettings{}).update(testStart, open, true); len(got) != 1 || got[0].Text != "clawdiator works. Country: DE." {
		t.Errorf("the first run must give the welcome notice: %v", got)
	}
	blocked := newCore(&serviceState{}, nil)
	check := mustFindCheck(t, blocked.tick(testStart, "wifi"))
	blocked.handleCheck(testStart.Add(time.Second), check.Generation, newResult("CN", "CN"), nil)
	if got := newAgentModel(agentSettings{}).update(testStart, blocked.state, false); len(got) != 1 || got[0].Text != "Anthropic and OpenAI are blocked. Country: CN." {
		t.Errorf("a login during a block must give 1 block notice: %v", got)
	}
	paused := newOpenSimulation(t).c
	paused.pause(testStart, time.Time{}, "ivan")
	if got := newAgentModel(agentSettings{}).update(testStart, paused.state, false); len(got) != 1 || got[0].Text != "The block is paused with no end." {
		t.Errorf("a login during a pause must give 1 pause notice: %v", got)
	}
	if got := newAgentModel(agentSettings{}).update(testStart, nil, false); len(got) != 0 {
		t.Errorf("a missing state at the start must give no notice: %v", got)
	}
}

func TestAgentBlockAndUnblock(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	check := mustFindCheck(t, run.core.tick(run.now, "hotel"))
	run.core.handleCheck(run.now, check.Generation, newResult("AF", "AF"), nil)
	got := run.update()
	if !slices.Equal(got, []string{"Anthropic is blocked. Country: AF."}) {
		t.Fatalf("a block must give 1 notice, and a short pre-block none: %v", got)
	}
	check = mustFindCheck(t, run.core.tick(run.now, "home"))
	run.core.handleCheck(run.now, check.Generation, newResult("DE"), nil)
	if got := run.update(); !slices.Equal(got, []string{"Anthropic is available again. Country: DE."}) {
		t.Errorf("an unblock after a block notice must give 1 notice: %v", got)
	}
}

func TestAgentShortPreBlockGivesNoNotice(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	check := mustFindCheck(t, run.core.tick(run.now, "hotel"))
	if got := run.update(); len(got) != 0 {
		t.Errorf("a pre-block must give no notice at once with the setting long: %v", got)
	}
	run.core.handleCheck(run.now, check.Generation, newResult("DE"), nil)
	if got := run.update(); len(got) != 0 {
		t.Errorf("the end of a short pre-block must give no notice: %v", got)
	}
}

func TestAgentLongPreBlock(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	check := mustFindCheck(t, run.core.tick(run.now, "hotel"))
	var notices []string
	for index := 0; index < 11; index++ {
		notices = append(notices, run.update()...)
	}
	if !slices.Equal(notices, []string{"Anthropic and OpenAI are blocked until the country check ends."}) {
		t.Fatalf("a pre-block of more than 20 seconds must give 1 notice: %v", notices)
	}
	run.core.handleCheck(run.now, check.Generation, newResult("DE"), nil)
	if got := run.update(); !slices.Equal(got, []string{"Anthropic and OpenAI are available again."}) {
		t.Errorf("the end of a long pre-block must give 1 notice: %v", got)
	}
}

func TestAgentPreBlockWithSettingAll(t *testing.T) {
	run := newAgentRun(t, agentSettings{PreBlockNotices: preBlockNoticesAll})
	check := mustFindCheck(t, run.core.tick(run.now, "hotel"))
	if got := run.update(); !slices.Equal(got, []string{"Anthropic and OpenAI are blocked until the country check ends."}) {
		t.Errorf("with the setting all, a pre-block must give a notice at once: %v", got)
	}
	run.core.handleCheck(run.now, check.Generation, newResult("DE"), nil)
	if got := run.update(); !slices.Equal(got, []string{"Anthropic and OpenAI are available again."}) {
		t.Errorf("with the setting all, the end of a pre-block must give a notice: %v", got)
	}
}

// A pre-block that ends in a block must not say that the providers are available again.
func TestAgentPreBlockEndsInBlock(t *testing.T) {
	run := newAgentRun(t, agentSettings{PreBlockNotices: preBlockNoticesAll})
	check := mustFindCheck(t, run.core.tick(run.now, "hotel"))
	run.update()
	run.core.handleCheck(run.now, check.Generation, newResult("AF"), nil)
	if got := run.update(); !slices.Equal(got, []string{"Anthropic is blocked. Country: AF.", "OpenAI is available again."}) {
		t.Errorf("only the provider that is not blocked is available again: %v", got)
	}
}

func TestAgentPauseNotices(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	until := time.Date(2026, 10, 14, 18, 0, 0, 0, time.Local)
	run.core.pause(run.now, until, "ivan")
	if got := run.update(); !slices.Equal(got, []string{"The block is paused until 18:00, 2026-10-14."}) {
		t.Errorf("a pause must give 1 notice: %v", got)
	}
	check := mustFindCheck(t, run.core.tick(run.now, "hotel"))
	run.core.handleCheck(run.now, check.Generation, newResult("CN"), nil)
	if got := run.update(); !slices.Equal(got, []string{"Anthropic and OpenAI: not available in CN. The pause is on."}) {
		t.Errorf("a verdict change during a pause must give 1 notice: %v", got)
	}
	run.core.resume(run.now)
	got := run.update()
	if len(got) != 2 || got[0] != "The pause ended. The country check applies again." || got[1] != "Anthropic and OpenAI are blocked. Country: CN." {
		t.Errorf("the end of the pause must give its notice and the block notice: %v", got)
	}
}

func TestAgentServiceDownAndBack(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	frozen := run.state.Heartbeat
	var notices []string
	for index := 0; index < 70; index++ {
		run.now = run.now.Add(2 * time.Second)
		for _, next := range run.model.update(run.now, run.state, false) {
			notices = append(notices, next.Text)
		}
		run.state.Heartbeat = frozen
	}
	if !slices.Equal(notices, []string{"The clawdiator service does not work. The block can be missing."}) {
		t.Fatalf("a heartbeat without a change for 2 minutes must give 1 notice: %v", notices)
	}
	if got := run.update(); !slices.Equal(got, []string{"The clawdiator service works again."}) {
		t.Errorf("a new heartbeat must give 1 notice: %v", got)
	}
}

func TestAgentNewInstallation(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	run.model.lastSeq = 500
	run.core.addError(run.now, "disk full")
	if got := run.update(); len(got) != 1 || !strings.Contains(got[0], "disk full") {
		t.Errorf("after a new installation, the sequence starts again, and the new events must count: %v", got)
	}
}

func TestAgentNoticesInRussian(t *testing.T) {
	useLanguage(t, languageRussian)
	if got := getBlockNotice(geocheck.Providers(), "CN", "not_supported").Text; got != "Anthropic и OpenAI заблокированы. Страна: CN." {
		t.Errorf("the Russian block notice = %q", got)
	}
	if got := getAvailableNotice([]geocheck.Provider{geocheck.OpenAI}, "").Text; got != "OpenAI снова доступен." {
		t.Errorf("the Russian notice for 1 provider = %q", got)
	}
}

func TestBuildTrayView(t *testing.T) {
	useLanguage(t, languageEnglish)
	open := newOpenSimulation(t).c.state
	if view := buildTrayView(open, false); view.Color != trayGreen || view.Country != "Country: DE" || !slices.Equal(view.Providers, []string{"Anthropic: open", "OpenAI: open"}) {
		t.Errorf("the open view = %+v", view)
	}
	if view := buildTrayView(open, true); view.Color != trayAlert {
		t.Errorf("a service that does not work must give the alert color: %+v", view)
	}
	if view := buildTrayView(nil, false); view.Color != trayAlert {
		t.Errorf("a missing state must give the alert color: %+v", view)
	}
	preBlocked := newCore(&serviceState{}, nil)
	preBlocked.tick(testStart, "wifi")
	if view := buildTrayView(preBlocked.state, false); view.Color != trayYellow || view.Providers[0] != "Anthropic: BLOCKED" {
		t.Errorf("the pre-block view = %+v", view)
	}
	blocked := newCore(&serviceState{}, nil)
	check := mustFindCheck(t, blocked.tick(testStart, "wifi"))
	blocked.handleCheck(testStart, check.Generation, newResult("AF"), nil)
	if view := buildTrayView(blocked.state, false); view.Color != trayRed || view.Providers[1] != "OpenAI: open" {
		t.Errorf("the block view = %+v", view)
	}
	blocked.pause(testStart, time.Time{}, "ivan")
	if view := buildTrayView(blocked.state, false); view.Color != trayGrey || !view.IsPaused {
		t.Errorf("the pause view = %+v", view)
	}
}

func TestQuoteFunctions(t *testing.T) {
	if got := quoteAppleScript(`say "hi" \ bye`); got != `"say \"hi\" \\ bye"` {
		t.Errorf("quoteAppleScript = %s", got)
	}
	if got := quoteShell("/Library/Application Support/it's"); got != `'/Library/Application Support/it'\''s'` {
		t.Errorf("quoteShell = %s", got)
	}
}

// The user did not see the block, for example because the event list was cut. Then the unblock must give no notice.
func TestAgentUnblockOnlyAfterKnownBlock(t *testing.T) {
	run := newAgentRun(t, agentSettings{})
	run.state.provider(geocheck.Anthropic).IsBlocked = true
	check := mustFindCheck(t, run.core.tick(run.now, "home"))
	run.core.handleCheck(run.now, check.Generation, newResult("DE"), nil)
	if kinds := getEventKinds(run.state); !slices.Contains(kinds, eventUnblock) {
		t.Fatalf("the setup must give an unblock event: %v", kinds)
	}
	if got := run.update(); len(got) != 0 {
		t.Errorf("an unblock without a known block must give no notice: %v", got)
	}
}
