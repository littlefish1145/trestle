package policy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"trestle/internal/condition"
	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/model"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

type Status struct {
	Ready   bool
	Reasons []string
}

type Change struct {
	Field  string
	Before string
	After  string
}

type RuleResult struct {
	Index   int
	When    string
	Target  string
	Matched bool
	Changes []Change
}

type Report struct {
	Rules          []RuleResult
	Changes        []Change
	Toolchain      toolchain.Toolchain
	ToolchainError string
}

type Probe struct {
	ctx              context.Context
	cfg              config.Config
	root             string
	packages         map[string]bool
	packageErrors    map[string]string
	pathChecked      map[string]bool
	pathErrors       map[string]error
	tools            map[string]bool
	wsl              map[string]bool
	cudaChecked      bool
	cudaError        error
	vulkanChecked    map[string]bool
	vulkanErrors     map[string]error
	toolchainChecked bool
	toolchainResult  toolchain.Toolchain
	toolchainError   error
}

func NewProbe(ctx context.Context, cfg config.Config) *Probe {
	return NewProbeAt(ctx, cfg, ".")
}

func NewProbeAt(ctx context.Context, cfg config.Config, root string) *Probe {
	if root == "" {
		root = "."
	}
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return &Probe{ctx: ctx, cfg: cfg, root: root, packages: map[string]bool{}, packageErrors: map[string]string{}, pathChecked: map[string]bool{}, pathErrors: map[string]error{}, tools: map[string]bool{}, wsl: map[string]bool{}, vulkanChecked: map[string]bool{}, vulkanErrors: map[string]error{}}
}

func (p *Probe) facts(mode, distribution string) condition.Facts {
	return condition.Facts{OS: runtime.GOOS, Arch: runtime.GOARCH, Mode: mode,
		Tool: func(name string) bool { return p.tool(mode, distribution, name) },
		WSL:  p.hasWSL, Package: p.hasPackage,
		WSLTool: func(distribution, name string) bool { return p.tool("wsl", distribution, name) },
		Env:     os.Getenv,
		Path: func(path string) bool {
			if !filepath.IsAbs(path) {
				path = filepath.Join(p.root, path)
			}
			_, err := os.Stat(path)
			return err == nil
		},
	}
}

func (p *Probe) tool(mode, distribution, name string) bool {
	key := mode + "|" + distribution + "|" + name
	if found, ok := p.tools[key]; ok {
		return found
	}
	found := p.commandAvailable(mode, distribution, name)
	p.tools[key] = found
	return found
}

