// Package buildanalyzer records and analyzes opt-in Ninja build sessions.
package buildanalyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trestle/internal/fsx"
	"trestle/internal/plan"
)

const SchemaVersion = 1

type Session struct {
	SchemaVersion int          `json:"schema_version"`
	ID            string       `json:"id"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at,omitempty"`
	DurationMS    int64        `json:"duration_ms"`
	Outcome       string       `json:"outcome"`
	Error         string       `json:"error,omitempty"`
	Executions    []*Execution `json:"executions"`
	DeepProfile   bool         `json:"deep_profile,omitempty"`

	root    string
	dir     string
	history *history
}

type Execution struct {
	Directory          string             `json:"directory"`
	Config             string             `json:"config"`
	Profile            string             `json:"profile"`
	Scheduler          string             `json:"scheduler"`
	Goals              []string           `json:"goals"`
	StartedAt          time.Time          `json:"started_at"`
	DurationMS         int64              `json:"duration_ms"`
	Outcome            string             `json:"outcome"`
	Error              string             `json:"error,omitempty"`
	LogComplete        bool               `json:"log_complete"`
	Entries            []LogEntry         `json:"entries"`
	Warnings           []string           `json:"warnings,omitempty"`
	Graph              *Graph             `json:"graph,omitempty"`
	Metrics            *Metrics           `json:"metrics,omitempty"`
	Explanations       []Explanation      `json:"explanations,omitempty"`
	Scope              string             `json:"configuration_id,omitempty"`
	Jobs               int                `json:"jobs,omitempty"`
	History            *HistoryAnalysis   `json:"history,omitempty"`
	RequestedScheduler string             `json:"requested_scheduler,omitempty"`
	AppliedWeights     int                `json:"applied_weights,omitempty"`
	CompilerProfile    []CompilerArtifact `json:"compiler_profile,omitempty"`

	before      logSnapshot
	snapshotErr error
}

type sessionKey struct{}

// Record wraps a complete CLI or TUI build, including failures before Ninja.
func Record(ctx context.Context, root string, progress func(string), build func(context.Context) error) error {
	session := &Session{SchemaVersion: SchemaVersion, StartedAt: time.Now(), Executions: []*Execution{}, root: root, DeepProfile: DeepProfile(ctx)}
	err := build(context.WithValue(ctx, sessionKey{}, session))
	session.FinishedAt = time.Now()
	session.DurationMS = session.FinishedAt.Sub(session.StartedAt).Milliseconds()
	session.Outcome, session.Error = outcome(ctx, err)
	session.analyzeHistory(root)
	path, writeErr := session.save(root)
	if progress != nil {
		if writeErr != nil {
			progress("Build Analyzer: " + writeErr.Error())
		} else {
			progress("Build Analyzer report: " + path)
		}
	}
	return err
}

// StartTiming excludes analyzer preparation from Ninja invocation wall time.
func (run *Execution) StartTiming() {
	if run != nil {
		run.StartedAt = time.Now()
	}
}

func (session *Session) save(root string) (string, error) {
	dir, err := session.prepareDirectory(root)
	if err != nil {
		return "", err
	}
	if err := session.saveTrace(dir); err != nil {
		return "", err
	}
	if err := session.saveHistoryIndex(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "report.json")
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return "", err
	}
	return path, fsx.AtomicWrite(path, append(data, '\n'))
}

func (session *Session) prepareDirectory(root string) (string, error) {
	if session.dir != "" {
		return session.dir, nil
	}
	parent := ReportsDir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(parent, session.StartedAt.UTC().Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return "", err
	}
	session.ID = filepath.Base(dir)
	session.dir = dir
	return dir, nil
}

func ReportsDir(root string) string {
	return filepath.Join(root, ".trestle", "build-analyzer")
}

// BeginExecution snapshots Ninja's timing log immediately before invocation.
// Callers hold the build directory lock until Finish returns.
func BeginExecution(ctx context.Context, directory, config, profile string, goals []string) *Execution {
	session, ok := ctx.Value(sessionKey{}).(*Session)
	if !ok {
		return nil
	}
	config, _ = filepath.Abs(config)
	run := &Execution{
		Directory: directory, Config: config, Profile: profile, Scheduler: "ninja",
		Goals: append([]string{}, goals...), Entries: []LogEntry{},
	}
	run.before, run.snapshotErr = snapshotLog(filepath.Join(directory, ".ninja_log"))
	run.StartedAt = time.Now()
	session.Executions = append(session.Executions, run)
	return run
}

func Active(ctx context.Context) bool {
	return ctx.Value(sessionKey{}) != nil
}

func (run *Execution) Finish(ctx context.Context, err error, buildPlan *plan.BuildPlan) {
	if run == nil {
		return
	}
	run.DurationMS = time.Since(run.StartedAt).Milliseconds()
	run.Outcome, run.Error = outcome(ctx, err)
	if buildPlan != nil {
		run.Graph = newGraph(run.Directory, *buildPlan)
		defer run.analyze()
	}
	if run.snapshotErr != nil {
		run.Warnings = append(run.Warnings, fmt.Sprintf("Ninja log before build: %v", run.snapshotErr))
		return
	}
	run.Entries, err = newLogEntries(filepath.Join(run.Directory, ".ninja_log"), run.before)
	run.LogComplete = err == nil
	if err != nil {
		run.Warnings = append(run.Warnings, fmt.Sprintf("Ninja log after build: %v", err))
	}
}

func outcome(ctx context.Context, err error) (string, string) {
	if err == nil {
		return "succeeded", ""
	}
	if ctx.Err() != nil {
		return "cancelled", err.Error()
	}
	return "failed", err.Error()
}
