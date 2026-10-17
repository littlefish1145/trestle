package app

import (
	"context"
	"path/filepath"
	"strings"
	"trestle/internal/plan"
	"trestle/internal/toolchain"
)

func translateWSLArguments(ctx context.Context, distribution string, buildPlan *plan.BuildPlan) error {
	cache := map[string]string{}
	convert := func(value string) (string, error) {
		if err := toolchain.ValidateWSLPath(distribution, value); err != nil {
			return "", err
		}
		if filepath.VolumeName(value) == "" && !strings.HasPrefix(value, `\\`) {
			return strings.ReplaceAll(value, `\`, "/"), nil
		}
		if mapped, ok := cache[value]; ok {
			return mapped, nil
		}
		mapped, err := toolchain.WSLPath(ctx, distribution, value)
		if err != nil {
			return "", err
		}
		cache[value] = mapped
		return mapped, nil
	}
	for i := range buildPlan.Actions {
		if err := translateWSLAction(&buildPlan.Actions[i], convert); err != nil {
			return err
		}
	}
	return nil
}

// translateWSLAction rewrites the host paths embedded in one WSL command's
// arguments, leaving any other action untouched.
func translateWSLAction(action *plan.Action, convert func(string) (string, error)) error {
	if !strings.EqualFold(filepath.Base(action.Command.Exe), "wsl.exe") {
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
		if prefix == "" && filepath.VolumeName(arg) == "" && !strings.HasPrefix(arg, `\\`) {
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
