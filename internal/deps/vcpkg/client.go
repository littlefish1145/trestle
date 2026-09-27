package vcpkg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Port struct {
	Name         string
	Version      string
	Description  string
	Installed    bool
	Source       string
	Homepage     string
	PackageURL   string
	License      string
	Updated      string
	Features     []string
	Dependencies []string
}

type Client struct {
	Executable  string
	Root        string
	Environment map[string]string
}

func NewClient(root string) (Client, error) {
	if root == "" {
		root = os.Getenv("VCPKG_ROOT")
	}
	if root == "" {
		if executable, err := exec.LookPath("vcpkg"); err == nil {
			absolute, absErr := filepath.Abs(executable)
			if absErr == nil {
				executable = absolute
			}
			return Client{Executable: executable, Root: filepath.Dir(executable)}, nil
		}
	}
	if root == "" {
		return Client{}, fmt.Errorf("E_VCPKG_ROOT: put vcpkg on PATH, set VCPKG_ROOT, or configure [vcpkg].root")
	}
	executable := filepath.Join(root, "vcpkg")
	if _, err := os.Stat(executable); err != nil {
		executable = filepath.Join(root, "vcpkg.exe")
	}
	if _, err := os.Stat(executable); err != nil {
		return Client{}, fmt.Errorf("E_VCPKG_EXECUTABLE: vcpkg executable not found under %s", root)
	}
	return Client{Executable: executable, Root: root}, nil
}

func (client Client) Search(ctx context.Context, query string) ([]string, error) {
	command := exec.CommandContext(ctx, client.Executable, "search", query)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("vcpkg search failed: %s", strings.TrimSpace(string(output)))
	}
	return parseSearchOutput(string(output)), nil
}

func parseSearchOutput(output string) []string {
	var result []string
	for _, rawLine := range strings.Split(output, "\n") {
		// Feature matches are printed as indented continuation rows. They are
		// metadata for the preceding port, not independently installable ports.
		if rawLine != "" && (rawLine[0] == ' ' || rawLine[0] == '\t') {
			continue
		}
		line := strings.TrimSpace(rawLine)
		if line != "" && !strings.HasPrefix(line, "The following ports") {
			result = append(result, line)
		}
	}
	return result
}

func (client Client) SearchPorts(ctx context.Context, query string) ([]Port, error) {
	lines, err := client.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make([]Port, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "The") {
			continue
		}
		port := Port{Name: fields[0], Installed: client.IsInstalled(fields[0]), Source: "local"}
		if len(fields) > 1 {
			port.Version = strings.TrimSuffix(fields[1], "#")
		}
		if len(fields) > 2 {
			port.Description = strings.Join(fields[2:], " ")
		}
		client.enrichPort(&port)
		result = append(result, port)
	}
	return result, nil
}

