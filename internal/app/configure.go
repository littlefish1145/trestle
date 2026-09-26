package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/huh/v2"
	"golang.org/x/term"
	"trestle/internal/config"
	"trestle/internal/sdk/vulkan"
	"trestle/internal/toolchain"
)

func ConfigureInteractive(path string, input *os.File, output *os.File) error {
	cfg, err := ensureProjectConfig(path)
	if err != nil {
		return err
	}
	projectDetected := hasExecutableTarget(cfg)
	fmt.Fprintf(output, "%s C++ project detected\n", status(projectDetected))
	candidates := toolchain.List(context.Background())
	selected := selectCandidate(cfg, candidates)
	if selected != nil {
		fmt.Fprintf(output, "%s %s detected\n", status(true), toolchainLabel(*selected))
	} else {
		fmt.Fprintf(output, "%s supported compiler not detected\n", status(false))
	}
	_, vulkanErr := vulkan.Detect()
	fmt.Fprintf(output, "%s Vulkan SDK detected\n", status(vulkanErr == nil))
	_, ninjaErr := exec.LookPath("ninja")
	fmt.Fprintf(output, "%s Ninja detected\n\n", status(ninjaErr == nil))
	if selected != nil {
		if cfg.Toolchain.CXX == "auto" || cfg.Toolchain.CXX == "" {
			cfg.Toolchain.CXX = selected.CXX
		}
		if cfg.Toolchain.C == "auto" || cfg.Toolchain.C == "" {
			cfg.Toolchain.C = selected.CC
		}
		if cfg.Toolchain.Archiver == "" || cfg.Toolchain.Archiver == "auto" {
			cfg.Toolchain.Archiver = selected.Archiver
		}
		if cfg.Toolchain.Linker == "" || cfg.Toolchain.Linker == "auto" {
			cfg.Toolchain.Linker = selected.Linker
		}
	}
	reader := bufio.NewReader(input)
	createExecutable := true
	compileShaders := cfg.Build.AutoCompileShaders
	if !term.IsTerminal(int(input.Fd())) {
		createExecutable = ask(reader, output, fmt.Sprintf("Create executable %q?", cfg.Project.Name))
		compileShaders = ask(reader, output, "Compile shaders automatically?")
	} else {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().Title(fmt.Sprintf("Create executable %q?", cfg.Project.Name)).Value(&createExecutable),
				huh.NewConfirm().Title("Compile shaders automatically?").Value(&compileShaders),
			),
		).WithInput(input).WithOutput(output).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
		if err := form.Run(); err != nil {
			return err
		}
	}
	if createExecutable {
		if _, exists := cfg.Targets["app"]; !exists {
			cfg.Targets["app"] = config.Target{Type: "executable", Sources: []string{"src/main.cpp"}, OutputName: cfg.Project.Name}
		}
	}
	cfg.Build.AutoCompileShaders = compileShaders
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(output, "\nGenerated %s\n", config.DefaultFileName)
	return nil
}

func hasExecutableTarget(cfg config.Config) bool {
	for _, target := range cfg.Targets {
		if target.Type == "executable" || target.Type == "test" {
			return true
		}
	}
	return false
}

func selectCandidate(cfg config.Config, candidates []toolchain.Toolchain) *toolchain.Toolchain {
	if cfg.Toolchain.CXX != "" && cfg.Toolchain.CXX != "auto" {
		for i := range candidates {
			if strings.EqualFold(candidates[i].CXX, cfg.Toolchain.CXX) || strings.Contains(strings.ToLower(string(candidates[i].Kind)), strings.ToLower(cfg.Toolchain.CXX)) {
				return &candidates[i]
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return &candidates[0]
}

func toolchainLabel(candidate toolchain.Toolchain) string {
	if candidate.Kind == toolchain.MSVC {
		return fmt.Sprintf("MSVC %s", candidate.Version)
	}
	return fmt.Sprintf("%s %s", strings.ToUpper(string(candidate.Kind)), candidate.Version)
}

func status(value bool) string {
	if value {
		return "✓"
	}
	return "✗"
}

func ask(reader *bufio.Reader, output *os.File, question string) bool {
	fmt.Fprintf(output, "%s\n> Yes\n", question)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return true
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes"
}
