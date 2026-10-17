// Package conan implements the Conan 2 package-manager provider. The CLI shape
// differs from vcpkg: recipes are references (name/version[@user/channel]),
// features are bracketed options (name[opt1,opt2]), and generators write CMake
// integration files into a build folder instead of a global install tree.
package conan

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
	"strconv"
	"strings"
	"sync"
	"time"
	"trestle/internal/fsx"
	"trestle/internal/processx"
)

// Package mirrors the vcpkg Port record so the shared Packages page can render
// both providers without special cases.
type Package struct {
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

// Client drives the conan executable for one project: its install location,
// the folder holding per-profile install outputs, the active profile, and the
// settings, options, and environment that shape every invocation.
type Client struct {
	Executable  string
	Root        string
	BuildRoot   string
	Profile     string
	Settings    map[string]string
	Options     map[string]string
	Environment map[string]string
}

// NewClient resolves the conan installation the same way vcpkg is resolved:
// explicit configuration, then CONAN_HOME, then the executable on PATH. Conan
// keeps its cache under CONAN_HOME rather than an install prefix, so only the
// executable and its home are required.
func NewClient(conanRoot, buildRoot string) (Client, error) {
	if conanRoot == "" {
		conanRoot = os.Getenv("CONAN_HOME")
	}
	if conanRoot == "" && executableFromPath() == "" {
		return Client{}, fmt.Errorf("E_CONAN_ROOT: configure [conan].root, set CONAN_HOME, or put conan on PATH")
	}
	executable := filepath.Join(conanRoot, "conan")
	if conanRoot != "" {
		if _, err := os.Stat(executable); err != nil {
			executable += ".exe"
		}
		if _, err := os.Stat(executable); err != nil {
			executable = executableFromPath()
		}
	} else {
		executable = executableFromPath()
	}
	if executable == "" {
		return Client{}, fmt.Errorf("E_CONAN_EXECUTABLE: conan executable not found under %s", conanRoot)
	}
	root := conanRoot
	if root == "" {
		root = filepath.Dir(executable)
	}
	client := Client{Executable: executable, Root: root, BuildRoot: buildRoot}
	if profile, err := client.DefaultProfile(context.Background()); err == nil {
		client.Profile = profile
	}
	return client, nil
}

func executableFromPath() string {
	executable, err := exec.LookPath("conan")
	if err != nil {
		return ""
	}
	absolute, absErr := filepath.Abs(executable)
	if absErr != nil {
		return executable
	}
	return absolute
}

// DefaultProfile returns the profile conan would use for a plain install.
func (client Client) DefaultProfile(ctx context.Context) (string, error) {
	stdout, err := client.run(ctx, "E_CONAN_PROFILE", nil, "profile", "path", "default")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(stdout))
	if path == "" {
		return "", fmt.Errorf("E_CONAN_PROFILE: conan reported no default profile")
	}
	return path, nil
}

func (client Client) run(ctx context.Context, code string, progress func(string), args ...string) ([]byte, error) {
	command := processx.Command(ctx, client.Executable, args...)
	command.Env = client.environment()
	var output bytes.Buffer
	if progress != nil {
		command.Stdout = io.MultiWriter(&output, writerFunc(func(data []byte) (int, error) {
			if line := strings.TrimRight(processx.DecodeOutput(data), "\r\n"); strings.TrimSpace(line) != "" {
				progress(line)
			}
			return len(data), nil
		}))
		command.Stderr = command.Stdout
	} else {
		command.Stdout = &output
		command.Stderr = &output
	}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: conan %s failed: %s", code, strings.Join(args, " "), strings.TrimSpace(output.String()))
	}
	return output.Bytes(), nil
}

type writerFunc func([]byte) (int, error)

// Write calls the wrapped function, letting it act as an io.Writer.
func (fn writerFunc) Write(data []byte) (int, error) { return fn(data) }

// Search looks recipe references up in conan and adapts them to the neutral
// record. An empty remote list searches the local cache; otherwise every
// configured remote is queried, so the Packages page can offer both installed
// recipes and versions available upstream.
func (client Client) Search(ctx context.Context, query string) ([]Package, error) {
	return client.SearchRemotes(ctx, query, nil)
}

// SearchRemotes searches the local cache when remotes is empty, or the named
// remotes otherwise. An empty list means "every configured remote".
func (client Client) SearchRemotes(ctx context.Context, query string, remotes []string) ([]Package, error) {
	if query == "" {
		query = "*"
	}
	var packages []Package
	var failures []string
	if len(remotes) == 0 {
		stdout, err := client.run(ctx, "E_CONAN_LIST", nil, "list", query, "--format=json")
		if err != nil {
			return nil, err
		}
		local, err := parseListOutput(stdout, "local")
		if err != nil {
			return nil, err
		}
		return local, nil
	}
	for _, remote := range remotes {
		stdout, err := client.run(ctx, "E_CONAN_LIST", nil, "list", query, "--format=json", "-r="+remote)
		if err != nil {
			failures = append(failures, remote+": "+err.Error())
			continue
		}
		found, err := parseListOutput(stdout, remote)
		if err != nil {
			failures = append(failures, remote+": "+err.Error())
			continue
		}
		packages = append(packages, found...)
	}
	if len(packages) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return packages, nil
}

