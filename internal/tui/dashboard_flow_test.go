package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
	"trestle/internal/config"
	"trestle/internal/deps"
	"trestle/internal/policy"
	"trestle/internal/runlog"
	"trestle/internal/toolchain"
)

// keyPress builds the key message the console would receive for a key name such
// as "enter", "pgdown", "ctrl+c" or a plain letter.
func keyPress(spec string) tea.KeyPressMsg {
	codes := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "backspace": tea.KeyBackspace,
		"tab": tea.KeyTab, "left": tea.KeyLeft, "right": tea.KeyRight,
		"up": tea.KeyUp, "down": tea.KeyDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
		"f2": tea.KeyF2, "f3": tea.KeyF3, "f4": tea.KeyF4, "f5": tea.KeyF5,
	}
	if code, ok := codes[spec]; ok {
		return tea.KeyPressMsg(tea.Key{Code: code})
	}
	switch spec {
	case "ctrl+c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	case "ctrl+r":
		return tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl})
	case "ctrl+u":
		return tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModCtrl})
	case "ctrl+w":
		return tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModCtrl})
	case "shift+tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	}
	return tea.KeyPressMsg(tea.Key{Code: []rune(spec)[0], Text: spec})
}

// tap delivers a key through the real update pipeline and reports any command.
func tap(model dashboardModel, spec string) (dashboardModel, tea.Cmd) {
	updated, command := model.Update(keyPress(spec))
	next, ok := updated.(dashboardModel)
	if !ok {
		panic(fmt.Sprintf("update returned %T", updated))
	}
	return next, command
}

func walkKeys(t *testing.T, model dashboardModel, specs ...string) dashboardModel {
	t.Helper()
	for _, spec := range specs {
		model = nextState(t, model, spec)
	}
	return model
}

func nextState(t *testing.T, model dashboardModel, spec string) dashboardModel {
	t.Helper()
	updated, _ := tap(model, spec)
	return updated
}

// cmdSync runs one command to completion, turning panics into failures.
func cmdSync(t *testing.T, cmd tea.Cmd) (msg tea.Msg, failure error) {
	t.Helper()
	if cmd == nil {
		return nil, nil
	}
	done := make(chan struct{})
	var recovered any
	go func() {
		defer func() {
			recovered = recover()
			close(done)
		}()
		msg = cmd()
	}()
	select {
	case <-done:
		if recovered != nil {
			if err, ok := recovered.(error); ok {
				return msg, err
			}
			return msg, fmt.Errorf("command panicked: %v", recovered)
		}
		return msg, nil
	case <-time.After(10 * time.Second):
		return nil, errors.New("command did not finish in 10s")
	}
}

// fire executes a command, unwrapping one level of tea batches.
func fire(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg, failure := cmdSync(t, cmd)
	if failure != nil {
		t.Errorf("command failed: %v", failure)
	}
	batch, isBatch := msg.(tea.BatchMsg)
	if !isBatch {
		return []tea.Msg{msg}
	}
	out := make([]tea.Msg, len(batch))
	var mu sync.Mutex
	var group sync.WaitGroup
	for index, member := range batch {
		if member == nil {
			continue
		}
		group.Add(1)
		go func(index int, member tea.Cmd) {
			defer group.Done()
			inner, innerFailure := cmdSync(t, member)
			mu.Lock()
			out[index] = inner
			if innerFailure != nil {
				t.Errorf("batch member failed: %v", innerFailure)
			}
			mu.Unlock()
		}(index, member)
	}
	group.Wait()
	return out
}

// feed runs a command and funnels every resulting message back through Update.
// Pulse messages are dropped so the spinner loop stays finite, and the depth
// budget limits recursive command churn.
func feed(t *testing.T, model dashboardModel, cmd tea.Cmd, depth int) dashboardModel {
	t.Helper()
	if cmd == nil || depth <= 0 {
		return model
	}
	for _, msg := range fire(t, cmd) {
		if depth <= 0 {
			break
		}
		updated, inner := model.Update(msg)
		next, ok := updated.(dashboardModel)
		if !ok {
			t.Fatalf("update returned %T", updated)
		}
		if _, isPulse := msg.(pulseMessage); isPulse {
			model = next
			continue
		}
		model = feed(t, next, inner, depth-1)
	}
	return model
}

// contains asserts the haystack carries every fragment.
func contains(t *testing.T, haystack, label string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Fatalf("%s missing %q:\n%s", label, needle, haystack)
		}
	}
}

type callJournal struct {
	mu    sync.Mutex
	calls []string
}

func (journal *callJournal) hit(entry string) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.calls = append(journal.calls, entry)
}

