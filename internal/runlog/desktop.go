package runlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const desktopRetainedRuns = 32

// PruneDesktop retains the newest finished, opted-in records per project.
// Existing CLI and TUI history is left to their existing retention behavior.
func PruneDesktop(configPath string) {
	pruneDesktop(configPath, desktopRetainedRuns)
}

func pruneDesktop(configPath string, retain int) {
	root, err := filepath.Abs(filepath.Join(filepath.Dir(configPath), ".trestle", "runs"))
	if err != nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var completed []Record
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "desktop-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		body, err := os.ReadFile(filepath.Join(dir, "record.json"))
		if err != nil {
			continue
		}
		var record Record
		if json.Unmarshal(body, &record) != nil ||
			record.ID != entry.Name() || record.Origin != "desktop" ||
			record.LogPath != filepath.Join(dir, "output.log") {
			continue
		}
		if record.Status == "running" {
			recovered, ok := readRecord(root, record.ID)
			if !ok {
				continue
			}
			record = recovered
		}
		if record.Finished != nil && record.Status != "running" {
			completed = append(completed, record)
		}
	}
	sort.Slice(completed, func(i, j int) bool {
		return completed[i].Started.After(completed[j].Started)
	})
	for _, record := range completed[min(retain, len(completed)):] {
		dir := filepath.Join(root, record.ID)
		// Only the files created by this recorder are eligible for removal.
		if err := os.Remove(filepath.Join(dir, "output.log")); err != nil && !os.IsNotExist(err) {
			continue
		}
		for _, name := range []string{"record.json", "active.trestle-lock.json", "active.trestle-lock"} {
			_ = os.Remove(filepath.Join(dir, name))
		}
		_ = os.Remove(dir)
	}
}
