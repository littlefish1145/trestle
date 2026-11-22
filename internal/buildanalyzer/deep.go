package buildanalyzer

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"trestle/internal/plan"
	"trestle/internal/processx"
	"trestle/internal/toolchain"
)

type deepKey struct{}

func WithDeepProfile(ctx context.Context) context.Context {
	return context.WithValue(ctx, deepKey{}, true)
}

func DeepProfile(ctx context.Context) bool { return ctx.Value(deepKey{}) != nil }

// DeepBuildDir gives each opt-in build a fresh isolated artifact tree.
func DeepBuildDir(ctx context.Context) (string, error) {
	session, ok := ctx.Value(sessionKey{}).(*Session)
	if !ok {
		return "", fmt.Errorf("deep profiling requires a Build Analyzer session")
	}
	dir, err := session.prepareDirectory(session.root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(dir, "deep"))
}

type CompilerArtifact struct {
	Edge    int             `json:"edge"`
	Adapter string          `json:"adapter"`
	File    string          `json:"file"`
	Events  []CompilerEvent `json:"events,omitempty"`
	Warning string          `json:"warning,omitempty"`
}

type CompilerEvent struct {
	Name       string  `json:"name"`
	DurationUS float64 `json:"duration_us"`
}

func Instrument(directory string, buildPlan *plan.BuildPlan, compiler toolchain.Toolchain) ([]CompilerArtifact, error) {
	const logDir = "compiler-profile"
	if err := os.MkdirAll(filepath.Join(directory, logDir), 0o755); err != nil {
		return nil, err
	}
	var artifacts []CompilerArtifact
	for i := range buildPlan.Actions {
		action := &buildPlan.Actions[i]
		if !strings.HasPrefix(string(action.ID), "compile_") || len(action.Inputs) == 0 || len(action.Outputs) == 0 {
			continue
		}
		driver := compiler.CXX
		if filepath.Ext(action.Inputs[0]) == ".c" {
			driver = compiler.CC
		}
		adapter := string(compiler.Kind)
		clangCL := strings.HasPrefix(strings.ToLower(filepath.Base(driver)), "clang-cl")
		switch {
		case strings.EqualFold(filepath.Ext(action.Inputs[0]), ".cu"):
			adapter = "nvcc"
		case clangCL:
			adapter = "clang"
		}
		base := fmt.Sprintf("%s/%d", logDir, i)
		artifact := CompilerArtifact{Edge: i, Adapter: adapter}
		switch adapter {
		case "clang":
			artifact.File = base + ".json"
			flag := "-ftime-trace=" + artifact.File
			if clangCL {
				flag = "/clang:" + flag
			}
			action.Command.Args = append(action.Command.Args, flag)
		case "nvcc":
			artifact.File = base + ".csv"
			action.Command.Args = append(action.Command.Args, "--time", artifact.File)
		case "gcc", "msvc":
			artifact.File = base + ".log"
			launcher, err := os.Executable()
			if err != nil {
				return nil, err
			}
			flag := "-ftime-report"
			if adapter == "msvc" {
				flag = "/Bt+"
			}
			action.Command.Args = append(action.Command.Args, flag)
			action.Command.Prefix = []string{launcher, "__profile-compiler", artifact.File}
		default:
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

// RunProfileCompiler runs a compiler with both diagnostic streams preserved
// for Ninja and duplicates them to that compile edge's profiling file.
func RunProfileCompiler(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("missing profile output or compiler")
	}
	cmd := processx.Command(ctx, args[1], args[2:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	log, err := os.Create(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "Build Analyzer compiler output:", err)
		return cmd.Run()
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = io.MultiWriter(stdout, profileLog{log}), io.MultiWriter(stderr, profileLog{log})
	return cmd.Run()
}

type profileLog struct{ file *os.File }

func (log profileLog) Write(data []byte) (int, error) {
	// Diagnostics must still reach Ninja if capturing the extra log fails.
	_, _ = log.file.Write(data)
	return len(data), nil
}

func (run *Execution) CollectCompiler(artifacts []CompilerArtifact) {
	completed := map[int]bool{}
	if run.Metrics != nil {
		for _, timing := range run.Metrics.Timings {
			completed[timing.Edge] = true
		}
	}
	for _, artifact := range artifacts {
		file, err := os.Open(diskPath(run.Directory, artifact.File))
		if os.IsNotExist(err) {
			if completed[artifact.Edge] {
				artifact.Warning = "compiler completed without producing a timing artifact"
				run.CompilerProfile = append(run.CompilerProfile, artifact)
			}
			continue
		}
		if err == nil {
			var data []byte
			data, err = io.ReadAll(io.LimitReader(file, (64<<20)+1))
			file.Close()
			if err == nil && len(data) > 64<<20 {
				err = fmt.Errorf("compiler artifact exceeds 64 MiB")
			}
			if err == nil {
				artifact.Events, err = parseCompiler(artifact.Adapter, data)
			}
		}
		if err != nil {
			artifact.Warning = err.Error()
		}
		run.CompilerProfile = append(run.CompilerProfile, artifact)
	}
}

var (
	gccTiming  = regexp.MustCompile(`(?m)^\s*([^\r\n:]+?)\s*:\s*([0-9.]+)\s*(?:\([^)]+\))?\s+([0-9.]+)\s*(?:\([^)]+\))?\s+([0-9.]+)`)
	msvcTiming = regexp.MustCompile(`(?m)^\s*time\(([^\r\n]+?)\)\s*=\s*([0-9.]+)s`)
)

func parseCompiler(adapter string, data []byte) ([]CompilerEvent, error) {
	var events []CompilerEvent
	add := func(name, value string, scale float64) {
		millis, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil && millis >= 0 && !math.IsNaN(millis) && !math.IsInf(millis*scale, 0) {
			events = append(events, CompilerEvent{strings.TrimSpace(name), millis * scale})
		}
	}
	switch adapter {
	case "clang":
		var trace struct {
			Events []struct {
				Name string  `json:"name"`
				Kind string  `json:"ph"`
				Time float64 `json:"dur"`
			} `json:"traceEvents"`
		}
		if err := json.Unmarshal(data, &trace); err != nil {
			return nil, err
		}
		for _, event := range trace.Events {
			if event.Kind == "X" && event.Name != "" && event.Time >= 0 {
				events = append(events, CompilerEvent{event.Name, event.Time})
			}
		}
	case "gcc":
		for _, match := range gccTiming.FindAllStringSubmatch(string(data), -1) {
			add(match[1], match[4], 1e6)
		}
	case "msvc":
		for _, match := range msvcTiming.FindAllStringSubmatch(string(data), -1) {
			add(filepath.Base(match[1]), match[2], 1e6)
		}
	case "nvcc":
		reader := csv.NewReader(bytes.NewReader(data))
		reader.FieldsPerRecord = -1
		reader.TrimLeadingSpace = true
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return events, err
			}
			if len(row) >= 8 && strings.TrimSpace(row[len(row)-1]) == "ms" {
				add(row[1], row[len(row)-2], 1e3)
			}
		}
	}
	if len(events) == 0 {
		return events, fmt.Errorf("no %s timing events found", adapter)
	}
	return events, nil
}