func (journal *callJournal) has(prefix string) bool {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	for _, call := range journal.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

func journalServices(journal *callJournal) Services {
	C := func(name string, emit int) func(context.Context, func(string)) error {
		return func(ctx context.Context, progress func(string)) error {
			journal.hit(name)
			for i := 0; i < emit; i++ {
				progress(fmt.Sprintf("progress %s %d", name, i))
			}
			return ctx.Err()
		}
	}
	V := func(name string) func(string) error {
		return func(value string) error {
			journal.hit(name + ":" + value)
			return nil
		}
	}
	return Services{
		Build: C("Build", 2),
		BuildTargets: func(ctx context.Context, targets []string, all bool, progress func(string)) error {
			journal.hit("BuildTargets")
			progress("progress targets")
			return ctx.Err()
		},
		BuildTargetsForce: func(ctx context.Context, targets []string, all bool, progress func(string)) error {
			journal.hit("BuildTargetsForce")
			progress("progress force")
			return ctx.Err()
		},
		AssessTargets: func(context.Context) (config.Config, map[string]policy.Status, policy.Report, error) {
			journal.hit("AssessTargets")
			return config.Default("demo"), map[string]policy.Status{"app": {}}, policy.Report{}, nil
		},
		PreviewTask: func(context.Context, string) (config.TaskPreview, error) {
			journal.hit("PreviewTask")
			return config.TaskPreview{Task: config.Task{Command: []string{"echo", "hi"}}, Changes: []config.TaskSettingChange{{Field: "build.profile", Before: "debug", After: "release"}}}, nil
		},
		RunTask: func(ctx context.Context, _ string, _ config.TaskPreview, progress func(string)) error {
			journal.hit("RunTask")
			progress("progress task")
			return ctx.Err()
		},
		AddPackage: V("AddPackage"),
		InstallPackage: func(ctx context.Context, name string, progress func(string)) error {
			journal.hit("InstallPackage:" + name)
			progress("progress install")
			return ctx.Err()
		},
		SetProfile:      V("SetProfile"),
		SetCompileFlags: V("SetCompileFlags"),
		SearchPackages: func(context.Context, string) ([]deps.PackageEntry, error) {
			journal.hit("SearchPackages")
			return []deps.PackageEntry{{Name: "fmt", Version: "11.2.0", Provider: "vcpkg", Source: "current"}}, nil
		},
		RemovePackage:     V("RemovePackage"),
		SetPackageVersion: func(name, value string) error { journal.hit("SetPackageVersion:" + name + "@" + value); return nil },
		SetCompiler:       V("SetCompiler"),
		SetWSLCompiler:    func(dist, path string) error { journal.hit("SetWSLCompiler"); return nil },
		SetEnvironment:    V("SetEnvironment"),
		SetCUDA:           func(enabled bool, root string) error { journal.hit("SetCUDA"); return nil },
		SetCUDAConnection: func(enabled bool, root, execution, dist string) error { journal.hit("SetCUDAConnection"); return nil },
		SetVulkan:         func(enabled bool, root, execution, dist string) error { journal.hit("SetVulkan"); return nil },
		SetTestGroup:      func(name, value string) error { journal.hit("SetTestGroup"); return nil },
		RunTests: func(ctx context.Context, selection TestSelection, progress func(string)) error {
			journal.hit("RunTests")
			progress("progress tests")
			return ctx.Err()
		},
		ImportCMake:       C("ImportCMake", 1),
		ImportXmake:       C("ImportXmake", 1),
		SetLanguageFlags:  func(language, value string) error { journal.hit("SetLanguageFlags:" + language); return nil },
		ApplyPreset:       V("ApplyPreset"),
		SavePreset:        V("SavePreset"),
		SetDefaultTargets: func(targets []string) error { journal.hit("SetDefaultTargets"); return nil },
		SetProjectSetting: func(key, value string) error { journal.hit("SetProjectSetting:" + key); return nil },
		DeletePreset:      V("DeletePreset"),
		ConfigureRelease: func(optimization string, targets []string, output string) error {
			journal.hit("ConfigureRelease")
			return nil
		},
		Release: C("Release", 1),
		Doctor:  C("Doctor", 1),
	}
}

// configPath writes a real trestle.toml the probe commands can read back.
func configPath(t *testing.T, project config.Config) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trestle.toml")
	buffer := &strings.Builder{}
	encoder := toml.NewEncoder(buffer)
	if err := encoder.Encode(project); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(buffer.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// freshConsole primes a wide, calm dashboard with every screen populated.
func freshConsole(t *testing.T) (dashboardModel, *callJournal) {
	t.Helper()
	journal := &callJournal{}
	model := newDashboard("trestle.toml", journalServices(journal))
	model.width, model.height, model.probing = 140, 42, false
	model.discovering, model.wslDiscovering = false, false
	model.config = config.Default("demo")
	model.config.Tasks = map[string]config.Task{"prepare": {Command: []string{"go", "env"}, Set: map[string]string{"build.profile": "release"}}}
	model.config.Targets = map[string]config.Target{
		"app":  {Type: "executable", Sources: []string{"src/main.cpp"}},
		"unit": {Type: "test", Sources: []string{"tests/unit.cpp"}, TestGroup: "smoke", Dependencies: []config.Dependency{{Target: "app"}}},
	}
	model.config.Packages = map[string]config.Package{"fmt": {Version: "11.2.0", Provider: "vcpkg"}}
	model.config.CompilerPresets = map[string]config.CompilerPreset{"fast": {CXX: "clang++", CompileFlags: []string{"-O2"}}}
	model.baseConfig = model.config
	model.projectFingerprint = "fp"
	model.probe = probeMessage{
		ProjectName: "demo", Targets: 2, Sources: 3, Ninja: true, Vcpkg: true, VcpkgRoot: "C:/vcpkg",
		CMake: true, CMakeProject: true, Xmake: true, XmakeProject: true,
		Statuses: map[string]policy.Status{"app": {}, "unit": {}},
		Vulkan:   true,
		Tests:    []testJob{{Name: "unit", Group: "smoke", Files: []string{"tests/unit.cpp"}}},
		Components: []toolchain.Component{
			{Name: "clang++", Family: "Clang", Path: "/usr/bin/clang++", Ready: true},
			{Name: "MSVC", Family: "MSVC", Path: "cl.exe", Ready: true, Execution: "native"},
			{Name: "CUDA 12", Family: "CUDA", Detail: "v12.4", Execution: "native", Ready: true},
			{Name: "Vulkan SDK", Family: "Vulkan", Detail: "1.3", Execution: "native", Ready: true},
			{Name: "Ubuntu clang", Family: "WSL", Path: "/usr/bin/clang", Execution: "wsl", Distribution: "Ubuntu", Ready: true},
		},
	}
	model.setMessage("Ready", false)
	return model, journal
}

func quietConsole(model dashboardModel) dashboardModel {
	model.services = Services{}
	return model
}

var _ = color.White // color import guard until the background tests land

var _ = lipgloss.NewStyle // lipgloss import guard until the view tests land

var _ = runlog.List // runlog import guard until the history tests land

// Batches deliver their events in order and stop at the first done answer.
func TestUpdateBatchesSettle(t *testing.T) {
	model, _ := freshConsole(t)

	updated, cmd := model.Update(workflowBatch{
		workflowEvent{label: "Build", line: "one"},
		workflowEvent{label: "Build", line: "two"},
		workflowEvent{label: "Build", done: true},
		workflowEvent{label: "Build", line: "arrival-after-done"},
	})
	fed := updated.(dashboardModel)
	if len(fed.workflowLog) != 2 || fed.batchingLogs {
		t.Fatalf("workflow batch processed %d events, batching=%v", len(fed.workflowLog), fed.batchingLogs)
	}
	if cmd == nil {
		t.Fatal("a batch containing events must recycle the wait command")
	}

	updated, _ = model.Update(installBatch{
		installEvent{name: "fmt", line: "l1"},
		installEvent{name: "fmt", line: "l2"},
		installEvent{name: "fmt", done: true},
	})
	fed = updated.(dashboardModel)
	if len(fed.installLog) != 2 || fed.mutating {
		t.Fatalf("install batch wasted events or state: lines=%d mutating=%v", len(fed.installLog), fed.mutating)
	}
	updated, _ = model.Update(installBatch{})
	if updated.(dashboardModel).batchingLogs {
		t.Fatal("an empty batch must not claim to keep batching")
	}
	updated, _ = model.Update(workflowBatch{})
	if updated.(dashboardModel).batchingLogs {
		t.Fatal("an empty workflow batch must not claim to keep batching")
	}
}

// The window resizes and the terminal background repaints the palette.
func TestUpdateWindowAndPalette(t *testing.T) {
	model, _ := freshConsole(t)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	if fed := updated.(dashboardModel); fed.width != 90 || fed.height != 24 {
		t.Fatal("window size ignored")
	}
	updated, _ = model.Update(tea.BackgroundColorMsg{})
	if updated.(dashboardModel).lightBackground {
		t.Fatal("a missing background must stay dark")
	}
	powderWhite := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	updated, _ = model.Update(tea.BackgroundColorMsg{Color: powderWhite})
	if !updated.(dashboardModel).lightBackground {
		t.Fatal("a light terminal must switch to the light palette")
	}
}

// Idle pulses advance the spinner only; busy ones keep kicking.
func TestUpdateSpinnerKeepsClock(t *testing.T) {
	model, _ := freshConsole(t)
	before := model.pulse
	updated, pulse := model.Update(pulseMessage(time.Now()))
	if pulse != nil || updated.(dashboardModel).pulse != before+1 {
		t.Fatal("an idle pulse must only advance the spinner frame")
	}
	model.mutating, model.operation = true, "Build"
	updated, pulse = model.Update(pulseMessage(time.Now()))
	if pulse == nil {
		t.Fatal("a busy model must keep the spinner alive")
	}
}

// Inventory answers merge into the visible component list.
func TestUpdateInventoriesMerge(t *testing.T) {
	model, _ := freshConsole(t)
	updated, _ := model.Update(inventoryMessage{components: []toolchain.Component{
		{Name: "WSL clang", Family: "WSL", Execution: "wsl", Ready: true},
		{Name: "clang++", Family: "Clang", Ready: true},
		{Name: "vulkan", Family: "Vulkan", Ready: false},
	}})
	model = updated.(dashboardModel)
	fed := updated.(dashboardModel)
	// The native inventory replaces everything, then carries over only the WSL
	// rows the previous inventory had already found.
	if fed.discovering || len(fed.probe.Components) != 4 || fed.probe.Vulkan {
		t.Fatalf("native inventory merge wrong: %+v", fed.probe.Components)
	}
	updated, _ = model.Update(wslInventoryMessage{components: []toolchain.Component{
		{Name: "Ubuntu clang", Family: "WSL", Execution: "wsl", Ready: true},
		{Name: "vulkan", Family: "Vulkan", Ready: true},
	}})
	fed = updated.(dashboardModel)
	// The WSL inventory keeps every natively detected row and appends its own.
	if fed.wslDiscovering || len(fed.probe.Components) != 4 {
		t.Fatalf("wsl inventory merge wrong: %+v", fed.probe.Components)
	}
	foundWsl, readyVulkan := false, false
	for _, component := range fed.probe.Components {
		if component.Execution == "wsl" {
			foundWsl = true
		}
		if component.Family == "Vulkan" && component.Ready {
			readyVulkan = true
		}
	}
	if !foundWsl || !readyVulkan {
		t.Fatal("the WSL rows must survive both merges")
	}
}

// Assessments apply matching fingerprints and drop stale ones.
func TestUpdateAssessmentRounds(t *testing.T) {
	model, _ := freshConsole(t)
	updated, _ := model.Update(assessmentMessage{fingerprint: "stale"})
	if updated.(dashboardModel).assessing {
		t.Fatal("a stale assessment must be ignored")
	}
	statuses := map[string]policy.Status{"app": {}}
	cfg := config.Default("demo")
	cfg.SchemaVersion = 2
	report := policy.Report{Rules: []policy.RuleResult{{Index: 1, When: "true", Matched: true}}}
	updated, _ = model.Update(assessmentMessage{fingerprint: "fp", cfg: cfg, statuses: statuses, rules: report})
	fed := updated.(dashboardModel)
	if fed.assessing || fed.config.SchemaVersion != 2 {
		t.Fatalf("assessment not applied: schema=%d", fed.config.SchemaVersion)
	}
	updated, _ = model.Update(assessmentMessage{fingerprint: "fp", err: errors.New("assess boom")})
	if fed := updated.(dashboardModel); !fed.messageError {
		t.Fatal("an assessment error must reach the status line")
	}
}

// The probe command really reads the on-disk config; a changed fingerprint
// schedules assessment on the loop.
func TestUpdateProbeSettlesAgainstDisk(t *testing.T) {
	path := configPath(t, config.Default("demo"))
	model, journal := freshConsole(t)
	model.path = path
	probeMsg, failure := cmdSync(t, model.probeCommand())
	if failure != nil {
		t.Fatalf("probe command failed: %v", failure)
	}
	probe, isProbe := probeMsg.(probeMessage)
	if !isProbe || probe.Error != nil || probe.ProjectName == "" || probe.Targets != 1 {
		t.Fatalf("probe should decode the fixture config: %#v", probeMsg)
	}
	model.assessing = true
	updated, assess := model.Update(probeMessage{Fingerprint: "fp2", Config: config.Default("demo"), BaseConfig: config.Default("demo"), ProjectName: "demo", Error: errors.New("bad toml")})
	if assess != nil || !updated.(dashboardModel).messageError {
		t.Fatal("a failed probe must surface its error without assessing")
	}
	updated, assess = model.Update(probeMessage{Fingerprint: "fp3", Config: config.Default("demo"), BaseConfig: config.Default("demo")})
	if assess == nil || !updated.(dashboardModel).assessing {
		t.Fatal("a changed probe must trigger assessment")
	}
	fed := feed(t, updated.(dashboardModel), assess, 4)
	if !journal.has("AssessTargets") {
		t.Fatal("assessment ran through the feedback loop")
	}
	if fed.assessing && fed.message != "" {
		t.Fatalf("assessment closure did not settle cleanly: %q", fed.message)
	}
}

// Search messages land the page on the right level and cursor.
func TestUpdatePackageSearchLanding(t *testing.T) {
	model, _ := freshConsole(t)
	updated, _ := model.Update(packageSearchMessage{err: errors.New("offline")})
	fed := updated.(dashboardModel)
	if !fed.messageError || fed.searching {
		t.Fatal("a search error must reach the status line")
	}
	updated, _ = model.Update(packageSearchMessage{})
	if fed := updated.(dashboardModel); fed.searching || fed.message != "No matching packages found" {
		t.Fatalf("empty search message = %q", fed.message)
	}
	model.config.Packages = map[string]config.Package{"existing": {}}
	updated, _ = model.Update(packageSearchMessage{ports: []deps.PackageEntry{{Name: "fmt", Version: "11.2.0", Provider: "vcpkg", Source: "current"}}})
	fed = updated.(dashboardModel)
	if fed.packageLevel != packageListLevel || fed.cursor != 1 {
		t.Fatalf("search results landed at cursor %d level %d", fed.cursor, fed.packageLevel)
	}
	model.packageRequested = "9.0.0"
	updated, _ = model.Update(packageSearchMessage{ports: []deps.PackageEntry{{Name: "fmt", Version: "9.0.0", Provider: "conan"}}})
	if fed := updated.(dashboardModel); fed.packageLevel != packageCandidatesLevel {
		t.Fatal("a name:version pin should enter the version picker")
	}
}

// Mutations settle through the event loop and failed ones reach the run log.
func TestUpdateMutationOutcomes(t *testing.T) {
	path := configPath(t, config.Default("demo"))
	model, _ := freshConsole(t)
	model.path = path

	started, cmd := model.startMutation("Palette flip", "flipped", func() error { return nil })
	busy := started.(dashboardModel)
	if !busy.mutating {
		t.Fatal("mutation must mark the model busy")
	}
	fed := busy
	var probe tea.Cmd
	for _, message := range fire(t, cmd) {
		if _, isPulse := message.(pulseMessage); isPulse {
			continue
		}
		updated, inner := busy.Update(message)
		fed = updated.(dashboardModel)
		probe = inner
	}
	if fed.mutating || !strings.Contains(fed.message, "flipped") {
		t.Fatalf("mutation outcome lost: %q", fed.message)
	}
	if probe == nil {
		t.Fatal("success must trigger a re-probe")
	}

	brokenStarted, brokenCmd := model.startMutation("Risky change", "never", func() error { return errors.New("gone wrong") })
	_ = brokenStarted
	broken := model
	for _, message := range fire(t, brokenCmd) {
		if _, isPulse := message.(pulseMessage); isPulse {
			continue
		}
		next, _ := broken.Update(message)
		broken = next.(dashboardModel)
		fed = broken
	}
	if !fed.messageError || fed.lastError == nil {
		t.Fatal("mutation failure not recorded")
	}
	records, err := runlog.List(path)
	if err != nil || len(records) == 0 || records[0].Status != "failed" {
		t.Fatalf("mutation failure must join the run history: %v", err)
	}
}

// Mouse clicks and wheels land in the panel they touch, including overlays.
func TestUpdateMouseTargets(t *testing.T) {
	model, _ := freshConsole(t)
	model.route, model.workflowRoute = BuildRoute, BuildRoute
	model.workflowLog = []string{"line 1", "line 2", "line 3"}

	next, _ := model.Update(tea.MouseClickMsg(tea.Mouse{X: 20, Y: 2 + model.mainHeight() - 1, Button: tea.MouseLeft}))
	model = next.(dashboardModel)
	if model.focus != focusLog {
		t.Fatal("a click inside the log area must focus the log")
	}
	next, _ = model.Update(tea.MouseClickMsg(tea.Mouse{X: 20, Y: 2, Button: tea.MouseLeft}))
	model = next.(dashboardModel)
	if model.focus != focusContent {
		t.Fatal("a click inside the page must focus content")
	}
	before := model.focus
	next, _ = model.Update(tea.MouseClickMsg(tea.Mouse{Button: tea.MouseRight}))
	model = next.(dashboardModel)
	if model.focus != before {
		t.Fatal("a wrong-button click must not change focus")
	}

	before = model.focus
	next, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = next.(dashboardModel)
	if model.focus == focusNavigation || before != model.focus {
		t.Fatal("wheels must scroll but stay clear of navigation")
	}
	next, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelLeft})
	model = next.(dashboardModel)
	if model.focus == focusNavigation || before != model.focus {
		t.Fatal("unknown wheel buttons must not move focus")
	}

	overlayConsole, _ := freshConsole(t)
	overlayConsole.help = true
	next, _ = overlayConsole.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if fed := next.(dashboardModel); fed.overlayScroll != 3 {
		t.Fatalf("wheel on an overlay scrolled by %d", fed.overlayScroll)
	}
	overlayDetails, _ := freshConsole(t)
	overlayDetails.details, overlayDetails.detailText = true, strings.Repeat("text line\n", 30)
	next, _ = overlayDetails.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if next.(dashboardModel).overlayScroll != 0 {
		t.Fatal("overlay scroll must clamp at zero")
	}
}

