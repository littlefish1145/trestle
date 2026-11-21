package desktoprpc

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"trestle/internal/app"
	"trestle/internal/model"
	"trestle/internal/runner"
)

const retainedBuilds = 24
const maxLog = 8 << 20

type buildState struct {
	ID          string                    `json:"build_id"`
	WorkspaceID string                    `json:"workspace_id"`
	Operation   string                    `json:"operation"`
	Compiler    string                    `json:"compiler"`
	Options     app.ProjectOperation      `json:"options"`
	Status      string                    `json:"status"`
	Created     time.Time                 `json:"created_at"`
	Started     *time.Time                `json:"started_at,omitempty"`
	Ended       *time.Time                `json:"ended_at,omitempty"`
	Error       *rpcError                 `json:"error,omitempty"`
	Progress    *progress                 `json:"progress,omitempty"`
	LogSize     int64                     `json:"log_size"`
	LogCapped   bool                      `json:"log_capped"`
	LogPaths    []string                  `json:"log_paths,omitempty"`
	DurationMS  int64                     `json:"duration_ms"`
	Artifacts   map[model.TargetID]string `json:"artifacts,omitempty"`
	TimingCount int                       `json:"timing_count"`
}

type progress struct {
	Member    string `json:"member"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Running   int    `json:"running"`
}

type timing struct {
	Member string `json:"member"`
	app.NinjaTiming
}

type job struct {
	state          buildState
	cancel         context.CancelFunc
	file           *os.File
	active         bool
	sequence       uint64
	timings        []timing
	invocations    []app.NinjaInvocation
	analysisBudget int
}

type session struct {
	historyDir string
	changing   map[string]bool
	ctx        context.Context
	dir        string
	mu         sync.Mutex
	workspaces map[string]app.ProjectSnapshot
	builds     map[string]*job
	order      []string
	next       uint64
	workers    sync.WaitGroup
}

func newSession(ctx context.Context) (*session, error) {
	dir, err := os.MkdirTemp("", "trestle-rpc-")
	if err != nil {
		return nil, err
	}
	s := &session{ctx: ctx, dir: dir, workspaces: map[string]app.ProjectSnapshot{}, builds: map[string]*job{}, changing: map[string]bool{}}
	s.loadHistory(os.Getenv("TRESTLE_DESKTOP_STATE_DIR"))
	return s, nil
}

func (s *session) close() {
	s.workers.Wait()
	for _, j := range s.builds {
		if j.file != nil {
			j.file.Close()
		}
	}
	os.RemoveAll(s.dir)
}

func (s *session) open(ctx context.Context, path string) (app.ProjectSnapshot, error) {
	path, err := app.ProjectPath(path)
	if err != nil {
		return app.ProjectSnapshot{}, err
	}
	snapshot, err := app.InspectProject(ctx, path)
	if err != nil {
		return app.ProjectSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.workspaces) >= 64 {
		if _, known := s.workspaces[path]; !known {
			return app.ProjectSnapshot{}, fmt.Errorf("workspace limit reached")
		}
	}
	entry := snapshot
	entry.Targets = nil
	s.workspaces[path] = entry
	return snapshot, nil
}

func (s *session) workspace(id string) (app.ProjectSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.workspaces[id]
	if !ok {
		return snapshot, fmt.Errorf("workspace %q is not open", id)
	}
	return snapshot, nil
}

type workspaceEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
}

func (s *session) list() []workspaceEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]workspaceEntry, 0, len(s.workspaces))
	for _, snapshot := range s.workspaces {
		items = append(items, workspaceEntry{ID: snapshot.ID, Path: snapshot.Path, Name: snapshot.Name})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items
}

func (s *session) start(id, operation string, options app.ProjectOperation, publish func(string, Event)) (buildState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.workspaces[id]; !exists {
		return buildState{}, fmt.Errorf("workspace %q is not open", id)
	}
	if s.changing[id] {
		return buildState{}, fmt.Errorf("toolchain selection is in progress")
	}
	for _, j := range s.builds {
		if j.active && j.state.WorkspaceID == id {
			return buildState{}, &rpcError{Code: -32004, Message: "an operation is already active in this workspace"}
		}
	}
	if len(s.order) >= retainedBuilds {
		evicted := false
		for index, buildID := range s.order {
			old := s.builds[buildID]
			if !old.active {
				if old.file != nil {
					old.file.Close()
					os.Remove(old.file.Name())
				}
				delete(s.builds, buildID)
				s.order = append(s.order[:index], s.order[index+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			return buildState{}, fmt.Errorf("active build limit reached")
		}
	}
	s.next++
	var entropy [6]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return buildState{}, err
	}
	created := time.Now()
	buildID := fmt.Sprintf("build-%s-%06d-%x", created.UTC().Format("20060102T150405.000000000Z"), s.next, entropy)
	file, err := os.OpenFile(filepath.Join(s.dir, buildID+".log"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return buildState{}, err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	snapshot := s.workspaces[id]
	compiler := snapshot.Compiler
	for _, project := range snapshot.Projects {
		if project.Name == options.Member {
			compiler = project.Compiler
			break
		}
	}
	state := buildState{Compiler: compiler, ID: buildID, WorkspaceID: id, Operation: operation, Options: options, Status: "queued", Created: created}
	j := &job{state: state, cancel: cancel, file: file, active: true}
	s.builds[buildID] = j
	s.order = append(s.order, buildID)
	s.workers.Go(func() { s.run(ctx, j, publish) })
	return state, nil
}

func (s *session) run(ctx context.Context, j *job, publish func(string, Event)) {
	defer j.cancel()
	// Call under s.mu to serialize notifications from Ninja stdout and stderr.
	send := func(method string, data any) {
		j.sequence++
		publish(method, Event{Version: version, BuildID: j.state.ID, Sequence: j.sequence, Timestamp: time.Now(), Data: data})
	}
	emit := func(text string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		offset := j.state.LogSize
		if remaining := maxLog - offset; remaining >= int64(len(text)+1) && !j.state.LogCapped {
			n, err := io.WriteString(j.file, text+"\n")
			j.state.LogSize += int64(n)
			j.state.LogCapped = err != nil
		} else {
			j.state.LogCapped = true
		}
		if len(text) > 8192 {
			text = text[:8192] + " …"
		}
		send("output", map[string]any{"text": text, "offset": offset, "next_offset": j.state.LogSize})
	}
	s.mu.Lock()
	j.state.Status = "preparing"
	now := time.Now()
	j.state.Started = &now
	send("status", j.state)
	s.mu.Unlock()
	ctx = app.WithBuildObserver(ctx, app.BuildObserver{
		Analysis: func(value app.NinjaInvocation) {
			s.mu.Lock()
			defer s.mu.Unlock()
			const maxInvocations = 256
			const maxAnalysisBytes = 1 << 20
			if len(j.invocations) >= maxInvocations {
				return
			}
			for i, reason := range value.Reasons {
				j.analysisBudget += len(reason) + 8
				if j.analysisBudget > maxAnalysisBytes {
					value.Reasons = value.Reasons[:i]
					value.ReasonsCut = true
					break
				}
			}
			if value.CriticalPath != nil {
				for _, step := range value.CriticalPath.Actions {
					j.analysisBudget += len(step.Action) + len(step.Output) + 48
				}
				if j.analysisBudget > maxAnalysisBytes {
					value.CriticalPath = nil
				}
			}
			j.invocations = append(j.invocations, value)
		},
		Artifacts: func(artifacts map[model.TargetID]string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			// Published status snapshots may be encoded concurrently.
			merged := make(map[model.TargetID]string, len(j.state.Artifacts)+len(artifacts))
			for id, path := range j.state.Artifacts {
				merged[id] = path
			}
			for id, path := range artifacts {
				merged[id] = path
			}
			j.state.Artifacts = merged
		},
		Progress: func(member string, value runner.Progress) {
			s.mu.Lock()
			defer s.mu.Unlock()
			changed := j.state.Status == "preparing"
			j.state.Status = "running"
			j.state.Progress = &progress{Member: member, Completed: value.Finished, Total: value.Total, Running: value.Running}
			if changed {
				send("status", j.state)
			}
			send("progress", j.state.Progress)
		},
		Timings: func(member string, origin time.Time, timings []app.NinjaTiming) {
			s.mu.Lock()
			defer s.mu.Unlock()
			offset := origin.Sub(now).Milliseconds()
			for _, entry := range timings {
				if len(j.timings) == 10000 {
					break
				}
				entry.StartMS += offset
				entry.EndMS += offset
				j.timings = append(j.timings, timing{Member: member, NinjaTiming: entry})
			}
			j.state.TimingCount = len(j.timings)
		},
		Log: func(path string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			j.state.LogPaths = append(j.state.LogPaths, path)
		},
	})
	err := ctx.Err()
	if err == nil {
		err = app.RunProjectOperation(ctx, j.state.WorkspaceID, j.state.Options, j.state.Operation == "configure", emit)
	}
	s.mu.Lock()
	j.state.Status = "completed"
	if err != nil {
		j.state.Status = "failed"
		j.state.Error = wireError(err)
	}
	if ctx.Err() != nil {
		j.state.Status = "cancelled"
		j.state.Error = nil
	}
	snapshotTime := time.Now()
	j.state.Ended = &snapshotTime
	j.state.DurationMS = snapshotTime.Sub(now).Milliseconds()
	j.active = false
	send("status", j.state)
	stored := storedBuild{Build: j.state, Timings: j.timings, Invocations: j.invocations}
	s.mu.Unlock()
	s.saveHistory(stored)
}

func (s *session) lookup(buildID string) (*job, error) {
	j, exists := s.builds[buildID]
	if !exists {
		return nil, fmt.Errorf("unknown build %q", buildID)
	}
	return j, nil
}

func (s *session) status(buildID string, cancel bool) (buildState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.lookup(buildID)
	if err != nil {
		return buildState{}, err
	}
	if cancel && j.active {
		j.cancel()
	}
	return j.state, nil
}

func (s *session) history(id string) []buildState {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []buildState{}
	for i := len(s.order) - 1; i >= 0; i-- {
		state := s.builds[s.order[i]].state
		if state.WorkspaceID == id {
			result = append(result, state)
		}
	}
	return result
}

func (s *session) log(buildID string, offset int64) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.lookup(buildID)
	if err != nil {
		return nil, err
	}
	if offset < 0 || offset > j.state.LogSize {
		return nil, fmt.Errorf("log offset is outside the retained output")
	}
	if offset == j.state.LogSize {
		return map[string]any{"offset": offset, "next_offset": offset, "log_size": offset, "text": "", "capped": j.state.LogCapped}, nil
	}
	data := make([]byte, min(64<<10, j.state.LogSize-offset))
	n, err := j.file.ReadAt(data, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if offset+int64(n) < j.state.LogSize {
		// Leave an incomplete UTF-8 code point for the next replay page.
		for i := n - 1; i >= max(0, n-4); i-- {
			if utf8.RuneStart(data[i]) {
				if !utf8.FullRune(data[i:n]) {
					n = i
				}
				break
			}
		}
	}
	return map[string]any{
		"offset": offset, "next_offset": offset + int64(n), "log_size": j.state.LogSize,
		"text": strings.ToValidUTF8(string(data[:n]), "\uFFFD"), "capped": j.state.LogCapped,
	}, nil
}

func (s *session) analysis(buildID string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.lookup(buildID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"build": j.state, "timings": append([]timing{}, j.timings...),
		"ninja_invocations": append([]app.NinjaInvocation{}, j.invocations...),
		"cache":             nil, "critical_path": nil,
	}, nil
}