func (p *Probe) commandAvailable(mode, distribution, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if mode == "wsl" {
		ctx, cancel := context.WithTimeout(p.ctx, 4*time.Second)
		defer cancel()
		_, err := toolchain.WSLExecutable(ctx, distribution, name)
		return err == nil
	}
	if (strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\')) && !filepath.IsAbs(name) {
		name = filepath.Join(p.root, name)
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func (p *Probe) hasWSL(distribution string) bool {
	if found, ok := p.wsl[distribution]; ok {
		return found
	}
	ctx, cancel := context.WithTimeout(p.ctx, 4*time.Second)
	defer cancel()
	_, err := toolchain.WSLExecutable(ctx, distribution, "sh")
	found := err == nil
	p.wsl[distribution] = found
	return found
}

func (p *Probe) hasPackage(name string) bool {
	if found, ok := p.packages[name]; ok {
		return found
	}
	pkg, ok := p.cfg.Packages[name]
	if !ok {
		p.packageErrors[name] = "package is not declared in [packages]"
		return false
	}
	if pkg.Triplet == "" || pkg.Triplet == "auto" {
		pkg.Triplet = p.cfg.Vcpkg.Triplet
	}
	root := p.cfg.Vcpkg.Root
	if root != "" && !filepath.IsAbs(root) {
		root = filepath.Join(p.root, root)
	}
	resolved, err := (vcpkg.Resolver{Root: root, Profile: p.cfg.Build.Profile, CRTLinkage: p.cfg.Vcpkg.CRTLinkage, LibraryLinkage: p.cfg.Vcpkg.LibraryLinkage}).Resolve(p.ctx, pkg)
	if err == nil {
		if resolved.Method == vcpkg.ResolutionManual {
			err = p.checkManualPackagePaths(pkg)
		} else {
			err = p.checkPackageUsagePaths(resolved.Usage)
		}
	}
	if err != nil {
		p.packageErrors[name] = err.Error()
	}
	found := err == nil
	p.packages[name] = found
	return found
}

func (p *Probe) checkManualPackagePaths(pkg config.Package) error {
	return p.checkPackagePaths(pkg.IncludeDirs, pkg.LibraryDirs, pkg.RuntimeFiles)
}

func (p *Probe) checkPackageUsagePaths(usage model.Usage) error {
	return p.checkPackagePaths(usage.Compile.IncludeDirs, usage.Link.LibraryDirs, usage.Link.RuntimeFiles)
}

func (p *Probe) checkPackagePaths(includeDirs, libraryDirs, runtimeFiles []string) error {
	for _, path := range includeDirs {
		if err := p.checkPath(path, true); err != nil {
			return fmt.Errorf("package include directory is unavailable: %s (%v)", path, err)
		}
	}
	for _, path := range libraryDirs {
		if err := p.checkPath(path, true); err != nil {
			return fmt.Errorf("package library directory is unavailable: %s (%v)", path, err)
		}
	}
	for _, path := range runtimeFiles {
		if err := p.checkPath(path, false); err != nil {
			return fmt.Errorf("package runtime file is unavailable: %s (%v)", path, err)
		}
	}
	return nil
}

func (p *Probe) checkPath(path string, directory bool) error {
	mode, distribution := p.cfg.Toolchain.Mode, p.cfg.Toolchain.WSLDistribution
	kind := "file"
	if directory {
		kind = "directory"
	}
	key := strings.Join([]string{mode, distribution, kind, path}, "|")
	if p.pathChecked[key] {
		return p.pathErrors[key]
	}
	err := p.probePath(mode, distribution, path, directory)
	p.pathChecked[key] = true
	p.pathErrors[key] = err
	return err
}

func (p *Probe) probePath(mode, distribution, path string, directory bool) error {
	if mode == "wsl" && strings.HasPrefix(path, "/") {
		ctx, cancel := context.WithTimeout(p.ctx, 4*time.Second)
		defer cancel()
		wsl, base, err := toolchain.WSLRunner(distribution)
		if err != nil {
			return err
		}
		check := "-f"
		if directory {
			check = "-d"
		}
		args := append(append([]string{}, base...), "--exec", "sh", "-lc", `test "$1" "$2"`, "trestle", check, path)
		if err := exec.CommandContext(ctx, wsl, args...).Run(); err != nil {
			return err
		}
		return nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.root, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	if !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	return nil
}

func (p *Probe) resolveSources(patterns []string) ([]string, []string) {
	var sources, reasons []string
	seen := map[string]bool{}
	for _, pattern := range patterns {
		resolvedPattern := pattern
		if !filepath.IsAbs(resolvedPattern) {
			resolvedPattern = filepath.Join(p.root, resolvedPattern)
		}
		matches := []string{resolvedPattern}
		if strings.ContainsAny(resolvedPattern, "*?[") {
			var err error
			matches, err = filepath.Glob(resolvedPattern)
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("invalid source pattern %q: %v", pattern, err))
				continue
			}
		}
		if len(matches) == 0 {
			reasons = append(reasons, fmt.Sprintf("source pattern matched no files: %s", pattern))
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("source is unavailable: %s (%v)", match, err))
				continue
			}
			if !info.Mode().IsRegular() {
				reasons = append(reasons, fmt.Sprintf("source is not a regular file: %s", match))
				continue
			}
			clean := filepath.Clean(match)
			if !seen[clean] {
				sources = append(sources, clean)
				seen[clean] = true
			}
		}
	}
	return sources, reasons
}

func (p *Probe) checkCUDA(host toolchain.Toolchain) error {
	if p.cudaChecked {
		return p.cudaError
	}
	p.cudaChecked = true
	p.cudaError = p.probeCUDA(host)
	return p.cudaError
}

