package desktoprpc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"trestle/internal/app"
)

type testPeer struct {
	in      *io.PipeWriter
	dec     *json.Decoder
	next    int
	lastSeq map[string]uint64
	output  []string
}

func connectPeer(t *testing.T) *testPeer {
	t.Helper()
	read, in := io.Pipe()
	out, write := io.Pipe()
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	go func() {
		defer write.Close()
		result <- Serve(ctx, read, write)
	}()
	t.Cleanup(func() {
		in.Close()
		out.Close()
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("RPC failed to shut down")
		}
	})
	return &testPeer{in: in, dec: json.NewDecoder(out), lastSeq: map[string]uint64{}}
}

type testFrame struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	Method string          `json:"method"`
	Params Event           `json:"params"`
}

func (peer *testPeer) read(t *testing.T) testFrame {
	t.Helper()
	var frame testFrame
	if err := peer.dec.Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Method != "" {
		if frame.Params.Version != 1 || frame.Params.Sequence <= peer.lastSeq[frame.Params.BuildID] || frame.Params.Timestamp.IsZero() {
			t.Fatalf("unordered or invalid event: %+v", frame.Params)
		}
		peer.lastSeq[frame.Params.BuildID] = frame.Params.Sequence
		if frame.Method == "event.build.output" {
			data, ok := frame.Params.Data.(map[string]any)
			if !ok {
				t.Fatal("bad output payload")
			}
			peer.output = append(peer.output, data["text"].(string))
		}
	}
	return frame
}

