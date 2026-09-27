package policy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"trestle/internal/condition"
	"trestle/internal/config"
	"trestle/internal/deps/vcpkg"
	"trestle/internal/toolchain"
)

type Status struct {
	Ready   bool
	Reasons []string
}

type Probe struct {
	ctx             context.Context
	cfg             config.Config
	packages        map[string]bool
	packageErrors   map[string]string
	tools           map[string]bool
	wsl             map[string]bool
	compilerChecked bool
	compilerReady   bool
	compilerError   string
}

func NewProbe(ctx context.Context, cfg config.Config) *Probe {
	return &Probe{ctx: ctx, cfg: cfg, packages: map[string]bool{}, packageErrors: map[string]string{}, tools: map[string]bool{}, wsl: map[string]bool{}}
}

func (p *Probe) facts(mode, distribution string) condition.Facts {
	return condition.Facts{OS: runtime.GOOS, Arch: runtime.GOARCH, Mode: mode,
		Tool: func(name string) bool { return p.tool(mode, distribution, name) },
		WSL:  p.hasWSL, Package: p.hasPackage,
		WSLTool: func(distribution, name string) bool { return p.tool("wsl", distribution, name) },
		Env:     os.Getenv,
		Path:    func(path string) bool { _, err := os.Stat(path); return err == nil },
	}
}

func (p *Probe) tool(mode, distribution, name string) bool {
	key := mode + "|" + distribution + "|" + name
	if found, ok := p.tools[key]; ok {
		return found
	}
	var found bool
	if mode == "wsl" {
		ctx, cancel := context.WithTimeout(p.ctx, 4*time.Second)
		defer cancel()
		_, err := toolchain.WSLExecutable(ctx, distribution, name)
		found = err == nil
	} else {
		_, err := exec.LookPath(name)
		found = err == nil
	}
	p.tools[key] = found
	return found
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
	_, err := (vcpkg.Resolver{Root: p.cfg.Vcpkg.Root, Profile: p.cfg.Build.Profile, CRTLinkage: p.cfg.Vcpkg.CRTLinkage, LibraryLinkage: p.cfg.Vcpkg.LibraryLinkage}).Resolve(p.ctx, pkg)
	if err != nil {
		p.packageErrors[name] = err.Error()
	}
	found := err == nil
	p.packages[name] = found
	return found
}

func (p *Probe) hasCompiler(mode, distribution, requested string) bool {
	if p.compilerChecked {
		return p.compilerReady
	}
	p.compilerChecked = true
	ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
	defer cancel()
	var err error
	if mode == "wsl" {
		_, err = toolchain.DetectWSL(ctx, distribution, requested)
	} else {
		detector := toolchain.NewDetector()
		setup := p.cfg.Toolchain.Setup
		if toolchain.NeedsMSVCEnvironment(ctx, requested) {
			setup = toolchain.ResolveMSVCSetup(ctx, setup, p.cfg.Toolchain.Archiver, p.cfg.Toolchain.Linker, p.cfg.Toolchain.C, requested)
		}
		if setup != "" {
			if setupDetector, ok := detector.(interface {
				DetectWithSetup(context.Context, string, string) (toolchain.Toolchain, error)
			}); ok {
				_, err = setupDetector.DetectWithSetup(ctx, requested, setup)
			} else {
				_, err = detector.Detect(ctx, requested)
			}
		} else {
			_, err = detector.Detect(ctx, requested)
		}
	}
	p.compilerReady = err == nil
	if err != nil {
		p.compilerError = err.Error()
	}
	return p.compilerReady
}

// Apply resolves conditional declarations in order, without changing the TOML.
func Apply(ctx context.Context, source config.Config) (config.Config, error) {
	cfg := source
	cfg.Targets = make(map[string]config.Target, len(source.Targets))
	for name, target := range source.Targets {
		target.Dependencies = append([]config.Dependency(nil), target.Dependencies...)
		target.CompileOptions = append([]string(nil), target.CompileOptions...)
		cfg.Targets[name] = target
	}
	cfg.Build.CompileFlags = append([]string(nil), source.Build.CompileFlags...)
	cfg.Build.CFlags = append([]string(nil), source.Build.CFlags...)
	cfg.Build.CXXFlags = append([]string(nil), source.Build.CXXFlags...)
	cfg.Build.LinkFlags = append([]string(nil), source.Build.LinkFlags...)
	p := NewProbe(ctx, cfg)
	for index, rule := range source.Rules {
		match, err := condition.Evaluate(rule.When, p.facts(cfg.Toolchain.Mode, cfg.Toolchain.WSLDistribution))
		if err != nil {
			return cfg, fmt.Errorf("rule %d: %w", index+1, err)
		}
		if !match {
			continue
		}
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
		}
		cfg.Build.CFlags = append(cfg.Build.CFlags, rule.CFlags...)
		cfg.Build.CXXFlags = append(cfg.Build.CXXFlags, rule.CXXFlags...)
		cfg.Build.LinkFlags = append(cfg.Build.LinkFlags, rule.LinkFlags...)
		if rule.Target != "" {
			target := cfg.Targets[rule.Target]
			target.CompileOptions = append(target.CompileOptions, rule.CompileFlags...)
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
	}
	if err := config.Validate(cfg); err != nil {
		return cfg, fmt.Errorf("resolved rules: %w", err)
	}
	return cfg, nil
}

func Assess(ctx context.Context, cfg config.Config) map[string]Status {
	p := NewProbe(ctx, cfg)
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
		if target.Type == "shader" {
			shaderMode, shaderDistribution := cfg.Toolchain.VulkanExecution, cfg.Toolchain.VulkanWSLDistribution
			if shaderMode == "" {
				shaderMode = "native"
			}
			if shaderDistribution == "" {
				shaderDistribution = distribution
			}
			if target.ShaderTool != "" {
				if !p.tool(shaderMode, shaderDistribution, target.ShaderTool) {
					reasons = append(reasons, "shader compiler unavailable: "+target.ShaderTool+" ("+shaderMode+")")
				}
			} else if !p.tool(shaderMode, shaderDistribution, "glslc") && !p.tool(shaderMode, shaderDistribution, "glslangValidator") {
				reasons = append(reasons, "no glslc or glslangValidator shader compiler in "+shaderMode)
			}
		}
		if target.Type != "shader" && !p.hasCompiler(mode, distribution, cfg.Toolchain.CXX) {
			reasons = append(reasons, "no usable "+mode+" C/C++ compiler (configured: "+fallback(cfg.Toolchain.CXX, "auto")+"): "+p.compilerError)
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
	return results
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
