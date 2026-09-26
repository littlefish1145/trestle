package ninja

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"trestle/internal/plan"
)

func Emit(buildPlan plan.BuildPlan) ([]byte, error) {
	if err := buildPlan.Validate(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.WriteString("ninja_required_version = 1.10\n\n")
	writtenRules := map[string]bool{}
	for _, action := range buildPlan.Actions {
		if action.Rule == "phony" {
			continue
		}
		if !writtenRules[string(action.Rule)] {
			writeRule(&output, action)
			writtenRules[string(action.Rule)] = true
		}
	}
	output.WriteString("pool compile_pool\n  depth = 2147483647\n\n")
	output.WriteString("pool console_pool\n  depth = 1\n\n")
	for _, action := range buildPlan.Actions {
		writeAction(&output, action)
		output.WriteByte('\n')
	}
	defaults := append([]string{}, buildPlan.Defaults...)
	if len(defaults) == 0 {
		defaults = []string{"all"}
	}
	output.WriteString("default ")
	for i, target := range defaults {
		if i > 0 {
			output.WriteByte(' ')
		}
		output.WriteString(EscapePath(target))
	}
	output.WriteByte('\n')
	return output.Bytes(), nil
}

func writeRule(output *bytes.Buffer, action plan.Action) {
	fmt.Fprintf(output, "rule %s\n", EscapePath(string(action.Rule)))
	fmt.Fprintf(output, "  command = %s\n", command(action))
	if action.Depfile != nil {
		fmt.Fprintf(output, "  depfile = %s\n", EscapePath(action.Depfile.Path))
	}
	if action.Deps != "" {
		fmt.Fprintf(output, "  deps = %s\n", action.Deps)
	}
	if action.Pool != "" {
		fmt.Fprintf(output, "  pool = %s\n", EscapePath(action.Pool))
	}
	description := strings.ReplaceAll(string(action.ID), "_", " ")
	fmt.Fprintf(output, "  description = %s $out\n", description)
}

func writeAction(output *bytes.Buffer, action plan.Action) {
	inputs := make([]string, len(action.Inputs))
	for i, input := range action.Inputs {
		inputs[i] = EscapePath(input)
	}
	outputs := make([]string, len(action.Outputs))
	for i, outputPath := range action.Outputs {
		outputs[i] = EscapePath(outputPath)
	}
	if action.Rule == "phony" {
		fmt.Fprintf(output, "build %s: phony", join(outputs, " "))
	} else {
		fmt.Fprintf(output, "build %s: %s", join(outputs, " "), EscapePath(string(action.Rule)))
	}
	if len(inputs) > 0 {
		output.WriteString(" " + join(inputs, " "))
	}
	if len(action.Implicit) > 0 {
		escaped := make([]string, len(action.Implicit))
		for i, input := range action.Implicit {
			escaped[i] = EscapePath(input)
		}
		output.WriteString(" | " + join(escaped, " "))
	}
	if len(action.OrderOnly) > 0 {
		escaped := make([]string, len(action.OrderOnly))
		for i, input := range action.OrderOnly {
			escaped[i] = EscapePath(input)
		}
		output.WriteString(" || " + join(escaped, " "))
	}
	output.WriteByte('\n')
}

func command(action plan.Action) string {
	if action.Command.Exe == "" {
		return EscapePath("false")
	}
	parts := []string{shellQuote(action.Command.Exe)}
	inputs := append([]string{}, action.Inputs...)
	outputs := append([]string{}, action.Outputs...)
	for _, argument := range action.Command.Args {
		quoted := shellQuote(replacePaths(argument, inputs, outputs))
		if len(parts) > 0 && parts[len(parts)-1] == quoted && (quoted == "$in" || quoted == "$out") {
			continue
		}
		parts = append(parts, quoted)
	}
	return join(parts, " ")
}

func replacePaths(value string, inputs, outputs []string) string {
	for _, input := range inputs {
		value = strings.ReplaceAll(value, input, "$in")
	}
	for _, output := range outputs {
		value = strings.ReplaceAll(value, output, "$out")
	}
	return value
}

func shellQuote(value string) string {
	if value == "$in" || value == "$out" {
		return value
	}
	if runtime.GOOS == "windows" {
		if strings.Contains(value, "$in") || strings.Contains(value, "$out") {
			return "\"" + value + "\""
		}
		if !strings.ContainsAny(value, " \t\"&|<>^") {
			return EscapePath(value)
		}
		replacer := strings.NewReplacer("\"", "\"\"", "%", "%%", "^", "^^", "!", "^^!")
		return EscapePath("\"" + replacer.Replace(value) + "\"")
	}
	quoted := value
	if strings.ContainsAny(quoted, " \t\n\"'\\`;&|<>()*?[#~=$") {
		quoted = "'" + strings.ReplaceAll(quoted, "'", "'\"'\"'") + "'"
	}
	return EscapePath(quoted)
}

func join(values []string, separator string) string { return strings.Join(values, separator) }

func Slashes(path string) string { return filepath.ToSlash(path) }