func (p *Probe) probeCUDA(host toolchain.Toolchain) error {
	ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
	defer cancel()
	mode := p.cfg.Toolchain.CUDAExecution
	if mode == "" {
		mode = "native"
	}
	distribution := p.cfg.Toolchain.CUDAWSLDistribution
	if distribution == "" {
		distribution = p.cfg.Toolchain.WSLDistribution
	}
	root := p.cfg.Toolchain.CUDARoot
	if mode == "wsl" {
		if p.cfg.Toolchain.Mode != "wsl" || host.Runner == "" {
			return fmt.Errorf("WSL CUDA requires a WSL host compiler")
		}
		if p.cfg.Toolchain.WSLDistribution != "" && !strings.EqualFold(distribution, p.cfg.Toolchain.WSLDistribution) {
			return fmt.Errorf("E_CUDA_WSL_DISTRIBUTION: CUDA is in %s but the compiler is in %s", distribution, p.cfg.Toolchain.WSLDistribution)
		}
		_, err := cuda.DetectWSL(ctx, distribution, root, host)
		return err
	}
	if host.Runner != "" {
		return fmt.Errorf("E_CUDA_NATIVE_HOST: native CUDA cannot use a WSL host compiler; connect CUDA from the same WSL distribution")
	}
	resolved, err := toolchain.ResolveCUDARoot(ctx, toolchain.Request{
		CacheDir: toolchain.CacheDir(p.cfg.Toolchain.CacheDir, p.cfg.Root()),
		CUDA:     p.cfg.Toolchain.CUDA,
		CUDARoot: root,
	})
	if err != nil {
		return err
	}
	_, err = cuda.DetectWithHost(ctx, resolved, host)
	return err
}

func (p *Probe) checkVulkan(target config.Target) error {
	mode := p.cfg.Toolchain.VulkanExecution
	if mode == "" {
		mode = "native"
	}
	distribution := p.cfg.Toolchain.VulkanWSLDistribution
	if distribution == "" {
		distribution = p.cfg.Toolchain.WSLDistribution
	}
	root := p.cfg.Toolchain.Vulkan
	key := strings.Join([]string{mode, distribution, root}, "|")
	if !p.vulkanChecked[key] {
		p.vulkanChecked[key] = true
		ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
		var err error
		if mode == "wsl" {
			_, err = vulkan.DetectWSL(ctx, distribution, root)
		} else if root == "" {
			_, err = vulkan.Detect()
		} else {
			if !filepath.IsAbs(root) {
				root = filepath.Join(p.root, root)
			}
			_, err = vulkan.DetectRoot(root)
		}
		cancel()
		p.vulkanErrors[key] = err
	}
	if err := p.vulkanErrors[key]; err != nil {
		return err
	}
	if target.ShaderTool != "" && !p.tool(mode, distribution, target.ShaderTool) {
		return fmt.Errorf("configured shader tool %q was not found", target.ShaderTool)
	}
	return nil
}

