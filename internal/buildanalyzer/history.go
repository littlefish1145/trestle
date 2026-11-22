package buildanalyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"trestle/internal/fsx"
)

const (
	historySessionLimit = 64
	historySampleLimit  = 15
	minHistorySamples   = 3
)

// ConfigurationID fingerprints the resolved build configuration and tools.
// Only the digest is persisted, including when the toolchain contains env data.
func ConfigurationID(configPath string, buildConfig, tools any) string {
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		absolute = configPath
	}
	data, err := json.Marshal([]any{impactKey(absolute), buildConfig, tools})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type historyKey struct {
	scope, output, command string
}

type history struct {
	samples map[historyKey][]int64
	skipped int
}

type historySample struct {
	Scope      string `json:"configuration_id"`
	Output     string `json:"output"`
	Command    string `json:"command_hash"`
	DurationMS int64  `json:"duration_ms"`
}

type historyIndex struct {
	SchemaVersion int             `json:"schema_version"`
	Session       string          `json:"session"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
	Samples       []historySample `json:"samples"`
}

func readHistory(root string, before time.Time) history {
	result := history{samples: map[historyKey][]int64{}}
	paths, _ := reportPaths(root)
	loaded := 0
	for _, path := range paths {
		index, err := loadHistoryIndex(path)
		if err != nil {
			result.skipped++
			continue
		}
		if index.StartedAt.IsZero() || index.FinishedAt.Before(index.StartedAt) ||
			!index.StartedAt.Before(before) || index.FinishedAt.After(before) || len(index.Samples) == 0 {
			continue
		}
		if loaded == historySessionLimit {
			break
		}
		loaded++
		result.add(index)
	}
	return result
}

func loadHistoryIndex(report string) (historyIndex, error) {
	var index historyIndex
	if _, err := os.Stat(report); err != nil {
		return index, err
	}
	dir := filepath.Dir(report)
	data, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if errors.Is(err, os.ErrNotExist) {
		session, err := LoadReport(report)
		if err != nil {
			return index, err
		}
		return session.historyIndex(), nil
	}
	if err != nil {
		return index, err
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return index, err
	}
	if index.SchemaVersion != SchemaVersion || index.Session != filepath.Base(dir) {
		return index, fmt.Errorf("invalid build history index")
	}
	return index, nil
}

func (session *Session) saveHistoryIndex(dir string) error {
	data, err := json.Marshal(session.historyIndex())
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(filepath.Join(dir, "history.json"), append(data, '\n'))
}

func (session *Session) historyIndex() historyIndex {
	finished := session.FinishedAt
	if finished.IsZero() {
		finished = session.StartedAt.Add(time.Duration(session.DurationMS) * time.Millisecond)
	}
	index := historyIndex{SchemaVersion: SchemaVersion, Session: session.ID, StartedAt: session.StartedAt, FinishedAt: finished, Samples: []historySample{}}
	if session.Outcome != "succeeded" || session.DeepProfile {
		return index
	}
	for _, run := range session.Executions {
		if run.Scope == "" || run.Outcome != "succeeded" || !run.LogComplete || run.Metrics == nil || run.Graph == nil {
			continue
		}
		for _, timing := range run.Metrics.Timings {
			if timing.Edge < 0 || timing.Edge >= len(run.Graph.Edges) || timing.StartMS < 0 ||
				timing.EndMS < timing.StartMS || timing.EndMS > run.DurationMS+1000 || timing.CommandHash == "" {
				continue
			}
			key, ok := run.historyKey(timing)
			if ok {
				index.Samples = append(index.Samples, historySample{key.scope, key.output, key.command, timing.EndMS - timing.StartMS})
			}
		}
	}
	return index
}

func (history history) add(index historyIndex) {
	seen := map[historyKey]bool{}
	for _, sample := range index.Samples {
		if sample.Scope == "" || sample.Output == "" || sample.Command == "" || sample.DurationMS < 0 || sample.DurationMS > math.MaxInt32 {
			continue
		}
		key := historyKey{sample.Scope, sample.Output, sample.Command}
		values := history.samples[key]
		if !seen[key] && len(values) < historySampleLimit {
			history.samples[key] = append(values, sample.DurationMS)
			seen[key] = true
		}
	}
}

func (run *Execution) historyKey(timing EdgeTiming) (historyKey, bool) {
	edge := run.Graph.Edges[timing.Edge]
	if edge.phony() || len(edge.Outputs) == 0 {
		return historyKey{}, false
	}
	return historyKey{run.Scope, pathKey(edge.Outputs[0]), timing.CommandHash}, true
}

func median(values []int64) int64 {
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	upper := ordered[len(ordered)/2]
	if len(ordered)%2 != 0 {
		return upper
	}
	lower := ordered[len(ordered)/2-1]
	return lower + (upper-lower)/2
}

type Prediction struct {
	Edge        int   `json:"edge"`
	Samples     int   `json:"samples"`
	PredictedMS int64 `json:"predicted_ms"`
	ActualMS    int64 `json:"actual_ms"`
}

type HistoryAnalysis struct {
	Predictions     []Prediction `json:"predictions"`
	Unpredicted     int          `json:"unpredicted"`
	SkippedReports  int          `json:"skipped_reports"`
	MeanAbsErrorMS  *float64     `json:"mean_abs_error_ms,omitempty"`
	LastSampleMAEMS *float64     `json:"last_sample_mae_ms,omitempty"`
	Simulation      *Simulation  `json:"simulation,omitempty"`
}

func (session *Session) analyzeHistory(root string) {
	if len(session.Executions) == 0 || session.DeepProfile {
		return
	}
	eligible := func(run *Execution) bool {
		return run.Scope != "" && run.LogComplete && run.Outcome == "succeeded" &&
			run.Graph != nil && run.Metrics != nil && len(run.Metrics.Timings) > 0
	}
	if session.history == nil && !slices.ContainsFunc(session.Executions, eligible) {
		return
	}
	if session.history == nil {
		data := readHistory(root, session.StartedAt)
		session.history = &data
	}
	for _, run := range session.Executions {
		if eligible(run) {
			run.analyzeHistory(*session.history)
		}
	}
}

func (run *Execution) analyzeHistory(history history) {
	result := &HistoryAnalysis{Predictions: []Prediction{}, SkippedReports: history.skipped}
	run.History = result
	modeled := make(map[int]simTask)
	var absError, lastError float64
	for _, timing := range run.Metrics.Timings {
		key, ok := run.historyKey(timing)
		if !ok {
			continue
		}
		actual := timing.EndMS - timing.StartMS
		samples := history.samples[key]
		task := simTask{duration: actual, previous: 1, predicted: 1}
		if len(samples) > 0 {
			task.previous, task.predicted = samples[0], samples[0]
		}
		if len(samples) >= minHistorySamples {
			task.predicted = median(samples)
			result.Predictions = append(result.Predictions, Prediction{timing.Edge, len(samples), task.predicted, actual})
			absError += math.Abs(float64(actual - task.predicted))
			lastError += math.Abs(float64(actual - task.previous))
		} else {
			result.Unpredicted++
		}
		modeled[timing.Edge] = task
	}
	if count := len(result.Predictions); count > 0 {
		mean, last := absError/float64(count), lastError/float64(count)
		result.MeanAbsErrorMS, result.LastSampleMAEMS = &mean, &last
	}
	if run.Graph.Complete && len(modeled) > 0 && run.Jobs > 0 {
		result.Simulation = simulate(run.Graph, modeled, run.Jobs)
	}
}