func (peer *testPeer) call(t *testing.T, method string, params any) testFrame {
	t.Helper()
	peer.next++
	err := json.NewEncoder(peer.in).Encode(map[string]any{"jsonrpc": "2.0", "id": peer.next, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	for {
		frame := peer.read(t)
		if frame.Method != "" {
			continue
		}
		var id int
		if err := json.Unmarshal(frame.ID, &id); err != nil || id != peer.next {
			t.Fatalf("unexpected response: %+v", frame)
		}
		return frame
	}
}

func rpcResult[T any](t *testing.T, frame testFrame) T {
	t.Helper()
	if frame.Error != nil {
		t.Fatal(frame.Error)
	}
	var value T
	if err := json.Unmarshal(frame.Result, &value); err != nil {
		t.Fatalf("decode %s: %v", frame.Result, err)
	}
	return value
}

func (peer *testPeer) handshake(t *testing.T) {
	t.Helper()
	v := rpcResult[map[string]any](t, peer.call(t, "system.handshake", map[string]any{"versions": []int{1}}))
	if v["version"] != float64(1) {
		t.Fatalf("unexpected handshake: %v", v)
	}
}

func TestNegotiationAndMalformedInput(t *testing.T) {
	peer := connectPeer(t)
	if _, err := io.WriteString(peer.in, "not-json\n"); err != nil {
		t.Fatal(err)
	}
	if frame := peer.read(t); frame.Error == nil || frame.Error.Code != -32700 {
		t.Fatalf("expected parse error: %+v", frame)
	}
	if frame := peer.call(t, "workspace.list", map[string]any{}); frame.Error == nil || frame.Error.Code != -32001 {
		t.Fatalf("accepted request before negotiation: %+v", frame)
	}
	if frame := peer.call(t, "system.handshake", map[string]any{"versions": []int{999}}); frame.Error == nil || frame.Error.Code != -32001 {
		t.Fatalf("accepted incompatible version: %+v", frame)
	}
	peer.handshake(t)
	if frame := peer.call(t, "workspace.open", []string{"bad params"}); frame.Error == nil || frame.Error.Code != -32602 {
		t.Fatalf("accepted invalid params: %+v", frame)
	}
	if frame := peer.call(t, "unsupported", map[string]any{}); frame.Error == nil || frame.Error.Code != -32601 {
		t.Fatalf("expected unknown method: %+v", frame)
	}
}

func openExample(t *testing.T, peer *testPeer) (string, app.ProjectSnapshot) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"trestle.toml", "cli/trestle.toml", "cli/src/main.cpp",
		"core/trestle.toml", "core/src/greeter.cpp", "core/include/greeter/greeter.h", "core/tests/greeter_test.cpp"} {
		source := filepath.Join("../../examples/hello-workspace", filepath.FromSlash(name))
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws := rpcResult[app.ProjectSnapshot](t, peer.call(t, "workspace.open", map[string]string{"path": dir}))
	if ws.ID == "" || len(ws.Projects) != 2 || len(ws.Targets) != 3 {
		t.Fatalf("unexpected workspace: %+v", ws)
	}
	target := rpcResult[app.ProjectTarget](t, peer.call(t, "graph.dependencies", map[string]string{"workspace_id": ws.ID, "target": "hello"}))
	if len(target.Dependencies) != 1 || target.Dependencies[0] != "greeter" {
		t.Fatalf("unexpected workspace dependency: %+v", target)
	}
	return dir, ws
}

func waitForBuild(t *testing.T, peer *testPeer, buildID string) buildState {
	t.Helper()
	for i := 0; i < 400; i++ {
		state := rpcResult[buildState](t, peer.call(t, "build.status", map[string]string{"build_id": buildID}))
		if state.Ended != nil {
			return state
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("build did not become terminal")
	return buildState{}
}

func requireNativeBuild(t *testing.T) {
	t.Helper()
	if os.Getenv("TRESTLE_TEST_NATIVE") != "1" {
		t.Skip("set TRESTLE_TEST_NATIVE=1 to run the desktop native integration")
	}
	compiler := os.Getenv("TRESTLE_TEST_COMPILER")
	if compiler == "" {
		compiler = "g++"
		if runtime.GOOS == "windows" {
			compiler = "clang-cl"
		}
	}
	for _, tool := range []string{compiler, "ninja"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildLifecycleAndReplay(t *testing.T) {
	requireNativeBuild(t)
	peer := connectPeer(t)
	peer.handshake(t)
	dir, ws := openExample(t, peer)
	if compiler := os.Getenv("TRESTLE_TEST_COMPILER"); compiler != "" {
		rpcResult[app.ProjectSnapshot](t, peer.call(t, "toolchain.select",
			map[string]string{"workspace_id": ws.ID, "compiler": compiler}))
	}
	options := map[string]any{"workspace_id": ws.ID, "member": "cli", "targets": []string{"hello"}, "profile": "debug"}
	configured := rpcResult[buildState](t, peer.call(t, "build.configure", options))
	if result := waitForBuild(t, peer, configured.ID); result.Status != "completed" {
		t.Fatalf("configure failed: %+v", result)
	}
	commands := rpcResult[generatedPage](t, peer.call(t, "build.commands", map[string]string{
		"build_id": configured.ID, "target": "hello",
	}))
	if !commands.Available || len(commands.Commands) != 2 ||
		!strings.Contains(string(commands.Commands[0].Action), "compile") ||
		!strings.Contains(string(commands.Commands[1].Action), "link") ||
		!strings.Contains(commands.Commands[1].Directory, "cli") {
		t.Fatalf("unexpected generated commands: %+v", commands)
	}
	build := rpcResult[buildState](t, peer.call(t, "build.start", options))
	if build.Status != "queued" || build.WorkspaceID != ws.ID {
		t.Fatalf("unexpected initial state: %+v", build)
	}
	result := waitForBuild(t, peer, build.ID)
	if result.Status != "completed" || result.Progress == nil || result.TimingCount == 0 || len(result.LogPaths) != 2 {
		t.Fatalf("incomplete build result: %+v", result)
	}
	log := rpcResult[struct{ Text string }](t, peer.call(t, "build.log", map[string]string{"build_id": build.ID}))
	if !strings.Contains(log.Text, "link hello") || !strings.Contains(strings.Join(peer.output, "\n"), "compile") {
		t.Fatalf("missing real build output: %s", log.Text)
	}
	type ninjaAnalysis struct {
		Invocations []app.NinjaInvocation `json:"ninja_invocations"`
	}
	analysis := func(id string) ninjaAnalysis {
		return rpcResult[ninjaAnalysis](t, peer.call(t, "analysis.summary", map[string]string{"build_id": id}))
	}
	first := analysis(build.ID)
	if len(first.Invocations) != 2 {
		t.Fatalf("missing Ninja invocations: %+v", first)
	}
	for _, invocation := range first.Invocations {
		if invocation.NoWork || invocation.CriticalPath == nil || len(invocation.CriticalPath.Actions) < 2 || len(invocation.Reasons) == 0 {
			t.Fatalf("missing dirty reasons or measured action chain: %+v", invocation)
		}
	}
	incremental := rpcResult[buildState](t, peer.call(t, "build.start", options))
	if result := waitForBuild(t, peer, incremental.ID); result.Status != "completed" || result.TimingCount != 0 {
		t.Fatalf("unexpected incremental build result: %+v", result)
	}
	incrementalAnalysis := analysis(incremental.ID)
	if len(incrementalAnalysis.Invocations) != 2 {
		t.Fatalf("missing incremental invocations: %+v", incrementalAnalysis)
	}
	for _, invocation := range incrementalAnalysis.Invocations {
		if !invocation.NoWork || invocation.CriticalPath != nil {
			t.Fatalf("expected Ninja no-work evidence: %+v", invocation)
		}
	}
	exe := filepath.Join(dir, "build", "debug", "cli", "bin", "hello-workspace")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "Hello, workspace!" {
		t.Fatalf("unexpected binary result: %s: %v", output, err)
	}
	options["rebuild"] = true
	rebuild := rpcResult[buildState](t, peer.call(t, "build.start", options))
	result = waitForBuild(t, peer, rebuild.ID)
	log = rpcResult[struct{ Text string }](t, peer.call(t, "build.log", map[string]string{"build_id": rebuild.ID}))
	if result.Status != "completed" || result.TimingCount == 0 || !strings.Contains(log.Text, "Cleaning") {
		t.Fatalf("clean rebuild failed: %+v\n%s", result, log.Text)
	}
	delete(options, "rebuild")
	if err := os.WriteFile(filepath.Join(dir, "cli", "src", "main.cpp"), []byte("#error DesktopBuildFailure\n"), 0600); err != nil {
		t.Fatal(err)
	}
	failed := rpcResult[buildState](t, peer.call(t, "build.start", options))
	result = waitForBuild(t, peer, failed.ID)
	log = rpcResult[struct{ Text string }](t, peer.call(t, "build.log", map[string]string{"build_id": failed.ID}))
	if result.Status != "failed" || result.Error == nil || !strings.Contains(log.Text, "DesktopBuildFailure") {
		t.Fatalf("compilation error not reported: %+v\n%s", result, log.Text)
	}
}

func TestCancellationStopsRunningNinja(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises Linux process-group cancellation")
	}
	requireNativeBuild(t)
	tools := t.TempDir()
	marker := filepath.Join(tools, "started")
	t.Setenv("TRESTLE_TEST_CANCEL_MARKER", marker)
	ninja := "#!/bin/sh\nprintf started > \"$TRESTLE_TEST_CANCEL_MARKER\"\nexec sleep 50\n"
	if err := os.WriteFile(filepath.Join(tools, "ninja"), []byte(ninja), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	peer := connectPeer(t)
	peer.handshake(t)
	_, ws := openExample(t, peer)
	build := rpcResult[buildState](t, peer.call(t, "build.start", map[string]string{"workspace_id": ws.ID}))
	for deadline := time.Now().Add(8 * time.Second); ; {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the build never started Ninja")
		}
		time.Sleep(25 * time.Millisecond)
	}
	busy := peer.call(t, "build.start", map[string]string{"workspace_id": ws.ID})
	if busy.Error == nil || busy.Error.Code != -32004 {
		t.Fatal("accepted a concurrent build for the same workspace")
	}
	_ = rpcResult[buildState](t, peer.call(t, "build.cancel", map[string]string{"build_id": build.ID}))
	if result := waitForBuild(t, peer, build.ID); result.Status != "cancelled" {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}
}

func TestWorkspaceRefreshAfterConfigDisappears(t *testing.T) {
	peer := connectPeer(t)
	peer.handshake(t)
	_, ws := openExample(t, peer)
	if err := os.Remove(ws.ID); err != nil {
		t.Fatal(err)
	}
	frame := peer.call(t, "workspace.get", map[string]string{"workspace_id": ws.ID})
	if frame.Error == nil || frame.Error.Message == "" {
		t.Fatalf("refresh did not report the missing configuration: %+v", frame)
	}
	if frame := peer.call(t, "system.capabilities", map[string]any{}); frame.Error != nil {
		t.Fatalf("connection stopped serving after a filesystem error: %+v", frame)
	}
}

func TestMissingNinjaReportsFailureWithoutDisconnecting(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux compiler symlinks in a private PATH")
	}
	requireNativeBuild(t)
	dir := t.TempDir()
	for _, name := range []string{"g++", "gcc", "ar", "as", "ld"} {
		actual, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actual, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	peer := connectPeer(t)
	peer.handshake(t)
	_, ws := openExample(t, peer)
	build := rpcResult[buildState](t, peer.call(t, "build.start", map[string]string{"workspace_id": ws.ID}))
	result := waitForBuild(t, peer, build.ID)
	if result.Status != "failed" || result.Error == nil || !strings.Contains(result.Error.Message, "Ninja executable is unavailable") {
		t.Fatalf("missing Ninja was not reported: %+v", result)
	}
	if frame := peer.call(t, "system.capabilities", map[string]any{}); frame.Error != nil {
		t.Fatalf("connection stopped serving after a failed build: %+v", frame)
	}
}