func (p *Probe) checkModuleScanner() error {
	requested := p.cfg.Toolchain.ModuleScanner
	if requested == "" {
		requested = os.Getenv("CLANG_SCAN_DEPS")
	}
	mode, distribution := p.cfg.Toolchain.Mode, p.cfg.Toolchain.WSLDistribution
	if requested == "" {
		requested = "clang-scan-deps"
		if mode == "wsl" {
			ctx, cancel := context.WithTimeout(p.ctx, 4*time.Second)
			defer cancel()
			_, err := toolchain.WSLExecutable(ctx, distribution, requested)
			if err != nil {
				return err
			}
			return nil
		}
	}
	if mode == "wsl" && (filepath.VolumeName(requested) != "" || strings.Contains(requested, `\`)) {
		return fmt.Errorf("WSL requires a Linux clang-scan-deps path, got %q", requested)
	}
	if !p.tool(mode, distribution, requested) {
		return fmt.Errorf("%s was not found", requested)
	}
	return nil
}

func (p *Probe) resolveToolchain() (toolchain.Toolchain, error) {
	if p.toolchainChecked {
		return p.toolchainResult, p.toolchainError
	}
	p.toolchainChecked = true
	ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
	defer cancel()
	p.toolchainResult, p.toolchainError = toolchain.DetectConfigured(ctx, toolchain.Request{
		Mode:            p.cfg.Toolchain.Mode,
		WSLDistribution: p.cfg.Toolchain.WSLDistribution,
		C:               p.cfg.Toolchain.C,
		CXX:             p.cfg.Toolchain.CXX,
		Archiver:        p.cfg.Toolchain.Archiver,
		Linker:          p.cfg.Toolchain.Linker,
		Setup:           p.cfg.Toolchain.Setup,
		MSVC:            p.cfg.Toolchain.MSVC,
		CacheDir:        toolchain.CacheDir(p.cfg.Toolchain.CacheDir, p.cfg.Root()),
	})
	return p.toolchainResult, p.toolchainError
}

// Apply resolves conditional declarations in order, without changing the TOML.
func Apply(ctx context.Context, source config.Config) (config.Config, error) {
	return ApplyAt(ctx, source, ".")
}

func ApplyAt(ctx context.Context, source config.Config, root string) (config.Config, error) {
	cfg, _, err := ApplyWithReportAt(ctx, source, root)
	return cfg, err
}

func ApplyWithReportAt(ctx context.Context, source config.Config, root string) (config.Config, Report, error) {
	cfg := source
	report := Report{}
	cfg.Targets = make(map[string]config.Target, len(source.Targets))
	for name, target := range source.Targets {
		target.Dependencies = append([]config.Dependency(nil), target.Dependencies...)
		target.CompileOptions = append([]string(nil), target.CompileOptions...)
		target.CFlags = append([]string(nil), target.CFlags...)
		target.CXXFlags = append([]string(nil), target.CXXFlags...)
		target.LinkOptions = append([]string(nil), target.LinkOptions...)
		cfg.Targets[name] = target
	}
	cfg.Build.CompileFlags = append([]string(nil), source.Build.CompileFlags...)
	cfg.Build.CFlags = append([]string(nil), source.Build.CFlags...)
	cfg.Build.CXXFlags = append([]string(nil), source.Build.CXXFlags...)
	cfg.Build.LinkFlags = append([]string(nil), source.Build.LinkFlags...)
	p := NewProbeAt(ctx, cfg, root)
	for index, rule := range source.Rules {
		result := RuleResult{Index: index + 1, When: rule.When, Target: rule.Target}
		match, err := condition.Evaluate(rule.When, p.facts(cfg.Toolchain.Mode, cfg.Toolchain.WSLDistribution))
		if err != nil {
			return cfg, report, fmt.Errorf("rule %d: %w", index+1, err)
		}
		result.Matched = match
		if !match {
			report.Rules = append(report.Rules, result)
			continue
		}
		before := ruleValues(cfg)
		if rule.C != "" {
			cfg.Toolchain.C = rule.C
		}
		if rule.CXX != "" {
			cfg.Toolchain.CXX = rule.CXX
		}
		if rule.Mode != "" {
			cfg.Toolchain.Mode = rule.Mode
		}
		if rule.WSL != "" {
			cfg.Toolchain.WSLDistribution = rule.WSL
		}
		if rule.Target == "" {
			cfg.Build.CompileFlags = append(cfg.Build.CompileFlags, rule.CompileFlags...)
			cfg.Build.CFlags = append(cfg.Build.CFlags, rule.CFlags...)
			cfg.Build.CXXFlags = append(cfg.Build.CXXFlags, rule.CXXFlags...)
			cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, rule.LinkFlags...)
		}
		if rule.Target != "" {
			target := cfg.Targets[rule.Target]
			target.CompileOptions = append(target.CompileOptions, rule.CompileFlags...)
			target.CFlags = append(target.CFlags, rule.CFlags...)
			target.CXXFlags = append(target.CXXFlags, rule.CXXFlags...)
			target.LinkOptions = append(target.LinkOptions, rule.LinkFlags...)
			for _, name := range rule.Packages {
				found := false
				for _, dep := range target.Dependencies {
					if dep.Package == name {
						found = true
						break
					}
				}
				if !found {
					target.Dependencies = append(target.Dependencies, config.Dependency{Package: name, Scope: "private"})
				}
			}
			for _, name := range rule.DependsOn {
				found := false
				for _, dep := range target.Dependencies {
					if dep.Target == name {
						found = true
						break
					}
				}
				if !found {
					target.Dependencies = append(target.Dependencies, config.Dependency{Target: name, Scope: "private"})
				}
			}
			cfg.Targets[rule.Target] = target
		}
		if len(rule.Targets) > 0 {
			cfg.Build.DefaultTargets = append([]string(nil), rule.Targets...)
		}
		result.Changes = diffRuleValues(before, ruleValues(cfg))
		report.Rules = append(report.Rules, result)
	}
	if err := config.Validate(cfg); err != nil {
		return cfg, report, fmt.Errorf("resolved rules: %w", err)
	}
	report.Changes = diffRuleValues(ruleValues(source), ruleValues(cfg))
	return cfg, report, nil
}

func ruleValues(cfg config.Config) map[string]string {
	values := map[string]string{
		"toolchain.c": cfg.Toolchain.C, "toolchain.cxx": cfg.Toolchain.CXX,
		"toolchain.mode": cfg.Toolchain.Mode, "toolchain.wsl_distribution": cfg.Toolchain.WSLDistribution,
		"build.compile_flags":   formatRuleList(cfg.Build.CompileFlags),
		"build.c_flags":         formatRuleList(cfg.Build.CFlags),
		"build.cxx_flags":       formatRuleList(cfg.Build.CXXFlags),
		"build.link_flags":      formatRuleList(cfg.Build.LinkFlags),
		"build.default_targets": formatRuleList(cfg.Build.DefaultTargets),
	}
	for name, target := range cfg.Targets {
		prefix := "targets." + name + "."
		values[prefix+"compile_options"] = formatRuleList(target.CompileOptions)
		values[prefix+"c_flags"] = formatRuleList(target.CFlags)
		values[prefix+"cxx_flags"] = formatRuleList(target.CXXFlags)
		values[prefix+"link_options"] = formatRuleList(target.LinkOptions)
		dependencies := make([]string, 0, len(target.Dependencies))
		for _, dep := range target.Dependencies {
			if dep.Package != "" {
				dependencies = append(dependencies, "package:"+dep.Package+" ("+dep.Scope+")")
			} else {
				dependencies = append(dependencies, "target:"+dep.Target+" ("+dep.Scope+")")
			}
		}
		values[prefix+"dependencies"] = formatRuleList(dependencies)
	}
	return values
}

func formatRuleList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return fmt.Sprintf("%q", values)
}

func diffRuleValues(before, after map[string]string) []Change {
	keys := make([]string, 0, len(after))
	for key := range after {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]Change, 0)
	for _, key := range keys {
		if before[key] != after[key] {
			changes = append(changes, Change{Field: key, Before: before[key], After: after[key]})
		}
	}
	return changes
}

func Assess(ctx context.Context, cfg config.Config) map[string]Status {
	return AssessAt(ctx, cfg, ".")
}

func AssessAt(ctx context.Context, cfg config.Config, root string) map[string]Status {
	statuses, _, _ := AssessWithToolchainAt(ctx, cfg, root)
	return statuses
}

func AssessWithToolchainAt(ctx context.Context, cfg config.Config, root string) (map[string]Status, toolchain.Toolchain, error) {
	p := NewProbeAt(ctx, cfg, root)
	_, ninjaErr := exec.LookPath("ninja")
	results := map[string]Status{}
	var visit func(string) Status
	visiting := map[string]bool{}
	visit = func(name string) Status {
		if status, ok := results[name]; ok {
			return status
		}
		if visiting[name] {
			return Status{Ready: false, Reasons: []string{"target dependency cycle"}}
		}
		visiting[name] = true
		target := cfg.Targets[name]
		var reasons []string
		sources, sourceReasons := p.resolveSources(target.Sources)
		reasons = append(reasons, sourceReasons...)
		if target.Type != "shader" {
			for _, path := range append(append([]string{}, target.IncludeDirs...), target.PrivateIncludeDirs...) {
				if err := p.checkPath(path, true); err != nil {
					reasons = append(reasons, fmt.Sprintf("include directory is unavailable: %s (%v)", path, err))
				}
			}
			for _, path := range target.LibraryDirs {
				if err := p.checkPath(path, true); err != nil {
					reasons = append(reasons, fmt.Sprintf("library directory is unavailable: %s (%v)", path, err))
				}
			}
		}
		needsC, needsCUDA := false, false
		if target.Type != "shader" {
			for _, source := range sources {
				switch strings.ToLower(filepath.Ext(source)) {
				case ".c":
					needsC = true
				case ".cu":
					needsCUDA = true
				}
			}
		}
		if ninjaErr != nil {
			reasons = append(reasons, "host Ninja executable is unavailable")
		}
		mode, distribution := cfg.Toolchain.Mode, cfg.Toolchain.WSLDistribution
		match, err := condition.Evaluate(target.When, p.facts(mode, distribution))
		if err != nil {
			reasons = append(reasons, err.Error())
		} else if !match {
			reasons = append(reasons, "condition not met: "+target.When)
		}
		if target.RequiresWSL != "" && !p.hasWSL(target.RequiresWSL) {
			reasons = append(reasons, "WSL distribution unavailable: "+target.RequiresWSL)
		}
		if target.RequiresWSL != "" && (mode != "wsl" || target.RequiresWSL != distribution) {
			reasons = append(reasons, "requires WSL toolchain in "+target.RequiresWSL)
		}
		if mode == "wsl" && !p.hasWSL(distribution) {
			reasons = append(reasons, "configured WSL distribution unavailable: "+fallback(distribution, "default"))
		}
		var tc toolchain.Toolchain
		var tcErr error
		needsResolvedToolchain := target.Type != "shader" || cfg.Toolchain.CUDA != "" || cfg.Toolchain.CUDARoot != "" || cfg.Build.Modules
		if needsResolvedToolchain {
			tc, tcErr = p.resolveToolchain()
			if tcErr != nil {
				reasons = append(reasons, "configured toolchain unavailable: "+tcErr.Error())
			}
		}
		cCompiler := tc.CC
		if cCompiler == "" {
			cCompiler = tc.CXX
		}
		if needsC && tcErr == nil && !p.tool(mode, distribution, cCompiler) {
			reasons = append(reasons, "C compiler unavailable: "+fallback(cCompiler, "no C compiler resolved from the selected toolchain"))
		}
		if target.Type == "static" && tcErr == nil && !p.tool(mode, distribution, tc.Archiver) {
			reasons = append(reasons, "static target archiver unavailable: "+fallback(tc.Archiver, "no archiver resolved from the selected toolchain"))
		}
		linker := tc.Linker
		if linker == "" {
			linker = tc.CXX
		}
		if (target.Type == "shared" || target.Type == "executable" || target.Type == "test") && tcErr == nil && !p.tool(mode, distribution, linker) {
			reasons = append(reasons, "target linker unavailable: "+fallback(linker, "no linker resolved from the selected toolchain"))
		}
		cudaConfigured := cfg.Toolchain.CUDA != "" || cfg.Toolchain.CUDARoot != ""
		if needsCUDA && !cudaConfigured {
			reasons = append(reasons, "CUDA source requires [toolchain].cuda to pin a CUDA version range such as 12.0~12.9")
		}
		if cudaConfigured && tcErr == nil {
			if err := p.checkCUDA(tc); err != nil {
				reasons = append(reasons, "CUDA toolchain unavailable: "+err.Error())
			}
		}
		if target.Type == "shader" {
			if err := p.checkVulkan(target); err != nil {
				reasons = append(reasons, "Vulkan shader toolchain unavailable: "+err.Error())
			}
		}
		if cfg.Build.Modules {
			if tcErr == nil && tc.Kind != toolchain.Clang {
				reasons = append(reasons, "C++ modules require Clang; selected toolchain is "+string(tc.Kind))
			}
			if err := p.checkModuleScanner(); err != nil {
				reasons = append(reasons, "module scanner unavailable: "+err.Error())
			}
		}
		for _, name := range target.RequiresTools {
			if !p.tool(mode, distribution, name) {
				reasons = append(reasons, "missing "+mode+" tool: "+name)
			}
		}
		for _, dep := range target.Dependencies {
			if dep.Package != "" && !p.hasPackage(dep.Package) {
				reasons = append(reasons, "package unavailable: "+dep.Package+": "+p.packageErrors[dep.Package])
			}
			if dep.Target != "" {
				for _, reason := range visit(dep.Target).Reasons {
					reasons = append(reasons, "dependency "+dep.Target+": "+reason)
				}
			}
		}
		status := Status{Ready: len(reasons) == 0, Reasons: reasons}
		results[name] = status
		delete(visiting, name)
		return status
	}
	for name := range cfg.Targets {
		visit(name)
	}
	return results, p.toolchainResult, p.toolchainError
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}

func CheckSelected(statuses map[string]Status, names []string) error {
	var details []string
	for _, name := range names {
		for _, reason := range statuses[name].Reasons {
			details = append(details, name+": "+reason)
		}
	}
	if len(details) > 0 {
		return fmt.Errorf("E_TARGET_UNAVAILABLE: %s (use --force to attempt the build anyway)", strings.Join(details, "; "))
	}
	return nil
}
