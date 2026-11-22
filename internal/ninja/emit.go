// Package ninja renders a build plan into the build.ninja manifest format and
// the dyndep files Ninja consumes for dynamic dependencies.
package ninja

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"trestle/internal/plan"
)

// Emit validates buildPlan and renders it as a build.ninja manifest: one rule per
// distinct action rule, the compile and console pools, every build statement, and
// the default goals.
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
	pools := PoolDepths(buildPlan)
	names := make([]string, 0, len(pools))
	for name := range pools {
		if name != "console" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&output, "pool %s\n  depth = %d\n\n", EscapePath(name), pools[name])
	}
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

// PoolDepths returns both the emitted pools and Ninja's built-in console pool.
func PoolDepths(buildPlan plan.BuildPlan) map[string]int {
	pools := map[string]int{"compile_pool": 2147483647, "console_pool": 1, "console": 1}
	for name, depth := range buildPlan.Pools {
		if name != "console" {
			pools[name] = depth
		}
	}
	return pools
}

func writeRule(output *bytes.Buffer, action plan.Action) {
	fmt.Fprintf(output, "rule %s\n", EscapePath(string(action.Rule)))
	if useArgumentFile(action.Command) {
		rsp := action.Outputs[0] + ".rsp"
		parts := commandPrefix(action.Command)
		fmt.Fprintf(output, "  command = %s\n", join(append(parts, shellQuote("@"+rsp)), " "))
		fmt.Fprintf(output, "  rspfile = %s\n", EscapePath(rsp))
		fmt.Fprintf(output, "  rspfile_content = %s\n", responseContent(action.Command))
	} else {
		fmt.Fprintf(output, "  command = %s\n", command(action))
	}
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
	fmt.Fprintf(output, "build %s", join(outputs, " "))
	if len(action.ImplicitOutputs) > 0 {
		output.WriteString(" | " + joinEscaped(action.ImplicitOutputs))
	}
	fmt.Fprintf(output, ": %s", EscapePath(string(action.Rule)))
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
	if action.Dyndep != "" {
		fmt.Fprintf(output, "  dyndep = %s\n", EscapePath(action.Dyndep))
	}
}

func command(action plan.Action) string {
	if action.Command.Exe == "" {
		return EscapePath("false")
	}
	parts := commandPrefix(action.Command)
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

func commandPrefix(command plan.Command) []string {
	parts := make([]string, 0, len(command.Prefix)+1)
	for _, argument := range command.Prefix {
		parts = append(parts, shellQuote(argument))
	}
	return append(parts, shellQuote(command.Exe))
}

func replacePaths(value string, inputs, outputs []string) string {
	// $in expands every input, so substituting individual inputs duplicates them.
	if len(inputs) == 1 && value == inputs[0] {
		return "$in"
	}
	if len(outputs) == 1 {
		output := outputs[0]
		if value == output {
			return "$out"
		}
		for _, prefix := range []string{"/Fo", "/Fe", "/OUT:", "/IMPLIB:"} {
			if value == prefix+output {
				return prefix + "$out"
			}
		}
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

// Slashes converts a filesystem path to forward slashes for use in a manifest.
func Slashes(path string) string { return filepath.ToSlash(path) }