// feedableOne applies a single event directly to the model, without running the
// command that comes back (used by hand-built event closures).
func feedableOne(t *testing.T, model dashboardModel, event tea.Msg) dashboardModel {
	t.Helper()
	updated, _ := model.Update(event)
	next, ok := updated.(dashboardModel)
	if !ok {
		t.Fatalf("update returned %T", updated)
	}
	return next
}

// keySweep runs every common key on the given screen so navigation, focus,
// overlays and dead keys all travel the real handler without surprises.
func keySweep(t *testing.T, model dashboardModel) dashboardModel {
	t.Helper()
	for _, spec := range []string{
		"?", "tab", "shift+tab", "left", "right", "h", "l", "enter",
		"up", "down", "k", "j", "pgup", "pgdown", "home", "end", "G",
		"b", "r", "ctrl+r", "p", "a", "n", "i", "f", "/", "s", "d", "v",
		"e", "x", "o", "c", "u", "g", "m", "t", "y", "0", "1", "7",
		"q", "esc", "ctrl+c", "backspace", "f2", "f3", "f4", "f5",
	} {
		next, _ := tap(model, spec)
		model = next
		model.View() // every middle state must stay renderable
	}
	return model
}

func TestKeySweepSurvivesEveryScreen(t *testing.T) {
	for _, route := range routes {
		t.Run(string(route), func(t *testing.T) {
			model, _ := freshConsole(t)
			model.route = route
			model = keySweep(t, model)
			// A second sweep with the palettes flipped, so both colour modes
			// stay renderable through the same journeys.
			light, _ := freshConsole(t)
			light.route = route
			light.lightBackground = true
			keySweep(t, light)
			_ = model
		})
	}
}

