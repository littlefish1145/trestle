package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/policy"
	"trestle/internal/toolchain"
)

type assessmentMessage struct {
	fingerprint string
	cfg         config.Config
	statuses    map[string]policy.Status
	rules       policy.Report
	err         error
}
type inventoryMessage struct{ components []toolchain.Component }
type wslInventoryMessage struct{ components []toolchain.Component }

// Local refresh is deliberately limited to file reads and PATH checks.
func (model dashboardModel) probeCommand() tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(model.path)
		message := probeMessage{Error: err}
		if err != nil {
			return message
		}
		cfg, err := config.Decode(model.path, data)
		message.Error = err
		if err != nil {
			return message
		}
		message.Fingerprint = config.Fingerprint(data)
		message.Config, message.BaseConfig, message.ProjectName = cfg, cfg, cfg.Project.Name
		for name, target := range cfg.Targets {
			message.Targets++
			message.Sources += len(target.Sources)
			if target.Type == "test" {
				message.Tests = append(message.Tests, testJob{Name: name, Group: fallback(target.TestGroup, "default"), Files: append([]string{}, target.Sources...)})
			}
		}
		sort.Slice(message.Tests, func(i, j int) bool { return message.Tests[i].Name < message.Tests[j].Name })
		if client, err := vcpkg.NewClient(cfg.Vcpkg.Root); err == nil {
			message.Vcpkg, message.VcpkgRoot = true, client.Root
		}
		_, err = exec.LookPath("ninja")
		message.Ninja = err == nil
		_, err = exec.LookPath("cmake")
		message.CMake = err == nil
		_, err = exec.LookPath("xmake")
		message.Xmake = err == nil
		_, err = os.Stat(filepath.Join(filepath.Dir(model.path), "CMakeLists.txt"))
		message.CMakeProject = err == nil
		_, err = os.Stat(filepath.Join(filepath.Dir(model.path), "xmake.lua"))
		message.XmakeProject = err == nil
		return message
	}
}

func (model dashboardModel) assessmentCommand() tea.Cmd {
	return func() tea.Msg {
		message := assessmentMessage{fingerprint: model.projectFingerprint}
		if model.services.AssessTargets == nil {
			return message
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		message.cfg, message.statuses, message.rules, message.err = model.services.AssessTargets(ctx)
		return message
	}
}
func (model dashboardModel) inventoryCommand() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return inventoryMessage{components: toolchain.DiscoverNative(ctx)}
	}
}

func (model dashboardModel) wslInventoryCommand() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return wslInventoryMessage{components: toolchain.DiscoverWSL(ctx)}
	}
}
