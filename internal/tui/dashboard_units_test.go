package tui

import (
	"slices"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/deps"
	"trestle/internal/toolchain"
)

// Selection helpers refuse out-of-range cursors and report sorted names.
func TestSelectedTargetPresetAndLists(t *testing.T) {
	model, _ := freshConsole(t)
	if name, ok := model.selectedTarget(); !ok || name != "app" {
		t.Fatalf("cursor 0 target = %q ok=%v", name, ok)
	}
	model.cursor = -1
	if _, ok := model.selectedTarget(); ok {
		t.Fatal("negative cursor still selects a target")
	}
	model.cursor = 99
	if _, ok := model.selectedTarget(); ok {
		t.Fatal("overflow cursor still selects a target")
	}
	model.cursor = 0
	if task, ok := model.selectedTask(); !ok || task != "prepare" {
		t.Fatalf("cursor 0 task = %q ok=%v", task, ok)
	}
	if preset, ok := model.selectedPreset(); !ok || preset != "fast" {
		t.Fatalf("cursor 0 preset = %q ok=%v", preset, ok)
	}
	names := model.optimizationNames()
	want := []string{"balanced", "speed", "size", "custom:fast"}
	if !slices.Equal(names, want) {
		t.Fatalf("optimization names = %q", names)
	}
}

// splitList keeps separator semantics for the release targets editor.
func TestSplitListShapes(t *testing.T) {
	got := splitList("a; b ,c,,d")
	want := []string{"a", "b", "c", "d"}
	if !slices.Equal(got, want) {
		t.Fatalf("splitList = %q", got)
	}
	if items := splitList(" , ; "); len(items) != 0 {
		t.Fatalf("empty splitList = %q", items)
	}
}

// Release optimization commits through the mutation channel.
func TestSelectReleaseOptimization(t *testing.T) {
	model, journal := freshConsole(t)
	model.cursor = 1
	armed, cmd := model.selectReleaseOptimization()
	busy := armed.(dashboardModel)
	if !busy.mutating || busy.operation != "Selecting release optimization" {
		t.Fatalf("optimization did not arm: %q", busy.operation)
	}
	if cmd == nil {
		t.Fatal("optimization without a command")
	}
	fed := feed(t, busy, cmd, 1)
	if !journal.has("ConfigureRelease") || !strings.Contains(fed.message, "Release optimization: speed") {
		t.Fatalf("optimization settle: message=%q", fed.message)
	}
	empty := dashboardModel{config: config.Default("demo"), width: 120, height: 40}
	outside := empty
	outside.cursor = -1
	refused, refuseCmd := outside.selectReleaseOptimization()
	if refuseCmd != nil || refused.(dashboardModel).mutating {
		t.Fatal("cursor outside the list must not arm a mutation")
	}
}