// With all services silent, every screen must still answer keys gracefully.
func TestKeySweepWithoutServices(t *testing.T) {
	base, _ := freshConsole(t)
	for _, route := range routes {
		idle := quietConsole(base)
		idle.route = route
		idle = keySweep(t, idle)
		if idle.message == "Reading project configuration…" {
			t.Fatal("the initial status line leaked into a calm sweep")
		}
	}
}

// Every entry input kind reaches its service with the typed value.
func TestHandleInputKitchenSink(t *testing.T) {
	cases := []struct {
		kind, text, pkg     string
		expectService       string
		expectMessage       string
		feedInstall         bool
		requiresEmptyIsFine bool
	}{
		{kind: "package", text: "curl", expectService: "AddPackage:curl", expectMessage: "Added curl"},
		{kind: "install", text: "curl", expectService: "InstallPackage:curl", expectMessage: "Installed and added curl", feedInstall: true},
		{kind: "features", text: "http2 ssl", pkg: "zlib", expectService: "InstallPackage:zlib[http2,ssl]", expectMessage: "Installed and added zlib[http2,ssl]", feedInstall: true},
		{kind: "flags", text: "-O2", expectService: "SetCompileFlags:-O2", expectMessage: "Compiler flags updated"},
		{kind: "c-flags", text: "-g0", expectService: "SetLanguageFlags:c", expectMessage: "C flags updated"},
		{kind: "cxx-flags", text: "-std=c++23", expectService: "SetLanguageFlags:cxx", expectMessage: "CXX flags updated"},
		{kind: "link-flags", text: "/DEBUG", expectService: "SetLanguageFlags:link", expectMessage: "LINK flags updated"},
		{kind: "apply-preset", text: "fast", expectService: "ApplyPreset:fast", expectMessage: "Applied preset fast"},
		{kind: "save-preset", text: "lean", expectService: "SavePreset:lean", expectMessage: "Saved preset lean"},
		{kind: "setting", text: "demo2", pkg: "project.name", expectService: "SetProjectSetting:project.name", expectMessage: "Updated project.name"},
		{kind: "release-output", text: "dist/out.zip", expectService: "ConfigureRelease", expectMessage: "Release output updated"},
		{kind: "release-targets", text: "app,gui", expectService: "ConfigureRelease", expectMessage: "Release targets updated"},
		{kind: "environment", text: "setup.cmd", expectService: "SetEnvironment:setup.cmd", expectMessage: "Build environment updated"},
		{kind: "cuda", text: "C:/cuda", expectService: "SetCUDA", expectMessage: "CUDA enabled"},
		{kind: "cuda-wsl", text: "wsl-cuda", expectService: "SetCUDAConnection", expectMessage: "WSL CUDA enabled"},
		{kind: "test-group", text: "smoke", pkg: "unit", expectService: "SetTestGroup", expectMessage: "Moved unit to group smoke"},
		{kind: "remove", text: "curl", expectService: "RemovePackage:curl", expectMessage: "Removed curl"},
		{kind: "version", text: "fmt@11.0.2", expectService: "SetPackageVersion:fmt@11.0.2", expectMessage: "Updated fmt to 11.0.2"},
	}
	for _, item := range cases {
		t.Run(item.kind, func(t *testing.T) {
			model, journal := freshConsole(t)
			model.probing = false
			model.openInput(item.kind, "fixture prompt")
			model.inputPackage = item.pkg
			model.inputText = item.text
			updated, cmd := tap(model, "enter")
			if item.feedInstall {
				fed := feed(t, updated, cmd, 6)
				if !journal.has(item.expectService) {
					t.Fatalf("%s never called the service", item.kind)
				}
				// The real chain settles into a background probe which
				// overwrites the banner, so replay the done event directly.
				name := item.text
				if item.kind == "features" {
					name = item.pkg + "[http2,ssl]"
				}
				settled := feedableOne(t, fed, installEvent{name: name, done: true})
				if !strings.Contains(settled.message, item.expectMessage) {
					t.Fatalf("%s finish message = %q", item.kind, settled.message)
				}
				return
			}
			busy := updated
			if !busy.mutating {
				t.Fatalf("%s did not mutate", item.kind)
			}
			// One hop lands exactly on the mutation outcome; a second hop
			// already runs the settle probe.
			fed := feed(t, busy, cmd, 1)
			if !journal.has(item.expectService) {
				t.Fatalf("%s never called the service", item.kind)
			}
			if !strings.Contains(fed.message, item.expectMessage) {
				t.Fatalf("%s outcome message = %q", item.kind, fed.message)
			}
			if fed.inputMode || fed.inputText != "" {
				t.Fatalf("%s left the input open: mode=%v", item.kind, fed.inputMode)
			}
		})
	}
}

