package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"trestle/internal/config"
	"trestle/internal/toolchain"
)

func configuredExecutable(ctx context.Context, cfg config.Config, tc toolchain.Toolchain, root, value, role string) (string, error) {
	if cfg.Toolchain.Mode == "wsl" {
		path, err := toolchain.WSLExecutable(ctx, cfg.Toolchain.WSLDistribution, value)
		if err != nil {
			return "", fmt.Errorf("E_WSL_TOOLCHAIN_INCOMPLETE: configured %s %q in %q: %w", role, value, cfg.Toolchain.WSLDistribution, err)
		}
		return path, nil
	}
	requested := value
	if strings.ContainsAny(value, `/\`) && !filepath.IsAbs(value) {
		requested = filepath.Join(root, value)
	}
	if path, err := exec.LookPath(requested); err == nil {
		return path, nil
	}
	for key, paths := range tc.Env {
		if !strings.EqualFold(key, "PATH") {
			continue
		}
		for _, dir := range filepath.SplitList(paths) {
			for _, suffix := range []string{"", ".exe"} {
				candidate := filepath.Join(dir, value+suffix)
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					if path, err := exec.LookPath(candidate); err == nil {
						return path, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("E_TOOLCHAIN_EXECUTABLE: configured %s %q was not found from %s or the selected compiler environment; correct the setting or install the matching tool", role, value, root)
}
