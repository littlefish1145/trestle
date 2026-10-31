package app

import (
	"context"
	"path"
	"strings"
	"trestle/internal/plan"
	"trestle/internal/toolchain"
)

func translateWSLArguments(ctx context.Context, distribution string, buildPlan *plan.BuildPlan) error {
	cache := map[string]string{}
	convert := func(distribution, value string) (string, error) {
		if err := toolchain.ValidateWSLPath(distribution, value); err != nil {
			return "", err
		}
		if !isWindowsWSLPath(value) {
			return strings.ReplaceAll(value, `\`, "/"), nil
		}
		if mapped, ok := cache[distribution+"\x00"+value]; ok {
			return mapped, nil
		}
		mapped, err := toolchain.WSLPath(ctx, distribution, value)
		if err != nil {
			return "", err
		}
		cache[distribution+"\x00"+value] = mapped
		return mapped, nil
	}
	for i := range buildPlan.Actions {
		action := &buildPlan.Actions[i]
		selectedDistribution := distribution
		for j, arg := range action.Command.Args {
			if arg == "--exec" {
				break
			}
			if arg == "-d" && j+1 < len(action.Command.Args) {
				selectedDistribution = action.Command.Args[j+1]
			}
		}
		if err := translateWSLAction(action, func(value string) (string, error) { return convert(selectedDistribution, value) }); err != nil {
			return err
		}
	}
	return nil
}

// translateWSLAction rewrites the host paths embedded in one WSL command's
// arguments, leaving any other action untouched.
func translateWSLAction(action *plan.Action, convert func(string) (string, error)) error {
	if !strings.EqualFold(path.Base(strings.ReplaceAll(action.Command.Exe, `\`, "/")), "wsl.exe") {
		return nil
	}
	argumentStart := -1
	for j, arg := range action.Command.Args {
		if arg == "--exec" {
			argumentStart = j + 2
			break
		}
	}
	if argumentStart < 0 {
		return nil
	}
	for j := argumentStart; j < len(action.Command.Args); j++ {
		arg := action.Command.Args[j]
		prefix, path := splitWSLArgument(arg)
		if path == "" {
			continue
		}
		if prefix == "" && !isWindowsWSLPath(arg) {
			continue
		}
		mapped, err := convert(path)
		if err != nil {
			return err
		}
		action.Command.Args[j] = prefix + mapped
	}
	return nil
}

// WSL arguments use Windows path syntax even when a build plan is inspected
// on Linux or macOS. filepath.VolumeName follows the host's syntax instead.
func isWindowsWSLPath(value string) bool {
	if strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
		return true
	}
	return len(value) >= 2 && value[1] == ':' &&
		(value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z')
}

// splitWSLArgument separates a Clang-style path flag into its literal prefix and
// the path value that follows it. Arguments without a recognised prefix return an
// empty prefix and the whole argument.
func splitWSLArgument(arg string) (string, string) {
	prefix := ""
	path := arg
	for _, candidate := range []string{"-fprebuilt-module-path=", "-fmodule-file=", "-I", "-L", "-MF", "@"} {
		if strings.HasPrefix(arg, candidate) {
			prefix = candidate
			path = strings.TrimPrefix(arg, candidate)
			break
		}
	}
	if prefix == "-fmodule-file=" {
		if equals := strings.Index(path, "="); equals >= 0 {
			prefix += path[:equals+1]
			path = path[equals+1:]
		}
	}
	return prefix, path
}