// Empty boxes refuse non-edit flows and keep the old value; esc editing keys
// work the same in every input.
func TestHandleInputCornerCases(t *testing.T) {
	model, journal := freshConsole(t)
	model.probing = false

	// Empty text without an exempt kind refuses the change.
	model.openInput("flags", "Set compiler flags")
	updated, cmd := tap(model, "enter")
	if cmd != nil || updated.message != "Nothing changed" {
		t.Fatalf("empty flags input produced %+v", updated)
	}

	// The setting kind tolerates an empty value (it can clear a field) and
	// still calls the service.
	model.openInput("setting", "Edit project name")
	model.inputPackage = "project.name"
	updated2, cmd2 := tap(model, "enter")
	fed := feed(t, updated2, cmd2, 1)
	if !strings.Contains(fed.message, "Updated project.name") || !journal.has("SetProjectSetting:project.name") {
		t.Fatalf("empty setting edit went wrong: %q journal=%v", fed.message, journal.calls)
	}

	// bad name@version inputs are refused with a hint.
	model.openInput("version", "Set package version")
	model.inputText = "bad"
	badVersion, versionCmd := tap(model, "enter")
	if versionCmd != nil || !strings.Contains(badVersion.message, "name@version") {
		t.Fatalf("version input hint = %q", badVersion.message)
	}

	// esc cancels; typing works; ctrl+u empties; ctrl+w removes a word.
	editor, _ := freshConsole(t)
	editor.probing = false
	editor.openInput("package", "Add package")
	title := editor
	title, _ = tap(title, "f")
	title, _ = tap(title, "m")
	title, _ = tap(title, "t")
	if title.inputText != "fmt" {
		t.Fatalf("typed text = %q", title.inputText)
	}
	title, _ = tap(title, "ctrl+w")
	if title.inputText == "fmt" {
		t.Fatal("ctrl+w must cut a word")
	}
	title, _ = tap(title, "ctrl+u")
	if title.inputText != "" {
		t.Fatalf("ctrl+u left %q", title.inputText)
	}
	cancelled, escCmd := tap(title, "esc")
	if escCmd != nil || cancelled.inputMode || cancelled.message != "Cancelled" {
		t.Fatalf("esc behavior wrong: mode=%v message=%q", cancelled.inputMode, cancelled.message)
	}
}

