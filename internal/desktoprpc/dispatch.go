package desktoprpc

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	"trestle/internal/app"
	"trestle/internal/config"
	"trestle/internal/toolchain"
)

var methods = []string{
	"system.handshake", "system.capabilities",
	"workspace.open", "workspace.list", "workspace.get", "workspace.targets",
	"toolchain.list", "toolchain.detect", "toolchain.select",
	"build.start", "build.configure", "build.cancel", "build.status", "build.log", "build.history", "build.commands",
	"graph.get", "graph.dependencies", "analysis.summary",
}

func (c *connection) dispatch(ctx context.Context, req request) (any, error) {
	if req.Method == "system.handshake" {
		return c.handshake(req.Params)
	}
	if !c.ready.Load() {
		return nil, &rpcError{Code: -32001, Message: "system.handshake is required"}
	}
	switch req.Method {
	case "system.capabilities":
		return map[string]any{"version": version, "methods": methods, "platform": runtime.GOOS}, nil
	case "workspace.list":
		return c.jobs.list(), nil
	case "workspace.open":
		var p struct {
			Path string `json:"path"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		return c.jobs.open(ctx, p.Path)
	case "build.status", "build.cancel", "build.log", "build.commands", "analysis.summary":
		var p struct {
			ID     string `json:"build_id"`
			Offset int64  `json:"offset,omitempty"`
			Target string `json:"target,omitempty"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		switch req.Method {
		case "build.commands":
			return c.jobs.commands(p.ID, p.Target, p.Offset)
		case "build.log":
			return c.jobs.log(p.ID, p.Offset)
		case "analysis.summary":
			return c.jobs.analysis(p.ID)
		default:
			return c.jobs.status(p.ID, req.Method == "build.cancel")
		}
	case "toolchain.list":
		return toolchain.Discover(ctx), ctx.Err()
	case "workspace.get", "workspace.targets", "graph.get", "graph.dependencies", "build.history",
		"toolchain.detect", "toolchain.select", "build.start", "build.configure":
		return c.workspaceMethod(ctx, req)
	default:
		return nil, &rpcError{Code: -32601, Message: "unknown method " + req.Method}
	}
}

func (c *connection) workspaceMethod(ctx context.Context, req request) (any, error) {
	var p struct {
		ID       string `json:"workspace_id"`
		Target   string `json:"target,omitempty"`
		Compiler string `json:"compiler,omitempty"`
		app.ProjectOperation
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	ws, err := c.jobs.workspace(p.ID)
	if err != nil {
		return nil, err
	}
	switch req.Method {
	case "workspace.get":
		return c.jobs.open(ctx, ws.ID)
	case "workspace.targets", "graph.get", "graph.dependencies":
		snapshot, err := app.InspectProject(ctx, ws.ID)
		if err != nil {
			return nil, err
		}
		if req.Method != "graph.dependencies" {
			return snapshot.Targets, nil
		}
		for _, target := range snapshot.Targets {
			if string(target.ID) == p.Target {
				return target, nil
			}
		}
		return nil, fmt.Errorf("unknown target %q", p.Target)
	case "toolchain.detect":
		return app.InspectToolchain(ctx, ws.ID, p.Member)
	case "toolchain.select":
		path := ws.ID
		if p.Member != "" {
			found := false
			for _, project := range ws.Projects {
				if project.Name == p.Member {
					found = true
					path = filepath.Join(project.Path, config.DefaultFileName)
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown workspace member %q", p.Member)
			}
		}
		// Serialize configuration mutation against starting new jobs in this
		// connection. Config.Save also detects edits from other processes.
		c.jobs.mu.Lock()
		if c.jobs.changing[ws.ID] {
			c.jobs.mu.Unlock()
			return nil, fmt.Errorf("toolchain selection is already in progress")
		}
		for _, j := range c.jobs.builds {
			if j.active && j.state.WorkspaceID == ws.ID {
				c.jobs.mu.Unlock()
				return nil, fmt.Errorf("cannot change the toolchain during an active operation")
			}
		}
		c.jobs.changing[ws.ID] = true
		c.jobs.mu.Unlock()
		defer func() {
			c.jobs.mu.Lock()
			delete(c.jobs.changing, ws.ID)
			c.jobs.mu.Unlock()
		}()
		err := app.SetCompilerWithContext(ctx, path, p.Compiler)
		if err != nil {
			return nil, err
		}
		return c.jobs.open(ctx, ws.ID)
	case "build.history":
		return c.jobs.history(ws.ID), nil
	case "build.start":
		return c.jobs.start(ws.ID, "build", p.ProjectOperation, c.publish)
	case "build.configure":
		return c.jobs.start(ws.ID, "configure", p.ProjectOperation, c.publish)
	default:
		return nil, &rpcError{Code: -32601, Message: "unknown workspace method"}
	}
}
