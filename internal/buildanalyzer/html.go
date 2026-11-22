package buildanalyzer

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"sort"

	"trestle/internal/fsx"
)

//go:embed report.html
var reportTemplate string

var offlineReport = template.Must(template.New("report").Funcs(template.FuncMap{
	"milliseconds": func(value *float64) string {
		if value == nil {
			return ""
		}
		return fmt.Sprintf("%.1f", *value)
	},
}).Parse(reportTemplate))

type htmlTimedEdge struct {
	Name, Pool     string
	Outputs        []string
	StartMS, EndMS int64
}

type htmlRun struct {
	*Execution
	Critical []string
	Timings  []htmlTimedEdge
}

// ExportHTML writes a self-contained report that works without network access.
func (session *Session) ExportHTML(path string) error {
	view := struct {
		*Session
		Runs []htmlRun
	}{Session: session}
	for _, run := range session.Executions {
		block := htmlRun{Execution: run}
		if run.Graph != nil && run.Metrics != nil {
			for _, index := range run.Metrics.CriticalPath {
				if index >= 0 && index < len(run.Graph.Edges) {
					block.Critical = append(block.Critical, run.Graph.Edges[index].ID)
				}
			}
			for _, timing := range run.Metrics.Timings {
				if timing.Edge >= 0 && timing.Edge < len(run.Graph.Edges) {
					edge := run.Graph.Edges[timing.Edge]
					block.Timings = append(block.Timings, htmlTimedEdge{edge.ID, edge.Pool, edge.Outputs, timing.StartMS, timing.EndMS})
				}
			}
			sort.SliceStable(block.Timings, func(i, j int) bool { return block.Timings[i].StartMS < block.Timings[j].StartMS })
		}
		view.Runs = append(view.Runs, block)
	}
	var output bytes.Buffer
	if err := offlineReport.Execute(&output, view); err != nil {
		return err
	}
	return fsx.AtomicWrite(path, output.Bytes())
}
