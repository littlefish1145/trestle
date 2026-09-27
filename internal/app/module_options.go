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
		for _, prefix := range prefixes {
			if option != prefix && !strings.HasPrefix(option, prefix) {
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
			if filepath.VolumeName(value) != "" || strings.Contains(value, `\`) {
				if !filepath.IsAbs(value) {
					value = filepath.Join(directory, value)
				}
				mapped, err := toolchain.WSLPath(ctx, distribution, value)
				if err != nil {
					return nil, err
				}
				if separate {
					result[i] = mapped
				} else {
					result[i] = prefix + mapped
				}
			}
			break
		}
	}
	return result, nil
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
