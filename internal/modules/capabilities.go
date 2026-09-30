package modules

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"trestle/internal/processx"
)

type CapabilityProbe struct {
	P1689Scan        bool
	NamedModules     bool
	Partitions       bool
	HeaderUnits      bool
	ExplicitArtifact bool
	Experimental     bool
}

func ProbeClang(ctx context.Context, compiler, scanner string) CapabilityProbe {
	probe := CapabilityProbe{}
	if compiler == "" || scanner == "" {
		return probe
	}
	if _, err := exec.LookPath(scanner); err != nil {
		return probe
	}
	directory, err := os.MkdirTemp("", "trestle-module-probe-")
	if err != nil {
		return probe
	}
	defer os.RemoveAll(directory)
	source := filepath.Join(directory, "probe.cppm")
	module := filepath.Join(directory, "probe.pcm")
	if err := os.WriteFile(source, []byte("export module probe; export int value() { return 1; }\n"), 0o644); err != nil {
		return probe
	}
	scan := processx.Command(ctx, scanner, "-format=p1689", "--", compiler, "-std=c++20", "-c", source)
	if scan.Run() == nil {
		probe.P1689Scan = true
	}
	compile := processx.Command(ctx, compiler, "-std=c++20", "--precompile", source, "-o", module)
	if compile.Run() == nil {
		probe.NamedModules = true
		probe.ExplicitArtifact = true
	}
	return probe
}

func ProbeCompiler(ctx context.Context, compiler string) CapabilityProbe {
	if runtime.GOOS == "windows" && strings.Contains(strings.ToLower(compiler), "cl") {
		return CapabilityProbe{Experimental: true}
	}
	return ProbeClang(ctx, compiler, "clang-scan-deps")
}
