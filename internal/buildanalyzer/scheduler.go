package buildanalyzer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"trestle/internal/plan"
	"trestle/internal/processx"
)

type schedulerKey struct{}

func WithHistoryScheduler(ctx context.Context) context.Context {
	return context.WithValue(ctx, schedulerKey{}, true)
}

// SchedulerCommand prepares the optional weights before starting Ninja. If the
// backend or data is unavailable, the original executable and args are used.
func (run *Execution) SchedulerCommand(ctx context.Context, args []string, buildPlan *plan.BuildPlan, progress func(string)) (string, []string, func()) {
	defaultCleanup := func() {}
	if run == nil || ctx.Value(schedulerKey{}) == nil || buildPlan == nil {
		return "ninja", args, defaultCleanup
	}
	run.RequestedScheduler = "history"
	fallback := func(reason string) (string, []string, func()) {
		message := "History scheduler using native Ninja: " + reason
		run.Warnings = append(run.Warnings, message)
		if progress != nil {
			progress(message)
		}
		return "ninja", args, defaultCleanup
	}
	session := ctx.Value(sessionKey{}).(*Session)
	if session.history == nil {
		data := readHistory(session.root, session.StartedAt)
		session.history = &data
	}
	weights := schedulerWeights(*session.history, run.Scope, *buildPlan)
	if weights == "" {
		return fallback("no matching action has three historical samples")
	}
	backend := os.Getenv("TRESTLE_HISTORY_NINJA")
	if backend == "" {
		name := "ninja-history"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		localBackend, err := filepath.Abs(filepath.Join(session.root, ".trestle", "tools", "build-analyzer", name))
		if err != nil {
			return fallback(err.Error())
		}
		if _, err := os.Stat(localBackend); err != nil {
			return fallback("patched Ninja is unavailable in the project or TRESTLE_HISTORY_NINJA")
		}
		backend = localBackend
	}
	backend, err := exec.LookPath(backend)
	if err != nil {
		return fallback(err.Error())
	}
	backend, err = filepath.Abs(backend)
	if err != nil {
		return fallback(err.Error())
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := processx.Command(probe, backend, "--trestle-history-version").Output()
	if err != nil || strings.TrimSpace(string(output)) != "trestle-history-v1 ninja a00b184" {
		return fallback("backend did not identify itself as the patched Ninja a00b184")
	}
	file, err := os.CreateTemp(run.Directory, ".trestle-history-*.tsv")
	if err != nil {
		return fallback(err.Error())
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	_, writeErr := file.WriteString(weights)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		return fallback(fmt.Sprintf("weights write failed (%v, %v)", writeErr, closeErr))
	}
	run.Scheduler = "unconfirmed"
	args = append([]string{"--trestle-history", filepath.Base(file.Name())}, args...)
	return backend, args, cleanup
}

func schedulerWeights(history history, scope string, buildPlan plan.BuildPlan) string {
	if scope == "" {
		return ""
	}
	outputs := map[string]bool{}
	for _, action := range buildPlan.Actions {
		if action.Rule != "phony" && len(action.Outputs) > 0 {
			outputs[pathKey(action.Outputs[0])] = true
		}
	}
	var rows []string
	for key, samples := range history.samples {
		if key.scope != scope || !outputs[key.output] || len(samples) < minHistorySamples ||
			strings.ContainsAny(key.output, "\t\r\n") {
			continue
		}
		if _, err := strconv.ParseUint(key.command, 16, 64); err == nil {
			rows = append(rows, fmt.Sprintf("%s\t%s\t%d\n", key.output, key.command, median(samples)))
		}
	}
	if len(rows) == 0 {
		return ""
	}
	slices.Sort(rows)
	return "trestle-history-v1\n" + strings.Join(rows, "")
}