// Release runs through the workflow stream; quiet services refuse politely.
func TestStartReleaseAndBuild(t *testing.T) {
	model, journal := freshConsole(t)
	armed, cmd := model.startRelease()
	busy := armed.(dashboardModel)
	if !busy.mutating || busy.operation != "Release" || cmd == nil {
		t.Fatalf("release did not arm: %q", busy.operation)
	}
	fed := feed(t, busy, cmd, 4)
	if !journal.has("Release") {
		t.Fatal("the release service never ran")
	}
	if !fed.hasLog() {
		t.Fatal("release log missing")
	}

	calm, _ := freshConsole(t)
	missing, cmd := quietConsole(calm).startRelease()
	if cmd != nil || !strings.Contains(missing.(dashboardModel).message, "release service is unavailable") {
		t.Fatal("quiet console must refuse the release")
	}

	build, journal2 := freshConsole(t)
	armedBuild, buildCmd := build.startSelectedTargetBuild()
	buildBusy := armedBuild.(dashboardModel)
	if !buildBusy.mutating || buildBusy.operation != "Build app" || buildCmd == nil {
		t.Fatalf("target build did not arm: %q", buildBusy.operation)
	}
	if buildBusy.route != BuildRoute || buildBusy.focus != focusLog {
		t.Fatal("target build must land on the build log panel")
	}
	fedBuild := feed(t, buildBusy, buildCmd, 4)
	_ = fedBuild
	if !journal2.has("BuildTargets") {
		t.Fatal("BuildTargets never ran")
	}
	blank := build
	blank.cursor = -1
	refused, refuseCmd := blank.startSelectedTargetBuild()
	if refuseCmd != nil || !strings.Contains(refused.(dashboardModel).message, "No target is selected") {
		t.Fatal("missing target must refuse the build")
	}
	forced, forceCmd := build.startForcedTargetBuild(targetRow{name: "app"})
	if !forced.(dashboardModel).mutating || forceCmd == nil {
		t.Fatal("force build did not arm")
	}
	fedForce := feed(t, forced.(dashboardModel), forceCmd, 4)
	_ = fedForce
	if !journal2.has("BuildTargetsForce") {
		t.Fatal("BuildTargetsForce never ran")
	}
	quietBuild := quietConsole(build)
	quietRefused, quietCmd := quietBuild.startForcedTargetBuild(targetRow{name: "app"})
	if quietCmd != nil || !strings.Contains(quietRefused.(dashboardModel).message, "Force-build service is unavailable") {
		t.Fatal("quiet console must refuse force builds")
	}
	targetless := quietConsole(build)
	targetless.services.BuildTargets = nil
	missingTargets, missingCmd := targetless.startTargetBuild([]string{"app"}, false, "Build app")
	if missingCmd != nil || !strings.Contains(missingTargets.(dashboardModel).message, "Target build service is unavailable") {
		t.Fatal("missing target build service must refuse")
	}
}

// Import starters pick Xmake or CMake and refuse on a quiet probe.
func TestImportStarters(t *testing.T) {
	model, journal := freshConsole(t)
	armed, cmd := model.startCMakeImport()
	busy := armed.(dashboardModel)
	if !busy.mutating || busy.operation != "CMake import" || cmd == nil {
		t.Fatalf("CMake import did not arm: %q", busy.operation)
	}
	fed := feed(t, busy, cmd, 4)
	_ = fed
	if !journal.has("ImportCMake") {
		t.Fatal("ImportCMake never ran")
	}

	noCMake := model
	noCMake.probe.CMakeProject = false
	refused, refuseCmd := noCMake.startCMakeImport()
	if refuseCmd != nil || !strings.Contains(refused.(dashboardModel).message, "CMakeLists.txt was not found") {
		t.Fatal("absent CMakeLists.txt must refuse")
	}

	xmake, journalX := freshConsole(t)
	xmake.probe.XmakeProject = true
	armedX, cmdX := xmake.startProjectImport()
	if !armedX.(dashboardModel).mutating || armedX.(dashboardModel).operation != "Xmake import" || cmdX == nil {
		t.Fatal("Xmake import did not arm")
	}
	feed(t, armedX.(dashboardModel), cmdX, 4)
	if !journalX.has("ImportXmake") {
		t.Fatal("ImportXmake never ran")
	}

	fallbackModel, journalF := freshConsole(t)
	fallbackModel.probe.XmakeProject = false
	armedF, cmdF := fallbackModel.startProjectImport()
	if !armedF.(dashboardModel).mutating || cmdF == nil {
		t.Fatal("fallback import did not arm")
	}
	feed(t, armedF.(dashboardModel), cmdF, 4)
	if !journalF.has("ImportCMake") {
		t.Fatal("fallback import must pick CMake")
	}
}