// The settings search input keeps the query live while typing and lands on the
// query results on enter; log search uses the log rows.
func TestHandleInputSearchKinds(t *testing.T) {
	model, _ := freshConsole(t)
	model.probing = false
	model.route = SettingsRoute
	model.openInput("settings-search", "Search keys, labels and values")
	updated, _ := tap(model, "d")
	if updated.settingsQuery != "d" {
		t.Fatalf("live query = %q", updated.settingsQuery)
	}
	searching, _ := tap(updated, "enter")
	if searching.inputMode || searching.settingsQuery != "d" || searching.settingsCategory != "" {
		t.Fatalf("settings search did not commit: %+v", searching)
	}

	logging := nextState(t, freshCalmConsole(t), "/")
	if !logging.inputMode || logging.inputKind != "log-search" {
		t.Fatalf("log search did not open: %#v", logging)
	}
	logging, _ = tap(logging, "n")
	logging, _ = tap(logging, "o")
	logging, _ = tap(logging, "enter")
	if !strings.Contains(logging.message, "No more matches") {
		t.Fatalf("log search on empty log = %q", logging.message)
	}

	// "s" opens the package search from any route; a "name:version" pins it.
	searchBase, _ := freshConsole(t)
	searcher := nextState(t, searchBase, "s")
	if !searcher.inputMode || searcher.inputKind != "search" {
		t.Fatalf("package search did not open: %#v", searcher)
	}
	searcher.inputText = "fmt:11.2.0"
	committed, cmd := tap(searcher, "enter")
	if !committed.searching || committed.packageRequested != "11.2.0" {
		t.Fatalf("search did not arm: searching=%v req=%q", committed.searching, committed.packageRequested)
	}
	found := feed(t, committed, cmd, 4)
	if len(found.packagePackages) == 0 || found.searching {
		t.Fatalf("search results missing: %+v", found.packagePackages)
	}
}

