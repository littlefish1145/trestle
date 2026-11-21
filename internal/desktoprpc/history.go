package desktoprpc

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxHistoryFileSize = 4 << 20

type storedBuild struct {
	Build   buildState `json:"build"`
	Timings []timing   `json:"timings"`
}

func (s *session) loadHistory(dir string) {
	if dir == "" || !filepath.IsAbs(dir) {
		return
	}
	s.historyDir = filepath.Join(dir, "build-history-v1")
	paths, err := historyPaths(s.historyDir)
	if err != nil {
		return
	}
	for _, path := range paths[max(0, len(paths)-retainedBuilds):] {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		var stored storedBuild
		err = json.NewDecoder(io.LimitReader(file, maxHistoryFileSize)).Decode(&stored)
		file.Close()
		if err != nil || stored.Build.ID+".json" != filepath.Base(path) ||
			stored.Build.Ended == nil || !filepath.IsAbs(stored.Build.WorkspaceID) {
			continue
		}
		// The transient replay queue is connection-owned. Canonical Core logs
		// can still be opened by path after restarting the desktop.
		stored.Build.LogSize = 0
		stored.Build.LogCapped = false
		s.builds[stored.Build.ID] = &job{state: stored.Build, timings: stored.Timings}
		s.order = append(s.order, stored.Build.ID)
	}
}

// saveHistory writes an immutable record per terminal run, so simultaneous
// desktop sessions do not overwrite one another's records.
func (s *session) saveHistory(build storedBuild) {
	if s.historyDir == "" {
		return
	}
	if err := os.MkdirAll(s.historyDir, 0700); err != nil {
		return
	}
	file, err := os.CreateTemp(s.historyDir, ".build-*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(build)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return
	}
	if err := os.Rename(file.Name(), filepath.Join(s.historyDir, build.Build.ID+".json")); err != nil {
		return
	}
	paths, err := historyPaths(s.historyDir)
	if err == nil {
		for _, path := range paths[:max(0, len(paths)-retainedBuilds)] {
			_ = os.Remove(path)
		}
	}
}

func historyPaths(dir string) ([]string, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type().IsRegular() && strings.HasPrefix(item.Name(), "build-") && strings.HasSuffix(item.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, item.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}