func (client Client) enrichPort(port *Port) {
	port.PackageURL = "https://vcpkg.io/en/package/" + port.Name + ".html"
	path := filepath.Join(client.Root, "ports", port.Name, "vcpkg.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var manifest struct {
		Description  json.RawMessage            `json:"description"`
		Homepage     string                     `json:"homepage"`
		License      string                     `json:"license"`
		Features     map[string]json.RawMessage `json:"features"`
		Dependencies json.RawMessage            `json:"dependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return
	}
	if description := decodeText(manifest.Description); description != "" {
		port.Description = description
	}
	port.Homepage, port.License = manifest.Homepage, manifest.License
	for name := range manifest.Features {
		port.Features = append(port.Features, name)
	}
	sort.Strings(port.Features)
	port.Dependencies = decodeNames(manifest.Dependencies)
}

func (client Client) IsInstalled(port string) bool {
	manifests, _ := filepath.Glob(filepath.Join(client.Root, "installed", "vcpkg", "info", port+"_*.list"))
	if len(manifests) > 0 {
		return true
	}
	shareDirs, _ := filepath.Glob(filepath.Join(client.Root, "installed", "*", "share", port))
	for _, directory := range shareDirs {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func (client Client) Install(ctx context.Context, ports []string, triplet string) error {
	_, err := client.install(ctx, ports, triplet, true)
	return err
}

// InstallCapture installs ports without writing into an active TUI alternate
// screen. The returned tail is suitable for a completion or error summary.
func (client Client) InstallCapture(ctx context.Context, ports []string, triplet string) (string, error) {
	return client.install(ctx, ports, triplet, false)
}

// InstallWithProgress emits complete stdout/stderr lines while vcpkg downloads
// and builds. The callback may be called from either output reader.
func (client Client) InstallWithProgress(ctx context.Context, ports []string, triplet string, progress func(string)) error {
	args := []string{"install"}
	args = append(args, ports...)
	if triplet != "" && triplet != "auto" {
		args = append(args, "--triplet", triplet)
	}
	command := exec.CommandContext(ctx, client.Executable, args...)
	command.Env = client.environment()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("E_VCPKG_INSTALL: %w", err)
	}
	var wait sync.WaitGroup
	var callbackLock sync.Mutex
	readErrors := make(chan error, 2)
	outputLines := make([]string, 0, 128)
	read := func(reader *bufio.Reader) {
		defer wait.Done()
		for {
			text, readErr := reader.ReadString('\n')
			line := strings.TrimRight(text, "\r\n")
			if line == "" {
				if readErr == io.EOF {
					return
				}
				if readErr != nil {
					if readErr != io.EOF {
						readErrors <- readErr
					}
					return
				}
				continue
			}
			callbackLock.Lock()
			outputLines = append(outputLines, line)
			if progress != nil {
				progress(line)
			}
			callbackLock.Unlock()
			if readErr != nil {
				if readErr != io.EOF {
					readErrors <- readErr
				}
				return
			}
		}
	}
	wait.Add(2)
	go read(bufio.NewReader(stdout))
	go read(bufio.NewReader(stderr))
	wait.Wait()
	close(readErrors)
	commandErr := command.Wait()
	for readErr := range readErrors {
		return fmt.Errorf("E_VCPKG_INSTALL: read output: %w", readErr)
	}
	if err := commandErr; err != nil {
		callbackLock.Lock()
		detail := strings.Join(outputLines, "\n")
		callbackLock.Unlock()
		if detail != "" {
			return fmt.Errorf("E_VCPKG_INSTALL: %s", tail(detail, 8))
		}
		return fmt.Errorf("E_VCPKG_INSTALL: %w", err)
	}
	return nil
}

func (client Client) install(ctx context.Context, ports []string, triplet string, stream bool) (string, error) {
	args := []string{"install"}
	args = append(args, ports...)
	if triplet != "" && triplet != "auto" {
		args = append(args, "--triplet", triplet)
	}
	command := exec.CommandContext(ctx, client.Executable, args...)
	command.Env = client.environment()
	var output bytes.Buffer
	if stream {
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
	} else {
		command.Stdout = &output
		command.Stderr = &output
	}
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(output.String())
		if detail != "" {
			return detail, fmt.Errorf("E_VCPKG_INSTALL: %s", detail)
		}
		return "", fmt.Errorf("E_VCPKG_INSTALL: vcpkg install failed: %w", err)
	}
	return tail(strings.TrimSpace(output.String()), 4), nil
}

func (client Client) environment() []string {
	if len(client.Environment) == 0 {
		return nil
	}
	result := make([]string, 0, len(os.Environ())+len(client.Environment))
	overrides := make(map[string]bool, len(client.Environment))
	for key := range client.Environment {
		overrides[strings.ToUpper(key)] = true
	}
	for _, entry := range os.Environ() {
		key := entry
		if offset := strings.IndexByte(entry, '='); offset >= 0 {
			key = entry[:offset]
		}
		if overrides[strings.ToUpper(key)] {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range client.Environment {
		result = append(result, key+"="+value)
	}
	return result
}

func tail(value string, lines int) string {
	parts := strings.Split(value, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
