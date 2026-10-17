package policy

import (
	"fmt"
	"path/filepath"
	"strings"

	"trestle/internal/condition"
	"trestle/internal/config"
	"trestle/internal/toolchain"
)

// assessor accumulates readiness verdicts for one configuration, sharing the
// probe and cycle-detection state across recursive target visits.
type assessor struct {
	probe    *Probe
	cfg      config.Config
	ninjaErr error
	results  map[string]Status
	visiting map[string]bool
}

// visit returns the readiness status for one target, memoizing the result and
// detecting dependency cycles across recursive visits.
func (a *assessor) visit(name string) Status {
	if status, ok := a.results[name]; ok {
		return status
	}
	if a.visiting[name] {
		return Status{Ready: false, Reasons: []string{"target dependency cycle"}}
	}
	a.visiting[name] = true
	target := a.cfg.Targets[name]
	reasons := a.targetReasons(target)
	status := Status{Ready: len(reasons) == 0, Reasons: reasons}
	a.results[name] = status
	delete(a.visiting, name)
	return status
}

// targetReasons evaluates every readiness condition for one target and returns
// the reasons it is not ready, in the order trestle reports them.
func (a *assessor) targetReasons(target config.Target) []string {
	sources, reasons := a.probe.resolveSources(target.Sources)
	reasons = append(reasons, a.pathReasons(target)...)
	if a.ninjaErr != nil {
		reasons = append(reasons, "host Ninja executable is unavailable")
	}
	mode, distribution := a.cfg.Toolchain.Mode, a.cfg.Toolchain.WSLDistribution
	reasons = append(reasons, a.conditionReasons(target, mode, distribution)...)
	reasons = append(reasons, a.wslReasons(target, mode, distribution)...)
	tc, tcErr, toolchainReasons := a.resolveToolchainIfNeeded(target)
	reasons = append(reasons, toolchainReasons...)
	needsC, needsCUDA := false, false
	if target.Type != "shader" {
		needsC, needsCUDA = sourceNeeds(sources)
	}
	reasons = append(reasons, a.compilerReasons(target, needsC, tc, tcErr, mode, distribution)...)
	reasons = append(reasons, a.cudaReasons(needsCUDA, tc, tcErr)...)
	reasons = append(reasons, a.shaderReasons(target)...)
	reasons = append(reasons, a.moduleReasons(tc, tcErr)...)
	reasons = append(reasons, a.requiredToolReasons(target, mode, distribution)...)
	reasons = append(reasons, a.dependencyReasons(target)...)
	return reasons
}

// pathReasons reports the missing include and library directories that make a
// non-shader target not ready.
func (a *assessor) pathReasons(target config.Target) []string {
	if target.Type == "shader" {
		return nil
	}
	var reasons []string
	for _, path := range append(append([]string{}, target.IncludeDirs...), target.PrivateIncludeDirs...) {
		if err := a.probe.checkPath(path, true); err != nil {
			reasons = append(reasons, fmt.Sprintf("include directory is unavailable: %s (%v)", path, err))
		}
	}
	for _, path := range target.LibraryDirs {
		if err := a.probe.checkPath(path, true); err != nil {
			reasons = append(reasons, fmt.Sprintf("library directory is unavailable: %s (%v)", path, err))
		}
	}
	return reasons
}

// sourceNeeds reports whether a source list contains C or CUDA translation
// units, which decide the extra compiler checks a target needs.
func sourceNeeds(sources []string) (needsC, needsCUDA bool) {
	for _, source := range sources {
		switch strings.ToLower(filepath.Ext(source)) {
		case ".c":
			needsC = true
		case ".cu":
			needsCUDA = true
		}
	}
	return needsC, needsCUDA
}

// conditionReasons reports a failing or unevaluable target condition.
func (a *assessor) conditionReasons(target config.Target, mode, distribution string) []string {
	var reasons []string
	match, err := condition.Evaluate(target.When, a.probe.facts(mode, distribution))
	if err != nil {
		reasons = append(reasons, err.Error())
	} else if !match {
		reasons = append(reasons, "condition not met: "+target.When)
	}
	return reasons
}

// wslReasons reports missing or mismatched WSL distributions for a target.
func (a *assessor) wslReasons(target config.Target, mode, distribution string) []string {
	var reasons []string
	if target.RequiresWSL != "" && !a.probe.hasWSL(target.RequiresWSL) {
		reasons = append(reasons, "WSL distribution unavailable: "+target.RequiresWSL)
	}
	if target.RequiresWSL != "" && (mode != "wsl" || target.RequiresWSL != distribution) {
		reasons = append(reasons, "requires WSL toolchain in "+target.RequiresWSL)
	}
	if mode == "wsl" && !a.probe.hasWSL(distribution) {
		reasons = append(reasons, "configured WSL distribution unavailable: "+fallback(distribution, "default"))
	}
	return reasons
}

