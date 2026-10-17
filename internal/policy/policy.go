// Package policy evaluates a project's conditional rules and target
// readiness. It resolves [[rules]] against the local machine and reports which
// targets can currently build, caching repeated filesystem and toolchain probes.
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
	"trestle/internal/deps/conan"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/model"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
	"trestle/internal/toolchain/cuda"
)

// Status is the readiness verdict for one target, with the reasons it is not
// ready.
type Status struct {
	Ready   bool
	Reasons []string
}

// Change records a single resolved-rule difference, used to show what a
// conditional rule altered.
type Change struct {
	Field  string
	Before string
	After  string
}

// RuleResult reports how one [[rules]] entry resolved: whether its condition
// matched and what it changed.
type RuleResult struct {
	Index   int
	When    string
	Target  string
	Matched bool
	Changes []Change
}

// Report is the full outcome of applying rules: per-rule results, the total
// set of changes, and the resolved toolchain or the error that prevented
// resolving it.
type Report struct {
	Rules          []RuleResult
	Changes        []Change
	Toolchain      toolchain.Toolchain
	ToolchainError string
}

// Probe caches filesystem and toolchain checks so rule evaluation and
// readiness assessment share one consistent, no-repeat view of the machine.
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

// NewProbe returns a probe for cfg rooted at the current directory.
func NewProbe(ctx context.Context, cfg config.Config) *Probe {
	return NewProbeAt(ctx, cfg, ".")
}

// NewProbeAt returns a probe for cfg whose relative paths resolve against root.
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

// hasPackage dispatches on the configured provider: conan packages check the
// per-profile install output, everything else keeps the vcpkg resolution
// (including manual path-only packages).
func (p *Probe) hasPackage(name string) bool {
	if found, ok := p.packages[name]; ok {
		return found
	}
	pkg, ok := p.cfg.Packages[name]
	if !ok {
		p.packageErrors[name] = "package is not declared in [packages]"
		return false
	}
	var err error
	if pkg.Provider == "conan" {
		err = p.checkConanPackage(pkg)
	} else {
		err = p.checkVcpkgPackage(pkg)
	}
	if err != nil {
		p.packageErrors[name] = err.Error()
	}
	found := err == nil
	p.packages[name] = found
	return found
}

func (p *Probe) checkVcpkgPackage(pkg config.Package) error {
	if pkg.Triplet == "" || pkg.Triplet == "auto" {
		pkg.Triplet = p.cfg.Vcpkg.Triplet
	}
	root := p.cfg.Vcpkg.Root
	if root != "" && !filepath.IsAbs(root) {
		root = filepath.Join(p.root, root)
	}
	resolved, err := (vcpkg.Resolver{Root: root, Profile: p.cfg.Build.Profile, CRTLinkage: p.cfg.Vcpkg.CRTLinkage, LibraryLinkage: p.cfg.Vcpkg.LibraryLinkage}).Resolve(p.ctx, pkg)
	if err != nil {
		return err
	}
	if resolved.Method == vcpkg.ResolutionManual {
		return p.checkManualPackagePaths(pkg)
	}
	return p.checkPackageUsagePaths(resolved.Usage)
}

func (p *Probe) checkConanPackage(pkg config.Package) error {
	if hasConanManualMetadata(pkg) {
		return p.checkManualPackagePaths(pkg)
	}
	buildRoot := p.cfg.Build.BuildDir
	if buildRoot != "" && !filepath.IsAbs(buildRoot) {
		buildRoot = filepath.Join(p.root, buildRoot)
	}
	resolved, err := (conan.Resolver{Root: p.cfg.Conan.Root, BuildRoot: filepath.Dir(buildRoot), Profile: p.cfg.Build.Profile}).Resolve(p.ctx, pkg)
	if err != nil {
		return err
	}
	if resolved.Method == conan.ResolutionManual {
		return p.checkManualPackagePaths(pkg)
	}
	return p.checkPackageUsagePaths(resolved.Usage)
}