func freshCalmConsole(t *testing.T) dashboardModel {
	t.Helper()
	model, _ := freshConsole(t)
	model.route = BuildRoute
	model.focus = focusLog
	return quietConsole(model)
}

// Install streams collect their lines, finish, and hand control back cleanly.
func TestInstallStreamRunsToDone(t *testing.T) {
	model, journal := freshConsole(t)
	model.route = PackagesRoute
	model.packagePackages = []deps.Package{{Name: "fmt", Candidates: []deps.Candidate{{Version: "11.2.0", Provider: "vcpkg"}}}}
	model.cursor = len(model.config.Packages)

	updated, cmd := tap(model, "i")
	busy := updated
	if !busy.mutating || busy.installCancel == nil || !strings.Contains(busy.message, "Preparing install for fmt") {
		t.Fatalf("install did not arm itself: %q", busy.message)
	}
	fed := feed(t, busy, cmd, 6)
	if !journal.has("InstallPackage:fmt") {
		t.Fatal("the installer service ran")
	}
	finish := feedableOne(t, fed, installEvent{name: "fmt", done: true})
	if !strings.Contains(finish.message, "Installed and added fmt") {
		t.Fatalf("install finish message = %q", finish.message)
	}
	if len(fed.installLog) == 0 || !strings.Contains(strings.Join(fed.installLog, "\n"), "progress install") {
		t.Fatalf("install lines missing: %q", strings.Join(fed.installLog, "|"))
	}

	// A failing installer reports through the status line, then releases the
	// busy state so the console stays interactive.
	broken, _ := freshConsole(t)
	broken.route = PackagesRoute
	broken.packagePackages = []deps.Package{{Name: "fmt", Candidates: []deps.Candidate{{Version: "11.2.0", Provider: "vcpkg"}}}}
	broken.cursor = len(broken.config.Packages)
	broken.services.InstallPackage = func(_ context.Context, _ string, _ func(string)) error { return errors.New("network down") }
	updated, cmd = tap(broken, "i")
	fed = feed(t, updated, cmd, 6)
	if !strings.Contains(fed.message, "Install failed") || !fed.messageError {
		t.Fatalf("install failure hidden: %q", fed.message)
	}
	if fed.mutating {
		t.Fatal("failure must release the busy state")
	}
}

