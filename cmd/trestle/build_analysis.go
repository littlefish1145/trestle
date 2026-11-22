package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trestle/internal/buildanalyzer"
)

func queryBuildAnalysis(flags runFlags, args []string) error {
	var session *buildanalyzer.Session
	var err error
	path := flags.analyzerReport
	if path == "" {
		session, path, err = buildanalyzer.LatestReport(".")
	} else {
		session, err = buildanalyzer.LoadReport(path)
	}
	if err != nil {
		return err
	}
	var result any
	switch flags.analyzerAction {
	case "html":
		if len(args) != 0 {
			return fmt.Errorf("usage: trestle analyze html [--report path] [--output file]")
		}
		output := flags.output
		if output == "" {
			output = filepath.Join(filepath.Dir(path), "report.html")
		}
		if !strings.EqualFold(filepath.Ext(output), ".html") && !strings.EqualFold(filepath.Ext(output), ".htm") {
			return fmt.Errorf("offline report output must use an .html or .htm extension")
		}
		if err := session.ExportHTML(output); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, output)
		return nil
	case "impact":
		if len(args) == 0 {
			return fmt.Errorf("usage: trestle analyze impact [--report path] file ...")
		}
		result, err = session.Impact(".", args)
	case "why":
		if len(args) != 0 {
			return fmt.Errorf("usage: trestle analyze why [--report path]")
		}
		result = session.Why()
	}
	if err != nil {
		return err
	}
	output := struct {
		SchemaVersion int    `json:"schema_version"`
		Session       string `json:"session"`
		Outcome       string `json:"outcome"`
		Error         string `json:"error,omitempty"`
		Result        any    `json:"result"`
	}{buildanalyzer.SchemaVersion, session.ID, session.Outcome, session.Error, result}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}
