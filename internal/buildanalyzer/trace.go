package buildanalyzer

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"trestle/internal/fsx"
)

type traceDocument struct {
	SchemaVersion int          `json:"schema_version"`
	Events        []traceEvent `json:"traceEvents"`
}

type traceEvent struct {
	Name string         `json:"name"`
	Kind string         `json:"ph"`
	PID  int            `json:"pid"`
	TID  int            `json:"tid"`
	TS   int64          `json:"ts"`
	Dur  int64          `json:"dur"`
	Args map[string]any `json:"args"`
}

func (session *Session) saveTrace(dir string) error {
	trace := traceDocument{SchemaVersion: SchemaVersion, Events: []traceEvent{}}
	for i, run := range session.Executions {
		if run.Metrics == nil {
			continue
		}
		timings := append([]EdgeTiming{}, run.Metrics.Timings...)
		sort.SliceStable(timings, func(i, j int) bool { return timings[i].StartMS < timings[j].StartMS })
		offset := run.StartedAt.Sub(session.StartedAt).Microseconds()
		var lanes []int64
		for _, timing := range timings {
			lane := 0
			for lane < len(lanes) && lanes[lane] > timing.StartMS {
				lane++
			}
			if lane == len(lanes) {
				lanes = append(lanes, timing.EndMS)
			} else {
				lanes[lane] = timing.EndMS
			}
			edge := run.Graph.Edges[timing.Edge]
			trace.Events = append(trace.Events, traceEvent{
				Name: edge.ID, Kind: "X", PID: i + 1, TID: lane + 1,
				TS: offset + timing.StartMS*1000, Dur: (timing.EndMS - timing.StartMS) * 1000,
				Args: map[string]any{"outputs": edge.Outputs, "pool": edge.Pool, "profile": run.Profile, "directory": run.Directory},
			})
		}
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(filepath.Join(dir, "trace.json"), append(data, '\n'))
}