// Remotes lists the remotes conan is configured to use, in configured order.
func (client Client) Remotes(ctx context.Context) ([]string, error) {
	stdout, err := client.run(ctx, "E_CONAN_REMOTE", nil, "remote", "list")
	if err != nil {
		return nil, err
	}
	return parseRemoteList(stdout), nil
}

// parseRemoteList reads the `conan remote list` table, where each row is
// "name: url [Verify SSL: ..., Enabled: ...]" and the first row is a header.
func parseRemoteList(stdout []byte) []string {
	var remotes []string
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "REMOTE") || strings.HasPrefix(line, "Find remotes") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if name == "" || strings.ContainsAny(name, "/ \t") {
			continue
		}
		remotes = append(remotes, name)
	}
	return remotes
}

// listEnvelope accepts both shapes `conan list --format=json` emits: a
// "Local Cache" object for cache lookups, and a top-level object keyed by
// remote name. Every top-level key other than the cache marker is a remote,
// which is how conan reports a remote search:
//
//	{"conancenter": {"fmt/11.1.4": {}, "zlib/1.3.1": {"revisions": {...}}}}
//
// Each remote nests the full reference (name/version) directly above its
// revisions, so a recipe with no revision detail is simply an empty object.
type listEnvelope map[string]map[string]conanReference

type conanReference struct {
	Revisions map[string]conanRevision `json:"revisions"`
}

type conanRevision struct {
	Timestamp conanTimestamp `json:"timestamp"`
}

// conanTimestamp reads the epoch-seconds float conan writes, and tolerates an
// ISO string so a future format change degrades to "no date" instead of
// failing the whole search.
type conanTimestamp struct {
	Seconds float64
	Text    string
}

// MarshalJSON writes the timestamp back in the shape conan expects, preferring
// the original text when one was parsed.
func (value conanTimestamp) MarshalJSON() ([]byte, error) {
	if value.Text != "" {
		return json.Marshal(value.Text)
	}
	return json.Marshal(value.Seconds)
}

// UnmarshalJSON accepts either the epoch-seconds number or an ISO string,
// recording whichever form conan sent.
func (value *conanTimestamp) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if text == "" || text == "null" {
		return nil
	}
	if seconds, err := strconv.ParseFloat(text, 64); err == nil {
		value.Seconds = seconds
		return nil
	}
	value.Text = text
	return nil
}

// Format renders the timestamp as a date, or "" when conan sent none.
// Format renders the timestamp as a date, or "" when conan sent none.
func (value conanTimestamp) Format() string {
	if value.Text != "" {
		return value.Text
	}
	if value.Seconds <= 0 {
		return ""
	}
	return time.Unix(int64(value.Seconds), 0).UTC().Format("2006-01-02")
}

// parseListOutput decodes `conan list --format=json` output. The source
// argument is the fallback label when the payload carries no recognizable
// top-level key; keys otherwise win, so "Local Cache" maps to the localSource
// marker and any remote name maps to itself.
func parseListOutput(data []byte, source string) ([]Package, error) {
	parsed, err := decodeListEnvelope(data)
	if err != nil {
		return nil, err
	}
	entries := packagesFromEnvelope(parsed, source)
	sortPackages(entries)
	return entries, nil
}

// decodeListEnvelope trims any leading console noise and decodes the JSON object
// `conan list` emits.
func decodeListEnvelope(data []byte) (listEnvelope, error) {
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if start := strings.Index(trimmed, "{"); start > 0 {
		trimmed = trimmed[start:]
	} else if start < 0 {
		return nil, fmt.Errorf("E_CONAN_LAYOUT: conan list returned no JSON object")
	}
	var parsed listEnvelope
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, fmt.Errorf("E_CONAN_LAYOUT: read conan list output: %w", err)
	}
	return parsed, nil
}