// Test starters select jobs or files, and refuse empty rows.
func TestTestStarters(t *testing.T) {
	model, journal := freshConsole(t)
	model.probe.Tests = []testJob{{Name: "unit", Files: []string{"tests/unit.cpp"}, Group: "smoke"}}
	armed, cmd := model.runSelectedTest()
	busy := armed.(dashboardModel)
	if !busy.mutating || busy.operation != "Test job unit" || cmd == nil {
		t.Fatalf("test job did not arm: %q", busy.operation)
	}
	fed := feed(t, busy, cmd, 4)
	_ = fed
	if !journal.has("RunTests") {
		t.Fatalf("RunTests missing: %v", journal.calls)
	}

	fileMode := model
	fileMode.testFileMode = true
	armedFile, fileCmd := fileMode.runSelectedTest()
	if armedFile.(dashboardModel).operation != "File test unit.cpp" {
		t.Fatalf("file run label = %q", armedFile.(dashboardModel).operation)
	}
	feed(t, armedFile.(dashboardModel), fileCmd, 4)
	if !journal.has("RunTests") {
		t.Fatal("file selection never ran the suite")
	}

	blank := model
	blank.cursor = -1
	refused, refuseCmd := blank.runSelectedTest()
	if refuseCmd != nil || !strings.Contains(refused.(dashboardModel).message, "No test job is selected") {
		t.Fatal("empty rows must refuse the run")
	}

	quiet := quietConsole(model)
	quietRefused, quietCmd := quiet.startTests(TestSelection{Job: "unit"}, "Run tests")
	if quietCmd != nil || !strings.Contains(quietRefused.(dashboardModel).message, "Test suite service is unavailable") {
		t.Fatal("quiet console must refuse tests")
	}
}

// SDK detection prefers the configured execution mode before falling back.
func TestDetectedSDKAndConnections(t *testing.T) {
	model := dashboardModel{config: config.Default("demo"), width: 120, height: 40}
	model.probe.Components = []toolchain.Component{
		{Name: "CUDA WSL", Family: "CUDA", Ready: true, Detail: "v12", Execution: "wsl", Distribution: "Ubuntu"},
		{Name: "CUDA host", Family: "CUDA", Ready: true, Detail: "nvcc", Execution: ""},
		{Name: "Vulkan", Family: "Vulkan", Ready: true, Detail: "1.3"},
	}

	if sdk, ok := model.detectedSDKForExecution("CUDA", "wsl"); !ok || sdk.Name != "CUDA WSL" {
		t.Fatalf("wsl sdk = %+v ok=%v", sdk, ok)
	}
	if _, ok := model.detectedSDKForExecution("CUDA", "bogus"); ok {
		t.Fatal("unknown execution must not match")
	}
	wslModel := model
	wslModel.config.Toolchain.Mode = "wsl"
	if sdk, ok := wslModel.detectedSDK("CUDA"); !ok || sdk.Name != "CUDA WSL" {
		t.Fatalf("mode-preferred sdk = %+v ok=%v", sdk, ok)
	}
	nativeModel := model
	nativeModel.config.Toolchain.Mode = "native"
	if sdk, ok := nativeModel.detectedSDK("CUDA"); !ok || sdk.Name != "CUDA host" {
		t.Fatalf("native fallback sdk = %+v ok=%v", sdk, ok)
	}
	if _, ok := model.detectedSDK("OpenCL"); ok {
		t.Fatal("unknown family must not match")
	}

	cudaJournal, cudaCalls := freshConsole(t)
	wslComponent, _ := model.detectedSDKForExecution("CUDA", "wsl")
	armed, cmd := cudaJournal.connectCUDA(wslComponent)
	fed := feed(t, armed.(dashboardModel), cmd, 1)
	if !cudaCalls.has("SetCUDAConnection") || !strings.Contains(fed.message, "CUDA connected: CUDA WSL") {
		t.Fatalf("WSL CUDA connect: %q", fed.message)
	}

	nativeJournal, nativeCalls := freshConsole(t)
	nativeJournal.services.SetCUDAConnection = nil
	nativeComponent := model.probe.Components[1]
	armedNative, cmdNative := nativeJournal.connectCUDA(nativeComponent)
	fedNative := feed(t, armedNative.(dashboardModel), cmdNative, 1)
	if !nativeCalls.has("SetCUDA") || !strings.Contains(fedNative.message, "CUDA connected: CUDA host") {
		t.Fatalf("native CUDA connect: %q", fedNative.message)
	}

	vulkanJournal, vulkanCalls := freshConsole(t)
	armedVulkan, cmdVulkan := vulkanJournal.connectVulkan(model.probe.Components[2])
	fedVulkan := feed(t, armedVulkan.(dashboardModel), cmdVulkan, 1)
	if !vulkanCalls.has("SetVulkan") || !strings.Contains(fedVulkan.message, "Vulkan SDK connected: Vulkan") {
		t.Fatalf("Vulkan connect: %q", fedVulkan.message)
	}

	missingVulkan := quietConsole(model)
	armedMissing, cmdMissing := missingVulkan.connectVulkan(model.probe.Components[2])
	fedMissing := feed(t, armedMissing.(dashboardModel), cmdMissing, 1)
	if !strings.Contains(fedMissing.message, "Vulkan service is unavailable") {
		t.Fatalf("quiet Vulkan settle = %q", fedMissing.message)
	}
	missingNative := quietConsole(model)
	missingNative.services.SetCUDAConnection = nil
	armedMissingCUDA, cmdMissingCUDA := missingNative.connectCUDA(model.probe.Components[1])
	fedMissingCUDA := feed(t, armedMissingCUDA.(dashboardModel), cmdMissingCUDA, 1)
	if !strings.Contains(fedMissingCUDA.message, "CUDA service is unavailable") {
		t.Fatalf("quiet CUDA settle = %q", fedMissingCUDA.message)
	}
}

