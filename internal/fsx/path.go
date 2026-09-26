package fsx

import (
	"os"
	"path/filepath"
)

func HasGlob(path string) bool {
	for _, char := range path {
		if char == '*' || char == '?' || char == '[' {
			return true
		}
	}
	return false
}

func ResolveSources(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		if !HasGlob(path) {
			if _, err := os.Stat(path); err != nil {
				return nil, err
			}
			result = append(result, filepath.Clean(path))
			seen[filepath.Clean(path)] = true
			continue
		}
		matches, err := filepath.Glob(path)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			clean := filepath.Clean(match)
			if !seen[clean] {
				result = append(result, clean)
				seen[clean] = true
			}
		}
	}
	return result, nil
}