// packagesFromEnvelope turns each source key and recipe reference into a neutral
// package record, labelling the local cache marker as installed and rendering
// the newest revision timestamp as its update date.
func packagesFromEnvelope(parsed listEnvelope, source string) []Package {
	var entries []Package
	for key, references := range parsed {
		current := key
		if current == "" {
			current = source
		}
		if key == "Local Cache" {
			current = localSource
		}
		for reference, revision := range references {
			name, version := splitReference(reference)
			if name == "" {
				continue
			}
			entry := Package{
				Source:     current,
				Name:       name,
				Version:    version,
				PackageURL: "https://conan.io/center/" + name,
			}
			if current == localSource {
				entry.Installed = true
			}
			if latest := latestTimestamp(revision); latest != "" {
				entry.Updated = latest
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

const localSource = "local"

// latestTimestamp renders the newest revision timestamp as a date. Conan's
// revision map is unordered, so the newest wins; a date string is kept as-is.
func latestTimestamp(revision conanReference) string {
	var newest conanTimestamp
	for _, item := range revision.Revisions {
		if newest.Seconds == 0 || item.Timestamp.Seconds > newest.Seconds {
			newest = item.Timestamp
		}
	}
	return newest.Format()
}

// sortPackages groups by name and orders each group newest-version-first, so
// the first row for a package is the version a user most likely wants.
func sortPackages(entries []Package) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return compareVersions(entries[i].Version, entries[j].Version) > 0
	})
}

// compareVersions reports whether left is a NEWER version than right. Dotted
// numeric segments compare segment by segment; an unparsable segment makes the
// comparison fall back to reverse-lexical order so versions like "1.0.1-rc1"
// still group together instead of interleaving.
func compareVersions(left, right string) int {
	leftParts, leftOK := versionParts(left)
	rightParts, rightOK := versionParts(right)
	if !leftOK || !rightOK {
		return strings.Compare(right, left)
	}
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		a, b := 0, 0
		if index < len(leftParts) {
			a = leftParts[index]
		}
		if index < len(rightParts) {
			b = rightParts[index]
		}
		if a != b {
			return a - b
		}
	}
	return 0
}

func versionParts(value string) ([]int, bool) {
	if value == "" {
		return nil, false
	}
	fields := strings.Split(value, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			return nil, false
		}
		number := 0
		for _, char := range field {
			if char < '0' || char > '9' {
				return nil, false
			}
			number = number*10 + int(char-'0')
		}
		parts = append(parts, number)
	}
	return parts, true
}

func splitReference(reference string) (string, string) {
	name, version := reference, ""
	if at := strings.Index(reference, "@"); at > 0 {
		reference = reference[:at]
	}
	if slash := strings.Index(reference, "/"); slash > 0 {
		name, version = reference[:slash], reference[slash+1:]
	}
	return name, version
}

// IsInstalled reports whether conan's cache holds the recipe reference.
func (client Client) IsInstalled(ctx context.Context, name string) bool {
	packages, err := client.Search(ctx, name)
	if err != nil {
		return false
	}
	for _, entry := range packages {
		if entry.Source == "local" && entry.Name == name {
			return true
		}
	}
	return false
}

// InstallWithProgress streams conan install output line by line.
func (client Client) InstallWithProgress(ctx context.Context, reference string, progress func(string)) error {
	lock, err := fsx.TryLock(ctx, filepath.Join(client.Root, "p"), "conan install")
	if err != nil {
		return err
	}
	defer lock.Close()
	command, stdout, stderr, err := client.startInstall(ctx, reference)
	if err != nil {
		return err
	}
	output := &installOutput{readErrors: make(chan error, 2)}
	outReader, errReader, progress := installReaders(ctx, stdout, stderr, progress)
	output.progress = progress
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		output.read(bufio.NewReader(outReader))
	}()
	go func() {
		defer wait.Done()
		output.read(bufio.NewReader(errReader))
	}()
	wait.Wait()
	close(output.readErrors)
	commandErr := command.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for readErr := range output.readErrors {
		return fmt.Errorf("E_CONAN_INSTALL: read output: %w", readErr)
	}
	if err := commandErr; err != nil {
		if detail := output.detail(); detail != "" {
			return fmt.Errorf("E_CONAN_INSTALL: %s", tail(detail, 8))
		}
		return fmt.Errorf("E_CONAN_INSTALL: %w", err)
	}
	return nil
}

// InstallCapture installs a reference without a TUI progress callback and
// returns the output tail for a summary line.
func (client Client) InstallCapture(ctx context.Context, reference string) (string, error) {
	command := processx.Command(ctx, client.Executable, client.installArgs(reference)...)
	command.Env = client.environment()
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(output.String())
		if detail != "" {
			return detail, fmt.Errorf("E_CONAN_INSTALL: %s", detail)
		}
		return "", fmt.Errorf("E_CONAN_INSTALL: conan install failed: %w", err)
	}
	return tail(strings.TrimSpace(output.String()), 4), nil
}

func (client Client) installArgs(reference string) []string {
	args := []string{"install", "--requires=" + reference, "--build=missing", "--format=json"}
	if client.Profile != "" {
		args = append(args, "-pr", client.Profile)
	}
	if client.BuildRoot != "" {
		profile := client.Profile
		if profile == "" {
			profile = "default"
		}
		args = append(args, "-of="+filepath.Join(client.BuildRoot, "conan-"+filepath.Base(profile)))
	}
	args = append(args, "-g", "CMakeDeps", "-g", "CMakeToolchain")
	for key, value := range client.Settings {
		if value != "" {
			args = append(args, "-s", key+"="+value)
		}
	}
	for key, value := range client.Options {
		if value != "" {
			args = append(args, "-o", key+"="+value)
		}
	}
	return args
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
