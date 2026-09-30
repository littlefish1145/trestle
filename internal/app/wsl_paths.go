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
		action := &buildPlan.Actions[i]
		if !strings.EqualFold(filepath.Base(action.Command.Exe), "wsl.exe") {
			continue
		}
		argumentStart := -1
		for j, arg := range action.Command.Args {
			if arg == "--exec" {
				argumentStart = j + 2
				break
			}
		}
		if argumentStart < 0 {
			continue
		}
		for j := argumentStart; j < len(action.Command.Args); j++ {
			arg := action.Command.Args[j]
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
			if prefix == "" && filepath.VolumeName(arg) == "" && !strings.HasPrefix(arg, `\\`) {
				continue
			}
			mapped, err := convert(path)
			if err != nil {
				return err
			}
			action.Command.Args[j] = prefix + mapped
		}
	}
	return nil
}
