package buildanalyzer

import (
	"fmt"
	"sort"
)

type EdgeTiming struct {
	Edge        int    `json:"edge"`
	StartMS     int64  `json:"start_ms"`
	EndMS       int64  `json:"end_ms"`
	CommandHash string `json:"command_hash"`
}

type ParallelInterval struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
	Running int   `json:"running"`
}

type Metrics struct {
	Timings        []EdgeTiming       `json:"timings"`
	CriticalPath   []int              `json:"critical_path"`
	CriticalPathMS int64              `json:"critical_path_ms"`
	WorkMS         int64              `json:"work_ms"`
	PeakParallel   int                `json:"peak_parallel"`
	Timeline       []ParallelInterval `json:"parallel_timeline"`
}

func (run *Execution) analyze() {
	if run.Graph == nil {
		return
	}
	graph := run.Graph
	metrics := &Metrics{Timings: []EdgeTiming{}, CriticalPath: []int{}, Timeline: []ParallelInterval{}}
	run.Metrics = metrics
	mapped := map[int]EdgeTiming{}
	invalid := map[int]bool{}
	for _, entry := range run.Entries {
		index, ok := graph.producers[pathKey(entry.Output)]
		if !ok || graph.Edges[index].phony() {
			run.LogComplete = false
			run.Warnings = append(run.Warnings, fmt.Sprintf("Ninja log output %q has no executable producer", entry.Output))
			continue
		}
		timing := EdgeTiming{index, entry.StartMS, entry.EndMS, entry.CommandHash}
		if old, exists := mapped[index]; exists && old != timing {
			invalid[index] = true
		}
		mapped[index] = timing
	}
	weights := make([]int64, len(graph.Edges))
	for index := range graph.Edges {
		if invalid[index] {
			run.LogComplete = false
			run.Warnings = append(run.Warnings, fmt.Sprintf("conflicting timings for edge %q", graph.Edges[index].ID))
			continue
		}
		if timing, exists := mapped[index]; exists {
			metrics.Timings = append(metrics.Timings, timing)
			weight := timing.EndMS - timing.StartMS
			weights[index] = weight
			metrics.WorkMS += weight
		}
	}
	metrics.Timeline, metrics.PeakParallel = parallelTimeline(metrics.Timings)
	if order, _, err := graph.topological(); err == nil {
		metrics.CriticalPath, metrics.CriticalPathMS = criticalPath(graph, order, weights)
	}
}

func criticalPath(graph *Graph, order []int, weights []int64) ([]int, int64) {
	cost := make([]int64, len(order))
	previous := make([]int, len(order))
	last := -1
	for _, index := range order {
		previous[index] = -1
		for _, parent := range graph.Edges[index].Prerequisites {
			if cost[parent] > cost[index] {
				cost[index], previous[index] = cost[parent], parent
			}
		}
		cost[index] += weights[index]
		if cost[index] > 0 && (last < 0 || cost[index] > cost[last]) {
			last = index
		}
	}
	path := []int{}
	if last < 0 {
		return path, 0
	}
	for index := last; index >= 0; index = previous[index] {
		path = append(path, index)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, cost[last]
}

func parallelTimeline(timings []EdgeTiming) ([]ParallelInterval, int) {
	changes := map[int64]int{}
	for _, timing := range timings {
		if timing.EndMS > timing.StartMS {
			changes[timing.StartMS]++
			changes[timing.EndMS]--
		}
	}
	boundaries := make([]int64, 0, len(changes))
	for time, delta := range changes {
		if delta != 0 {
			boundaries = append(boundaries, time)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	var previous int64
	var running, peak int
	timeline := []ParallelInterval{}
	for _, time := range boundaries {
		if time > previous {
			timeline = append(timeline, ParallelInterval{previous, time, running})
		}
		running += changes[time]
		peak = max(peak, running)
		previous = time
	}
	return timeline, peak
}
