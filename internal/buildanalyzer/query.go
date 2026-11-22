package buildanalyzer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func LoadReport(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("invalid Build Analyzer report: %w", err)
	}
	if session.SchemaVersion != SchemaVersion || session.ID == "" {
		return nil, fmt.Errorf("unsupported Build Analyzer report schema %d", session.SchemaVersion)
	}
	for _, run := range session.Executions {
		if run == nil {
			return nil, errors.New("report contains a null build execution")
		}
	}
	return &session, nil
}

func reportPaths(root string) ([]string, error) {
	directory := ReportsDir(root)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			paths = append(paths, filepath.Join(directory, entry.Name(), "report.json"))
		}
	}
	slices.Reverse(paths)
	return paths, nil
}

func LatestReport(root string) (*Session, string, error) {
	paths, err := reportPaths(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	for _, path := range paths {
		if session, err := LoadReport(path); err == nil {
			return session, path, nil
		}
	}
	return nil, "", errors.New("no readable Build Analyzer report; run trestle build --profile first")
}

type ImpactResult struct {
	Complete bool           `json:"complete"`
	Files    []string       `json:"files"`
	Edges    []ImpactedEdge `json:"edges"`
}

type ImpactedEdge struct {
	Directory string   `json:"directory"`
	ID        string   `json:"id"`
	Via       string   `json:"via"`
	Outputs   []string `json:"outputs"`
}

type edgeRef struct{ run, edge int }

// Impact follows dirty-propagating paths, including across workspace members
// when a produced file in one manifest is a consumed file in another.
func (session *Session) Impact(root string, files []string) (ImpactResult, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return ImpactResult{}, err
	}
	result := ImpactResult{Complete: len(session.Executions) > 0, Files: files, Edges: []ImpactedEdge{}}
	consumers := map[string][]edgeRef{}
	for i, run := range session.Executions {
		if run.Graph == nil {
			result.Complete = false
			continue
		}
		result.Complete = result.Complete && run.Graph.Complete
		for j, edge := range run.Graph.Edges {
			for _, inputs := range [][]string{edge.Inputs, edge.Implicit, edge.Discovered, edge.DynamicInputs} {
				for _, name := range inputs {
					key := impactKey(diskPath(run.Directory, name))
					consumers[key] = append(consumers[key], edgeRef{i, j})
				}
			}
		}
	}
	pending := make([]string, 0, len(files))
	for _, name := range files {
		pending = append(pending, diskPath(root, name))
	}
	seen := map[edgeRef]bool{}
	for i := 0; i < len(pending); i++ {
		for _, ref := range consumers[impactKey(pending[i])] {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			run := session.Executions[ref.run]
			edge := run.Graph.Edges[ref.edge]
			outputs := []string{}
			for _, paths := range [][]string{edge.Outputs, edge.ImplicitOutputs, edge.DynamicOutputs} {
				for _, name := range paths {
					outputs = append(outputs, diskPath(run.Directory, name))
				}
			}
			pending = append(pending, outputs...)
			if !edge.phony() {
				result.Edges = append(result.Edges, ImpactedEdge{run.Directory, edge.ID, pending[i], outputs})
			}
		}
	}
	return result, nil
}

func impactKey(path string) string {
	key := pathKey(path)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}
