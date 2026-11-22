package tui

import (
	"context"
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"trestle/internal/buildanalyzer"
)

const profiledBuildLabel = "Profiled build"

type analyzerMessage struct {
	report *buildanalyzer.Session
	path   string
	err    error
}

func (model dashboardModel) loadAnalyzer() tea.Cmd {
	root := filepath.Dir(model.path)
	return func() tea.Msg {
		report, path, err := buildanalyzer.LatestReport(root)
		return analyzerMessage{report, path, err}
	}
}

func (model dashboardModel) startProfiledBuild() (tea.Model, tea.Cmd) {
	if model.busy() || model.services.ProfileBuild == nil {
		return model, nil
	}
	model.selectRoute(routeIndex(BuildRoute))
	model.focus = focusContent
	member := model.member
	return model.startWorkflow(profiledBuildLabel, func(ctx context.Context, progress func(string)) error {
		return model.services.ProfileBuild(ctx, member, progress)
	})
}

func (model dashboardModel) analyzerLines(p palette, width int) []string {
	lines := []string{p.muted.Render("Enter reloads the latest report · Ctrl+P opens Profile build")}
	view := model.analyzer
	if view.err != nil {
		return appendWrappedLog(lines, p.muted, view.err.Error(), width)
	}
	if view.report == nil {
		return lines
	}
	lines = append(lines, "", p.title.Render(fmt.Sprintf("%s · %d ms", view.report.Outcome, view.report.DurationMS)))
	lines = appendWrappedLog(lines, p.muted, view.path, width)
	if view.report.Error != "" {
		lines = appendWrappedLog(lines, p.danger, view.report.Error, width)
	}
	for _, run := range view.report.Executions {
		lines = append(lines, "", p.title.Render(fmt.Sprintf("%s · %s · %d ms", run.Profile, run.Scheduler, run.DurationMS)))
		lines = appendWrappedLog(lines, p.muted, run.Directory, width)
		if !run.LogComplete {
			lines = append(lines, p.warning.Render("Timing log incomplete"))
		}
		if run.Graph != nil && !run.Graph.Complete {
			lines = append(lines, p.warning.Render("Dependency graph incomplete"))
		}
		if run.Metrics == nil || run.Graph == nil {
			continue
		}
		metrics := run.Metrics
		lines = append(lines,
			keyValue(p, "Completed edges", fmt.Sprint(len(metrics.Timings))),
			keyValue(p, "Peak parallelism", fmt.Sprint(metrics.PeakParallel)),
			keyValue(p, "Critical path", fmt.Sprintf("%d ms", metrics.CriticalPathMS)))
		for _, index := range metrics.CriticalPath {
			if index >= 0 && index < len(run.Graph.Edges) {
				lines = appendWrappedLog(lines, p.text, run.Graph.Edges[index].ID, width)
			}
		}
		if history := run.History; history != nil && history.MeanAbsErrorMS != nil {
			lines = append(lines, keyValue(p, "Prediction MAE", fmt.Sprintf("%.1f ms · %d edges", *history.MeanAbsErrorMS, len(history.Predictions))))
		}
	}
	return lines
}
