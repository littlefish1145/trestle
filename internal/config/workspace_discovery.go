package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"trestle/internal/fsx"
)

func workspaceRootTree(root Config) (map[string]any, error) {
	if root.sourcePath == "" {
		data, err := encodeTOML(root)
		if err != nil {
			return nil, err
		}
		return decodeTree(data)
	}
	data, err := os.ReadFile(root.sourcePath)
	if err != nil {
		return nil, err
	}
	if Fingerprint(data) != root.sourceFingerprint {
		return nil, configChanged(root.sourcePath)
	}
	return decodeTree(data)
}

func workspaceMemberPaths(rootDir string, root Config) ([]string, error) {
	members := slices.Clone(root.Workspace.Members)
	seen := make(map[string]bool)
	record := func(member string) (bool, error) {
		_, dir, err := MemberDir(rootDir, member)
		if err != nil {
			return false, err
		}
		canonical, err := fsx.Canonical(dir)
		if err != nil {
			return false, err
		}
		if runtime.GOOS == "windows" {
			canonical = strings.ToLower(canonical)
		}
		exists := seen[canonical]
		seen[canonical] = true
		return exists, nil
	}
	for _, member := range members {
		exists, err := record(member)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("E_WORKSPACE_INVALID: member %q is listed twice", member)
		}
	}
	if root.Workspace.Discover {
		discovered, err := DiscoverWorkspaceMembers(rootDir, root.Build.BuildDir)
		if err != nil {
			return nil, err
		}
		for _, member := range discovered {
			exists, err := record(member)
			if err != nil {
				return nil, err
			}
			if !exists {
				members = append(members, member)
			}
		}
	}
	return members, nil
}

// DiscoverWorkspaceMembers finds the first manifest in each directory branch.
// WalkDir lists entries in lexical order and does not follow directory links.
func DiscoverWorkspaceMembers(root, buildDir string) ([]string, error) {
	if !filepath.IsAbs(buildDir) {
		buildDir = filepath.Join(root, buildDir)
	}
	var members []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || path == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || path == buildDir {
			return filepath.SkipDir
		}
		info, err := os.Lstat(filepath.Join(path, DefaultFileName))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		member, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		members = append(members, filepath.ToSlash(member))
		return filepath.SkipDir
	})
	return members, err
}
