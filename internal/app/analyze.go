package app

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"trestle/internal/source"
)

func AnalyzeProject(root string, output io.Writer) error {
	project, err := source.Analyze(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Analyzing source tree...\n\n")
	fmt.Fprintf(output, "Sources\n")
	languages := make([]string, 0, len(project.Languages))
	for language := range project.Languages {
		languages = append(languages, string(language))
	}
	sort.Strings(languages)
	for _, language := range languages {
		label := language
		if language == string(source.LanguageHeader) {
			label = "Headers"
		}
		fmt.Fprintf(output, "  %-12s %d\n", label, project.Languages[source.Language(language)])
	}
	fmt.Fprintf(output, "  %-12s %d\n", "total", len(project.Sources))
	entrySources := project.MainSources()
	if len(entrySources) > 0 {
		fmt.Fprintf(output, "\nEntry points\n")
		for _, path := range entrySources {
			for _, entry := range project.Sources[path].EntryPoints {
				fmt.Fprintf(output, "  %s\n    kind: %s\n    inferred target: %s\n", relativeProjectPath(root, path), entry.Kind, entry.InferredTarget)
			}
		}
	}
	testCount := 0
	for _, info := range project.Sources {
		testCount += len(info.Tests)
	}
	if testCount > 0 {
		fmt.Fprintf(output, "\nTests\n  %d detected\n", testCount)
	}
	includeCount := 0
	for _, dependencies := range project.Includes {
		includeCount += len(dependencies)
	}
	fmt.Fprintf(output, "\nIncludes\n  %d local edges\n", includeCount)
	return nil
}

func relativeProjectPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}
