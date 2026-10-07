package toolchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PortableSelector rewrites a machine-local compiler reference into something
// safe to commit: a well-known compiler name when one can be recognised, and
// "auto" otherwise. Non-path values are returned unchanged, so "clang-cl",
// "18~20", and an explicit escape-hatch path all survive.
func PortableSelector(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "auto"
	}
	if !IsLocalPath(trimmed) {
		return trimmed
	}
	// Split off the last segment manually instead of filepath.Base: the value
	// may use Windows separators, which only filepath on Windows understands.
	base := strings.ToLower(strings.ReplaceAll(trimmed, `\`, "/"))
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	for _, name := range []string{"clang-cl", "clang++", "clang", "g++", "gcc", "mingw32-g++", "cl"} {
		if base == name {
			return name
		}
	}
	return "auto"
}

// MSVCToolsetVersion recovers the VCTOOLSVERSION that a path belonged to by
// matching the VC/Tools/MSVC/<version> segment, so a configuration that stored an
// absolute path can be expressed as a version range instead.
func MSVCToolsetVersion(value string) (string, bool) {
	segments := strings.Split(strings.ReplaceAll(value, "\\", "/"), "/")
	for index, segment := range segments {
		if !strings.EqualFold(segment, "MSVC") || index+1 >= len(segments) {
			continue
		}
		if version, ok := NormalizeVersion(segments[index+1]); ok {
			return version, true
		}
	}
	return "", false
}

// ToolkitVersionFromPath recovers a CUDA version from a toolkit root. The
// directory is normally named v12.4 or cuda-12.4, and toolkits also ship
// version.json and version.txt for layouts that use a plain name.
func ToolkitVersionFromPath(root string) (string, bool) {
	cleaned := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(root), "\\", "/"), "/")
	if index := strings.LastIndex(cleaned, "/"); index >= 0 {
		cleaned = cleaned[index+1:]
	}
	// Toolkits are installed as v12.4, cuda-12.4, or cuda_12.4 depending on
	// platform, and sometimes as a plain CUDA directory.
	for _, prefix := range []string{"v", "V", "cuda-", "cuda_", "CUDA-"} {
		if strings.HasPrefix(cleaned, prefix) {
			if version, ok := NormalizeVersion(strings.TrimPrefix(cleaned, prefix)); ok {
				return version, true
			}
		}
	}
	for _, name := range []string{"version.json", "version.txt"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		if version, ok := toolkitVersionFromMetadata(data); ok {
			return version, true
		}
	}
	return "", false
}

func toolkitVersionFromMetadata(data []byte) (string, bool) {
	var document struct {
		CUDA struct {
			Name string `json:"name"`
		} `json:"cuda"`
	}
	if json.Unmarshal(data, &document) == nil {
		if version, ok := NormalizeVersion(document.CUDA.Name); ok {
			return version, true
		}
	}
	return ExtractVersion(string(data))
}
