package app

import (
	"path/filepath"
	"slices"
	"strings"

	"trestle/internal/plan"
)

// NinjaInvocation reports what Ninja observed for one generated manifest.
// NoWork is based on Ninja's own successful no-work response. It conveys
// nothing about compiler or remote cache hits.
type NinjaInvocation struct {
	Member       string        `json:"member"`
	Status       string        `json:"status"`
	NoWork       bool          `json:"no_work"`
	Reasons      []string      `json:"reasons,omitempty"`
	ReasonsCut   bool          `json:"reasons_truncated,omitempty"`
	CriticalPath *CriticalPath `json:"critical_path,omitempty"`
}

type CriticalPath struct {
	DurationMS int64      `json:"duration_ms"`
	Actions    []PathStep `json:"actions"`
}

type PathStep struct {
	Action     string `json:"action"`
	Output     string `json:"output"`
	DurationMS int64  `json:"duration_ms"`
}

// measuredCriticalPath uses the generated dependency DAG and the intervals
// observed in the current Ninja invocation. Untimed edges have zero weight
// but still preserve dependencies, including phony aliases. The path measures
// action duration, not scheduling delays, depfile-discovered edges or caches.
func measuredCriticalPath(actions []plan.Action, timings []NinjaTiming) *CriticalPath {
	if len(actions) == 0 || len(timings) == 0 {
		return nil
	}
	producers := make(map[string]int)
	for index, action := range actions {
		for _, output := range action.Outputs {
			key := filepath.Clean(output)
			if _, exists := producers[key]; exists {
				return nil
			}
			producers[key] = index
		}
	}
	measured := make([]*NinjaTiming, len(actions))
	for index := range timings {
		entry := &timings[index]
		producer, exists := producers[filepath.Clean(entry.Output)]
		if !exists {
			continue
		}
		if previous := measured[producer]; previous != nil {
			// Ninja can write one record per output of the same edge.
			// Differing intervals cannot be attributed uniquely.
			if previous.StartMS != entry.StartMS || previous.EndMS != entry.EndMS {
				return nil
			}
		} else {
			measured[producer] = entry
		}
	}
	degree := make([]int, len(actions))
	dependents := make([][]int, len(actions))
	for consumer, action := range actions {
		deps := make(map[int]bool)
		for _, inputs := range [][]string{action.Inputs, action.Implicit, action.OrderOnly} {
			for _, input := range inputs {
				producer, exists := producers[filepath.Clean(input)]
				if exists && !deps[producer] {
					deps[producer] = true
					degree[consumer]++
					dependents[producer] = append(dependents[producer], consumer)
				}
			}
		}
	}
	queue := make([]int, 0, len(actions))
	for index, count := range degree {
		if count == 0 {
			queue = append(queue, index)
		}
	}
	total := make([]int64, len(actions))
	steps := make([]int, len(actions))
	previous := make([]int, len(actions))
	for index := range previous {
		previous[index] = -1
	}
	best := -1
	for cursor := 0; cursor < len(queue); cursor++ {
		index := queue[cursor]
		if entry := measured[index]; entry != nil {
			total[index] += entry.EndMS - entry.StartMS
			steps[index]++
			if best < 0 || total[index] > total[best] ||
				(total[index] == total[best] && steps[index] > steps[best]) {
				best = index
			}
		}
		for _, consumer := range dependents[index] {
			if total[index] > total[consumer] ||
				(total[index] == total[consumer] && steps[index] > steps[consumer]) {
				total[consumer], steps[consumer], previous[consumer] = total[index], steps[index], index
			}
			degree[consumer]--
			if degree[consumer] == 0 {
				queue = append(queue, consumer)
			}
		}
	}
	if len(queue) != len(actions) || best < 0 {
		return nil
	}
	path := CriticalPath{DurationMS: total[best], Actions: make([]PathStep, 0, steps[best])}
	for index := best; index >= 0; index = previous[index] {
		if entry := measured[index]; entry != nil {
			path.Actions = append(path.Actions, PathStep{
				Action: string(actions[index].ID), Output: entry.Output, DurationMS: entry.EndMS - entry.StartMS,
			})
		}
	}
	slices.Reverse(path.Actions)
	return &path
}

type ninjaObservations struct {
	noWork  bool
	reasons []string
	seen    map[string]bool
	cut     bool
}

func (value *ninjaObservations) read(line string) {
	if strings.TrimSpace(line) == "ninja: no work to do." {
		value.noWork = true
		return
	}
	reason, ok := strings.CutPrefix(line, "ninja explain: ")
	if !ok || value.seen[reason] {
		return
	}
	if len(value.reasons) == 128 || len(reason) > 2048 {
		value.cut = true
		return
	}
	value.seen[reason] = true
	value.reasons = append(value.reasons, reason)
}
