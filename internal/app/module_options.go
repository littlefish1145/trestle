package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"trestle/internal/modules/p1689"
	"trestle/internal/toolchain"
)

// mapWSLModuleOptions translates host paths embedded in Clang path flags.
// Relative paths remain relative to the build directory in both environments.
func mapWSLModuleOptions(ctx context.Context, distribution, directory string, options []string) ([]string, error) {
	result := append([]string{}, options...)
	prefixes := []string{"-isystem", "-iquote", "-include", "--sysroot=", "-I", "-F"}
	for i := 0; i < len(result); i++ {
		option := result[i]
		prefix, ok := moduleOptionPrefix(option, prefixes)
		if !ok {
			continue
		}
		value := strings.TrimPrefix(option, prefix)
		separate := value == ""
		if separate {
			if i+1 >= len(result) {
				return nil, fmt.Errorf("E_MODULE_SCAN_FAILED: %s requires a path", prefix)
			}
			i++
			value = result[i]
		}
		mapped, hostPath, err := mapWSLModuleOptionPath(ctx, distribution, directory, value)
		if err != nil {
			return nil, err
		}
		if !hostPath {
			continue
		}
		if separate {
			result[i] = mapped
		} else {
			result[i] = prefix + mapped
		}
	}
	return result, nil
}

// moduleOptionPrefix returns the first Clang path flag prefix that option matches,
// if any. An exact match such as "-I" takes its value from the next argument.
func moduleOptionPrefix(option string, prefixes []string) (string, bool) {
	for _, prefix := range prefixes {
		if option == prefix || strings.HasPrefix(option, prefix) {
			return prefix, true
		}
	}
	return "", false
}

// mapWSLModuleOptionPath translates a host path embedded in a module option to
// its WSL form, reporting whether the value actually named a host path.
func mapWSLModuleOptionPath(ctx context.Context, distribution, directory, value string) (string, bool, error) {
	if filepath.VolumeName(value) == "" && !strings.Contains(value, `\`) {
		return "", false, nil
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}
	mapped, err := toolchain.WSLPath(ctx, distribution, path)
	if err != nil {
		return "", false, err
	}
	return mapped, true, nil
}

func moduleScannableSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cpp", ".cc", ".cxx", ".c++", ".cppm", ".ixx", ".mxx":
		return true
	}
	return false
}

func hasModuleEdges(document p1689.Document) bool {
	for _, rule := range document.Rules {
		if len(rule.Provides) > 0 || len(rule.Requires) > 0 {
			return true
		}
	}
	return false
}

func hasPICOption(options []string) bool {
	for _, option := range options {
		if option == "-fPIC" || option == "-fpic" {
			return true
		}
	}
	return false
}