// resolveToolchainIfNeeded resolves the shared toolchain when a target feature
// needs it, returning the failure as a readiness reason.
func (a *assessor) resolveToolchainIfNeeded(target config.Target) (toolchain.Toolchain, error, []string) {
	if target.Type == "shader" && a.cfg.Toolchain.CUDA == "" && a.cfg.Toolchain.CUDARoot == "" && !a.cfg.Build.Modules {
		return toolchain.Toolchain{}, nil, nil
	}
	tc, err := a.probe.resolveToolchain()
	if err != nil {
		return tc, err, []string{"configured toolchain unavailable: " + err.Error()}
	}
	return tc, nil, nil
}

// compilerReasons reports missing C compilers, archivers and linkers for the
// language and link kind the target uses.
func (a *assessor) compilerReasons(target config.Target, needsC bool, tc toolchain.Toolchain, tcErr error, mode, distribution string) []string {
	var reasons []string
	cCompiler := tc.CC
	if cCompiler == "" {
		cCompiler = tc.CXX
	}
	if needsC && tcErr == nil && !a.probe.tool(mode, distribution, cCompiler) {
		reasons = append(reasons, "C compiler unavailable: "+fallback(cCompiler, "no C compiler resolved from the selected toolchain"))
	}
	if target.Type == "static" && tcErr == nil && !a.probe.tool(mode, distribution, tc.Archiver) {
		reasons = append(reasons, "static target archiver unavailable: "+fallback(tc.Archiver, "no archiver resolved from the selected toolchain"))
	}
	linker := tc.Linker
	if linker == "" {
		linker = tc.CXX
	}
	if (target.Type == "shared" || target.Type == "executable" || target.Type == "test") && tcErr == nil && !a.probe.tool(mode, distribution, linker) {
		reasons = append(reasons, "target linker unavailable: "+fallback(linker, "no linker resolved from the selected toolchain"))
	}
	return reasons
}

// cudaReasons reports missing CUDA configuration and toolchain failures for
// targets whose sources or project settings require CUDA.
func (a *assessor) cudaReasons(needsCUDA bool, tc toolchain.Toolchain, tcErr error) []string {
	var reasons []string
	cudaConfigured := a.cfg.Toolchain.CUDA != "" || a.cfg.Toolchain.CUDARoot != ""
	if needsCUDA && !cudaConfigured {
		reasons = append(reasons, "CUDA source requires [toolchain].cuda to pin a CUDA version range such as 12.0~12.9")
	}
	if cudaConfigured && tcErr == nil {
		if err := a.probe.checkCUDA(tc); err != nil {
			reasons = append(reasons, "CUDA toolchain unavailable: "+err.Error())
		}
	}
	return reasons
}

// shaderReasons reports a Vulkan toolchain failure for shader targets.
func (a *assessor) shaderReasons(target config.Target) []string {
	if target.Type != "shader" {
		return nil
	}
	if err := a.probe.checkVulkan(target); err != nil {
		return []string{"Vulkan shader toolchain unavailable: " + err.Error()}
	}
	return nil
}

// moduleReasons reports C++ module requirements: a Clang toolchain and an
// available module scanner.
func (a *assessor) moduleReasons(tc toolchain.Toolchain, tcErr error) []string {
	if !a.cfg.Build.Modules {
		return nil
	}
	var reasons []string
	if tcErr == nil && tc.Kind != toolchain.Clang {
		reasons = append(reasons, "C++ modules require Clang; selected toolchain is "+string(tc.Kind))
	}
	if err := a.probe.checkModuleScanner(); err != nil {
		reasons = append(reasons, "module scanner unavailable: "+err.Error())
	}
	return reasons
}

// requiredToolReasons reports each requested tool that is unavailable in the
// target's toolchain mode.
func (a *assessor) requiredToolReasons(target config.Target, mode, distribution string) []string {
	var reasons []string
	for _, name := range target.RequiresTools {
		if !a.probe.tool(mode, distribution, name) {
			reasons = append(reasons, "missing "+mode+" tool: "+name)
		}
	}
	return reasons
}

// dependencyReasons reports unavailable packages and folds each dependency
// target's readiness reasons into this target.
func (a *assessor) dependencyReasons(target config.Target) []string {
	var reasons []string
	for _, dep := range target.Dependencies {
		if dep.Package != "" && !a.probe.hasPackage(dep.Package) {
			reasons = append(reasons, "package unavailable: "+dep.Package+": "+a.probe.packageErrors[dep.Package])
		}
		if dep.Target != "" {
			for _, reason := range a.visit(dep.Target).Reasons {
				reasons = append(reasons, "dependency "+dep.Target+": "+reason)
			}
		}
	}
	return reasons
}