// The esc key cancels an installation in flight; closing events honor the
// quit-after-task handshake only when asked.
func TestInstallCancelAndQuitHandshake(t *testing.T) {
	released := make(chan struct{}, 1)
	model, _ := freshConsole(t)
	model.route = PackagesRoute
	model.packagePackages = []deps.Package{{Name: "fmt", Candidates: []deps.Candidate{{Version: "11.2.0", Provider: "vcpkg"}}}}
	model.cursor = len(model.config.Packages)
	model.services.InstallPackage = func(ctx context.Context, _ string, _ func(string)) error {
		<-ctx.Done()
		released <- struct{}{}
		return context.Canceled
	}
	armed, cmd := tap(model, "i")
	busy := armed
	if busy.installCancel == nil {
		t.Fatal("install must open cancellation")
	}
	// The installer service only starts inside the command; run it in the
	// background so esc can reach the cancellation context.
	go func() { _ = fire(t, cmd) }()
	fed, cancelCmd := tap(busy, "esc")
	if cancelCmd != nil {
		t.Fatal("cancelling stays inside the dashboard")
	}
	if !strings.Contains(fed.message, "Cancelling installation") {
		t.Fatalf("cancel message = %q", fed.message)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the installer goroutine was not released by esc")
	}

	// A done answer after the cancel closes the install bookkeeping without
	// flipping back to busy.
	next := feedableOne(t, fed, installEvent{name: "fmt", done: true, err: context.Canceled})
	if next.mutating || next.installEvents != nil {
		t.Fatal("closing events must clear the installation state")
	}
}

// Workflow streams run builds and mirror the install pipeline for diagnosis,
// cancellation and the quit handshake.
func TestWorkflowStreamRunsToDone(t *testing.T) {
	model, journal := freshConsole(t)

	updated, cmd := tap(model, "b")
	busy := updated
	if !busy.mutating || !strings.Contains(busy.message, "Build") {
		t.Fatalf("build did not arm: %q", busy.message)
	}
	fed := feed(t, busy, cmd, 6)
	if !journal.has("Build") {
		t.Fatal("the build service ran")
	}
	// A follow-up probe after settling may overwrite the status text, so the
	// finishing transition itself is asserted on the replayed done event.
	finish := feedableOne(t, busy, workflowEvent{label: "Build", done: true})
	if !strings.Contains(finish.message, "Build completed") || finish.lastRun != "Completed just now" {
		t.Fatalf("build finish message = %q lastRun=%q", finish.message, finish.lastRun)
	}
	if len(fed.workflowLog) == 0 {
		t.Fatal("build lines were not captured")
	}

	noisy, _ := freshConsole(t)
	noisy.services.Build = func(ctx context.Context, progress func(string)) error {
		progress("error c1: broken include")
		progress("  failed: link crashed")
		progress("all done")
		return ctx.Err()
	}
	updated, cmd = tap(noisy, "b")
	fed = feed(t, updated, cmd, 6)
	if len(fed.workflowErrors) < 1 {
		t.Fatalf("diagnostic lines were not collected: %v", fed.workflowErrors)
	}

	canceled, _ := freshConsole(t)
	stopped := false
	canceled.mutating, canceled.workflowRoute, canceled.operation = true, BuildRoute, "Build"
	canceled.workflowCancel = func() { stopped = true }
	stepped, cancelCmd := tap(canceled, "esc")
	if cancelCmd != nil || !stopped {
		t.Fatal("esc must cancel the running operation in place")
	}
	if !strings.Contains(stepped.message, "Cancelling") {
		t.Fatalf("cancel status = %q", stepped.message)
	}
	afterCancel := feedableOne(t, stepped, workflowEvent{label: "Build", done: true, err: context.Canceled})
	if !strings.Contains(afterCancel.message, "Build cancelled") {
		t.Fatalf("cancel closure message = %q", afterCancel.message)
	}

	timedOut, _ := freshConsole(t)
	timedOut.mutating = true
	timedOut.workflowCancel = func() {}
	afterTimeout := feedableOne(t, timedOut, workflowEvent{label: "Build", done: true, err: context.DeadlineExceeded})
	if !strings.Contains(afterTimeout.message, "timed out") {
		t.Fatalf("timeout closure message = %q", afterTimeout.message)
	}

	failed, _ := freshConsole(t)
	failed.mutating = true
	failed.workflowCancel = func() {}
	afterFailure := feedableOne(t, failed, workflowEvent{label: "Build", done: true, err: errors.New("kaboom")})
	if !strings.Contains(afterFailure.message, "Build failed") {
		t.Fatalf("failure closure message = %q", afterFailure.message)
	}

	quitting, _ := freshConsole(t)
	quitting.mutating = true
	quitting.quitAfterTask = true
	quitting.workflowCancel = func() {}
	_, quitCmd := quitting.Update(workflowEvent{label: "Build", done: true})
	if quitCmd == nil {
		t.Fatal("the finished operation must honor the quit handshake")
	}
}