func hasConanManualMetadata(pkg config.Package) bool {
	return len(pkg.IncludeDirs) > 0 || len(pkg.LibraryDirs) > 0 || len(pkg.Libraries) > 0 || len(pkg.Defines) > 0
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
		return p.probeWSLPath(distribution, path, directory)
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

// probeWSLPath checks for a file or directory on the Linux side of a WSL
// distribution, where the path can only be tested through a shell inside it.
func (p *Probe) probeWSLPath(distribution, path string, directory bool) error {
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
	// Detection runs several child tools: the cached inventory may be cold,
	// Visual Studio discovery queries vswhere, and the MSVC environment waits
	// on vcvars64.bat. On a loaded machine that chain needs real headroom;
	// cutting it short used to surface as a bogus "no Visual Studio
	// environment" message, so the budget stays generous and an overrun is
	// reported as a timeout instead.
	ctx, cancel := context.WithTimeout(p.ctx, 20*time.Second)
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

// ApplyAt resolves conditional rules against a project rooted at root,
// mirroring Apply for callers that already know the root.
func ApplyAt(ctx context.Context, source config.Config, root string) (config.Config, error) {
	cfg, _, err := ApplyWithReportAt(ctx, source, root)
	return cfg, err
}

// ApplyWithReportAt resolves rules and returns a detailed report of matched
// rules and changes alongside the resulting configuration.
func ApplyWithReportAt(ctx context.Context, source config.Config, root string) (config.Config, Report, error) {
	cfg := cloneConfigForRules(source)
	report := Report{}
	p := NewProbeAt(ctx, cfg, root)
	for index, rule := range source.Rules {
		result := RuleResult{Index: index + 1, When: rule.When, Target: rule.Target}
		match, err := condition.Evaluate(rule.When, p.facts(cfg.Toolchain.Mode, cfg.Toolchain.WSLDistribution))
		if err != nil {
			return cfg, report, fmt.Errorf("rule %d: %w", index+1, err)
		}
		result.Matched = match
		if match {
			before := ruleValues(cfg)
			applyRule(&cfg, rule)
			result.Changes = diffRuleValues(before, ruleValues(cfg))
		}
		report.Rules = append(report.Rules, result)
	}
	if err := config.Validate(cfg); err != nil {
		return cfg, report, fmt.Errorf("resolved rules: %w", err)
	}
	report.Changes = diffRuleValues(ruleValues(source), ruleValues(cfg))
	return cfg, report, nil
}

// cloneConfigForRules copies the slices a rule can append to, so applying rules
// never mutates the caller's configuration.
func cloneConfigForRules(source config.Config) config.Config {
	cfg := source
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
	return cfg
}

// applyRule mutates cfg with one matched rule's compiler, mode and flag
// overrides, plus its target-scoped flags, packages and dependencies.
func applyRule(cfg *config.Config, rule config.Rule) {
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
			if !hasPackageDependency(target.Dependencies, name) {
				target.Dependencies = append(target.Dependencies, config.Dependency{Package: name, Scope: "private"})
			}
		}
		for _, name := range rule.DependsOn {
			if !hasTargetDependency(target.Dependencies, name) {
				target.Dependencies = append(target.Dependencies, config.Dependency{Target: name, Scope: "private"})
			}
		}
		cfg.Targets[rule.Target] = target
	}
	if len(rule.Targets) > 0 {
		cfg.Build.DefaultTargets = append([]string(nil), rule.Targets...)
	}
}

// hasPackageDependency reports whether dependencies already contains a package
// dependency on name, keeping rule application idempotent.
func hasPackageDependency(dependencies []config.Dependency, name string) bool {
	for _, dep := range dependencies {
		if dep.Package == name {
			return true
		}
	}
	return false
}

// hasTargetDependency reports whether dependencies already contains a target
// dependency on name, keeping rule application idempotent.
func hasTargetDependency(dependencies []config.Dependency, name string) bool {
	for _, dep := range dependencies {
		if dep.Target == name {
			return true
		}
	}
	return false
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

// Assess reports whether each target in cfg is ready to build, evaluating from
// the current directory.
func Assess(ctx context.Context, cfg config.Config) map[string]Status {
	return AssessAt(ctx, cfg, ".")
}

// AssessAt reports target readiness with relative paths resolved against root.
func AssessAt(ctx context.Context, cfg config.Config, root string) map[string]Status {
	statuses, _, _ := AssessWithToolchainAt(ctx, cfg, root)
	return statuses
}

// AssessWithToolchainAt reports target readiness and also returns the resolved
// toolchain, or the error that prevented resolving it, so callers can reuse it.
func AssessWithToolchainAt(ctx context.Context, cfg config.Config, root string) (map[string]Status, toolchain.Toolchain, error) {
	p := NewProbeAt(ctx, cfg, root)
	_, ninjaErr := exec.LookPath("ninja")
	a := &assessor{probe: p, cfg: cfg, ninjaErr: ninjaErr, results: map[string]Status{}, visiting: map[string]bool{}}
	for name := range cfg.Targets {
		a.visit(name)
	}
	return a.results, p.toolchainResult, p.toolchainError
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}

// CheckSelected returns an error naming every reason the selected targets are
// not ready, or nil when all are ready.
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
