// Package vcpkg integrates trestle with a local vcpkg checkout: searching
// installed and available ports, resolving a port's usage requirements from
// pkg-config, built-in adapters or install manifests, and installing ports into
// a triplet.
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
	"trestle/internal/fsx"
	"trestle/internal/processx"
)

// Port is one package known to vcpkg, as returned by search or read from the
// local port tree, with optional metadata such as its homepage and features.
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

// Client runs a local vcpkg executable. Executable is the resolved binary, Root
// is the checkout it belongs to, and Environment adds or overrides variables for
// every command it runs.
type Client struct {
	Executable  string
	Root        string
	Environment map[string]string
}

// NewClient locates a vcpkg installation, preferring root, then VCPKG_ROOT, then
// vcpkg on PATH. It returns an error when no executable can be found.
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

// Search runs vcpkg search and returns the raw result lines, one port per line,
// with header and feature-continuation rows removed.
func (client Client) Search(ctx context.Context, query string) ([]string, error) {
	command := processx.Command(ctx, client.Executable, "search", query)
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

// SearchPorts runs a search and parses each result line into a Port, filling in
// version, description and metadata from the local port manifest.
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

// IsInstalled reports whether a port appears installed for any triplet, checking
// both the install manifests and the installed share directories.
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

// Install installs ports into triplet, streaming vcpkg's output to the terminal.
// An empty or "auto" triplet defers to vcpkg's own default.
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
	lock, err := fsx.TryLock(ctx, filepath.Join(client.Root, "installed"), "vcpkg install")
	if err != nil {
		return err
	}
	defer lock.Close()
	command := processx.Command(ctx, client.Executable, installArgs(ports, triplet)...)
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
	stream := &installStream{lines: make([]string, 0, 128), readErr: make(chan error, 2), progress: progress}
	var wait sync.WaitGroup
	wait.Add(2)
	var outReader, errReader io.Reader = stdout, stderr
	if raw := processx.Output(ctx); raw != nil {
		// The two stream readers tee into the context sink concurrently, so the
		// sink only sees valid Write calls when they are serialized here.
		tee := processx.Sync(raw)
		outReader, errReader = io.TeeReader(stdout, tee), io.TeeReader(stderr, tee)
		stream.progress = processx.Notify(ctx, stream.progress)
	}
	go readInstallStream(bufio.NewReader(outReader), stream, &wait)
	go readInstallStream(bufio.NewReader(errReader), stream, &wait)
	wait.Wait()
	close(stream.readErr)
	commandErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for readErr := range stream.readErr {
		return fmt.Errorf("E_VCPKG_INSTALL: read output: %w", readErr)
	}
	if err := commandErr; err != nil {
		detail := stream.detail()
		if detail != "" {
			return fmt.Errorf("E_VCPKG_INSTALL: %s", tail(detail, 8))
		}
		return fmt.Errorf("E_VCPKG_INSTALL: %w", err)
	}
	return nil
}

// installStream collects the complete output lines of a running vcpkg install
// while forwarding each one to a progress callback. Its stdout and stderr
// readers use it concurrently.
type installStream struct {
	lock     sync.Mutex
	lines    []string
	readErr  chan error
	progress func(string)
}

// append records one complete line, trims the history to the newest 128 lines
// and forwards the line to the progress callback.
func (stream *installStream) append(line string) {
	stream.lock.Lock()
	defer stream.lock.Unlock()
	stream.lines = append(stream.lines, line)
	if len(stream.lines) > 128 {
		stream.lines = stream.lines[len(stream.lines)-128:]
	}
	if stream.progress != nil {
		stream.progress(line)
	}
}

// detail returns the buffered output lines as a newline-joined string.
func (stream *installStream) detail() string {
	stream.lock.Lock()
	defer stream.lock.Unlock()
	return strings.Join(stream.lines, "\n")
}

// readInstallStream drains one of a command's output pipes, forwarding each
// complete line to the stream and reporting a read failure on its channel. It
// marks one wait-group entry done when it returns.
func readInstallStream(reader *bufio.Reader, stream *installStream, wait *sync.WaitGroup) {
	defer wait.Done()
	for {
		text, readErr := reader.ReadString('\n')
		line := strings.TrimRight(processx.DecodeOutput([]byte(text)), "\r\n")
		if text == "" {
			if readErr == io.EOF {
				return
			}
			if readErr != nil {
				if readErr != io.EOF {
					stream.readErr <- readErr
				}
				return
			}
			continue
		}
		stream.append(line)
		if readErr != nil {
			if readErr != io.EOF {
				stream.readErr <- readErr
			}
			return
		}
	}
}

// installArgs builds the vcpkg install argument list, adding --triplet when a
// concrete triplet is requested.
func installArgs(ports []string, triplet string) []string {
	args := []string{"install"}
	args = append(args, ports...)
	if triplet != "" && triplet != "auto" {
		args = append(args, "--triplet", triplet)
	}
	return args
}

func (client Client) install(ctx context.Context, ports []string, triplet string, stream bool) (string, error) {
	lock, err := fsx.TryLock(ctx, filepath.Join(client.Root, "installed"), "vcpkg install")
	if err != nil {
		return "", err
	}
	defer lock.Close()
	command := processx.Command(ctx, client.Executable, installArgs(ports, triplet)...)
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
