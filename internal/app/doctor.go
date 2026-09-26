package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trestle/internal/config"
)

func Doctor(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	tc, err := detectConfigured(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Compiler\n  ✓ %s\n", tc.CXX)
	fmt.Printf("Linker\n  ✓ %s\n", tc.Linker)
	if tc.Setup != "" {
		fmt.Printf("Environment\n  ✓ %s\n", tc.Setup)
	} else {
		fmt.Println("Environment\n  ! no setup script configured")
	}
	if len(tc.Env) > 0 {
		fmt.Println("Runtime\n  ✓ MSVC environment captured")
		for _, library := range []string{"libcmt.lib", "vcruntime.lib", "msvcrt.lib"} {
			fmt.Printf("  %s %s\n", status(libraryFound(tc.Env["LIB"], library)), library)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Build.BuildDir, cfg.Build.Profile, "build.ninja")); err == nil {
		fmt.Println("Generation\n  ✓ build.ninja exists")
	} else {
		fmt.Println("Generation\n  ! build.ninja has not been generated")
	}
	return nil
}

func libraryFound(libPath, name string) bool {
	for _, directory := range strings.Split(libPath, ";") {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			return true
		}
	}
	return false
}