// Diagnostics, log routing and navigation helpers behave as labeled.
func TestDiagnosticsLogRoutingAndNavigation(t *testing.T) {
	if !diagnosticLine("  failed: something") || !diagnosticLine("fatal error C1083") ||
		!diagnosticLine(" error c2065: mystery") || !diagnosticLine(" error lnk2019") ||
		!diagnosticLine("undefined reference to `main'") || !diagnosticLine("unresolved external symbol") {
		t.Fatal("diagnostic misses")
	}
	if diagnosticLine("allstations nominal") {
		t.Fatal("clean line flagged as diagnostic")
	}

	model := dashboardModel{config: config.Default("demo"), width: 140, height: 42}
	model.route = PackagesRoute
	if model.routeHasLog() {
		t.Fatal("packages without install lines still report a log")
	}
	model.installLog = []string{"one"}
	if !model.routeHasLog() {
		t.Fatal("packages ignore install lines")
	}
	model.route = BuildRoute
	model.installLog = nil
	if model.routeHasLog() {
		t.Fatal("build without workflow lines still reports a log")
	}
	model.workflowLog = []string{"one"}
	if !model.routeHasLog() {
		t.Fatal("build ignores workflow lines")
	}
	model.route = SettingsRoute
	model.workflowLog = []string{"one"}
	if model.routeHasLog() {
		t.Fatal("settings route must not anchor a log")
	}

	if navigationLabel(ImportRoute) != "Import" || navigationLabel(PresetsRoute) != "Presets" {
		t.Fatal("special navigation labels wrong")
	}
	if navigationKey(0) != "1" || navigationKey(8) != "9" || navigationKey(9) != "0" || navigationKey(10) != " " {
		t.Fatal("navigation keys wrong")
	}
	if width := sidebarWidth(60); width != 30 {
		t.Fatalf("narrow sidebar = %d", width)
	}
	if width := sidebarWidth(200); width != 34 {
		t.Fatalf("wide sidebar = %d", width)
	}
	if key := routeIndex(TasksRoute); key < 0 || routes[key] != TasksRoute {
		t.Fatal("route index lost its route")
	}
}

// candidateFeatures only offers vcpkg features for the feature editor.
func TestCandidateFeatures(t *testing.T) {
	model := dashboardModel{config: config.Default("demo"), width: 120, height: 40}
	vcpkg := deps.Package{Name: "zlib", Candidates: []deps.Candidate{
		{Provider: "vcpkg", Features: []string{"ssl"}},
		{Provider: "conan"},
	}}
	if feats := candidateFeatures(model, vcpkg); !slices.Equal(feats, []string{"ssl"}) {
		t.Fatalf("vcpkg features = %q", feats)
	}
	allConan := deps.Package{Name: "zlib", Candidates: []deps.Candidate{{Provider: "conan"}, {Provider: "conan"}}}
	if feats := candidateFeatures(model, allConan); feats != nil {
		t.Fatalf("conan-only features = %q", feats)
	}
}
