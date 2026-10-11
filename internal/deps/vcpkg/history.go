package vcpkg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"trestle/internal/processx"
)

// PortVersion is one published version of a port, taken from the vcpkg version
// database. Version is the upstream version; PortVersion counts revisions of
// that same upstream version, so (11.0.2, 1) is newer than (11.0.2, 0).
type PortVersion struct {
	Port         string
	Version      string
	PortVersion  int
	GitTree      string
	Date         string
	Installed    bool
	Current      bool
	PackageURL   string
	Description  string
	License      string
	Homepage     string
	Dependencies []string
}

// versionDatabaseEntry mirrors one record of a versions/<letter>/<port>.json
// file. The file is maintained by vcpkg itself and is the authoritative list of
// every published version, unlike the working tree, which only holds the newest.
type versionDatabaseEntry struct {
	GitTree     string `json:"git-tree"`
	Version     string `json:"version"`
	PortVersion int    `json:"port-version"`
}

type versionDatabase struct {
	Versions []versionDatabaseEntry `json:"versions"`
}

// versionDatabasePath maps a port name onto its database file. vcpkg shards the
// database into versions/<shard>/, where the shard is the port name's first
// character followed by "-", so fmt lives in versions/f-/fmt.json. Digits and
// symbols use their own first character, also with the "-" suffix.
func versionDatabasePath(root, name string) string {
	shard := "-"
	if name != "" {
		shard = strings.ToLower(string(name[0])) + "-"
	}
	return filepath.Join(root, "versions", shard, strings.ToLower(name)+".json")
}

// PortVersions lists every published version of a port, newest first. It reads
// the version database and resolves each entry's git tree through the vcpkg
// checkout, so historical versions are available even though the working tree
// only contains the current port.
//
// A vcpkg checkout that is not a git repository still yields the versions
// recorded in the database, without their dates or port metadata.
func (client Client) PortVersions(ctx context.Context, name string) ([]PortVersion, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("E_VCPKG_HISTORY: port name is required")
	}
	databasePath := versionDatabasePath(client.Root, name)
	data, err := os.ReadFile(databasePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("E_VCPKG_HISTORY: no version history for %q: %s is missing", name, filepath.Base(databasePath))
		}
		return nil, fmt.Errorf("E_VCPKG_HISTORY: read %s: %w", databasePath, err)
	}
	var database versionDatabase
	if err := json.Unmarshal(data, &database); err != nil {
		return nil, fmt.Errorf("E_VCPKG_HISTORY: parse %s: %w", databasePath, err)
	}
	manifest := client.currentManifest(name)
	installed := client.IsInstalled(name)
	versions := make([]PortVersion, 0, len(database.Versions))
	for _, entry := range database.Versions {
		if entry.Version == "" {
			continue
		}
		version := PortVersion{
			Port:        name,
			Version:     entry.Version,
			PortVersion: entry.PortVersion,
			GitTree:     entry.GitTree,
			Installed:   installed && manifest != nil && manifest.Version == entry.Version,
			PackageURL:  "https://vcpkg.io/en/package/" + name + ".html",
		}
		// The newest entry describes the port currently in the working tree, so
		// it can reuse the already-parsed manifest instead of a git call.
		if manifest != nil && manifest.Version == entry.Version && manifest.VersionDate == "" {
			version.Current = true
		}
		versions = append(versions, version)
	}
	sortPortVersions(versions)
	if err := client.fillVersionDetails(ctx, name, versions); err != nil {
		return versions, err
	}
	return versions, nil
}

// portManifest is the subset of vcpkg.json needed to describe a version.
type portManifest struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	VersionDate  string          `json:"version-date"`
	VersionField string          `json:"version-string"`
	Description  json.RawMessage `json:"description"`
	Homepage     string          `json:"homepage"`
	License      string          `json:"license"`
	Dependencies json.RawMessage `json:"dependencies"`
}

func (m portManifest) date() string {
	if m.VersionDate != "" {
		return m.VersionDate
	}
	return m.VersionField
}

// fillVersionDetails resolves each version's git tree to its manifest, which is
// where the description, license and dependencies of that specific release live.
// A checkout without git history simply leaves those fields empty.
func (client Client) fillVersionDetails(ctx context.Context, name string, versions []PortVersion) error {
	if !client.hasGitCheckout() {
		return nil
	}
	// Newest first, so the newest entries can stop the walk early: only the
	// newest few releases need their metadata for the UI.
	for index := range versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		version := &versions[index]
		if version.GitTree == "" {
			continue
		}
		manifest, err := client.manifestFromTree(ctx, version.GitTree)
		if err != nil || manifest == nil {
			continue
		}
		if version.Current || (version.Description == "" && version.License == "") {
			version.Date = manifest.date()
		}
		if version.Description == "" {
			version.Description = decodeText(manifest.Description)
		}
		if version.License == "" {
			version.License = manifest.License
		}
		if version.Homepage == "" {
			version.Homepage = manifest.Homepage
		}
		if len(version.Dependencies) == 0 {
			version.Dependencies = decodeNames(manifest.Dependencies)
		}
		if manifest.Version != "" {
			version.Version = manifest.Version
		}
	}
	return nil
}

// manifestFromTree reads vcpkg.json out of a git tree object.
func (client Client) manifestFromTree(ctx context.Context, tree string) (*portManifest, error) {
	command := processx.Command(ctx, "git", "-C", client.Root, "show", tree+":vcpkg.json")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	var manifest portManifest
	if err := json.Unmarshal(output, &manifest); err != nil {
		return nil, err
	}
	if manifest.Version == "" {
		manifest.Version = manifest.VersionField
	}
	return &manifest, nil
}

func (client Client) currentManifest(name string) *portManifest {
	data, err := os.ReadFile(filepath.Join(client.Root, "ports", name, "vcpkg.json"))
	if err != nil {
		return nil
	}
	var manifest portManifest
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	if manifest.Version == "" {
		manifest.Version = manifest.VersionField
	}
	return &manifest
}

func (client Client) hasGitCheckout() bool {
	if _, err := os.Stat(filepath.Join(client.Root, ".git")); err != nil {
		return false
	}
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	return true
}

// sortPortVersions orders newest first, treating a higher port-version of the
// same upstream version as newer, and keeping unparsable versions last.
func sortPortVersions(versions []PortVersion) {
	sort.SliceStable(versions, func(i, j int) bool {
		if versions[i].Version != versions[j].Version {
			return compareVersionStrings(versions[i].Version, versions[j].Version) > 0
		}
		return versions[i].PortVersion > versions[j].PortVersion
	})
}

func compareVersionStrings(left, right string) int {
	leftParts, leftOK := versionNumbers(left)
	rightParts, rightOK := versionNumbers(right)
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

func versionNumbers(value string) ([]int, bool) {
	if value == "" {
		return nil, false
	}
	// A trailing pre-release or build suffix makes the version non-numeric.
	if strings.ContainsAny(value, "-+abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
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

// SearchPortsVersions returns every published version of the ports matching a
// query, newest first. Unlike the working tree, the version database keeps
// historical releases, so a search can offer the full version history of a port
// rather than only the current one.
//
// Ports absent from the database still appear once, carrying the version the
// working tree reports, so a port that vcpkg has not versioned yet is not lost.
func (client Client) SearchPortsVersions(ctx context.Context, query string, limit int) ([]PortVersion, error) {
	names, err := client.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	var found []PortVersion
	var failures []string
	for _, line := range names {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "The") {
			continue
		}
		name := fields[0]
		versions, historyErr := client.PortVersions(ctx, name)
		if historyErr != nil {
			// Not every port is in the version database; fall back to the
			// single version the local tree reports.
			port := Port{Name: name, Installed: client.IsInstalled(name), Source: "local"}
			if len(fields) > 1 {
				port.Version = strings.TrimSuffix(fields[1], "#")
			}
			port.PackageURL = "https://vcpkg.io/en/package/" + name + ".html"
			found = append(found, PortVersion{
				Port: name, Version: port.Version, Current: true, Installed: port.Installed, PackageURL: port.PackageURL,
			})
			continue
		}
		if len(found)+len(versions) > 200 {
			failures = append(failures, fmt.Sprintf("%s history truncated", name))
			break
		}
		found = append(found, versions...)
	}
	if len(found) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("E_VCPKG_HISTORY: %s", strings.Join(failures, "; "))
	}
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found, nil
}

// ResolveVersion finds the exact published version of a port. The match is
// exact on the upstream version, and a "#n" suffix in the request pins the
// port-version too. The newest release is returned first when the request
// carries no port-version, so "fmt:11.0.2" resolves to the latest 11.0.2.
func (client Client) ResolveVersion(ctx context.Context, name, version string) ([]PortVersion, error) {
	wanted, wantedPort := splitPortVersion(version)
	versions, err := client.PortVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	var matched []PortVersion
	for _, candidate := range versions {
		if candidate.Version != wanted {
			continue
		}
		if wantedPort >= 0 && candidate.PortVersion != wantedPort {
			continue
		}
		matched = append(matched, candidate)
	}
	if len(matched) == 0 {
		available := make([]string, 0, 4)
		for index, candidate := range versions {
			if index >= 4 {
				break
			}
			available = append(available, candidate.FormatVersion())
		}
		suffix := ""
		if len(available) > 0 {
			suffix = "; newest: " + strings.Join(available, ", ")
		}
		return nil, fmt.Errorf("E_VCPKG_HISTORY: %s has no version %s%s", name, version, suffix)
	}
	if wantedPort >= 0 {
		// An explicit "#n" pins one revision, so at most one entry can match.
		return matched, nil
	}
	// Without a "#n" the request names a version, and a version can have
	// several port revisions. Only the newest is the one the user meant; the
	// others are packaging fixes for the same upstream release and installing
	// them would silently pick a different one on each run.
	return matched[:1], nil
}

// splitPortVersion separates a "11.0.2#3" request into its version and
// port-version. A missing suffix reports -1 so any port-version matches.
func splitPortVersion(value string) (string, int) {
	text := strings.TrimSpace(value)
	base, suffix, found := strings.Cut(text, "#")
	if !found {
		return text, -1
	}
	number := 0
	if suffix == "" {
		return base, -1
	}
	for _, char := range suffix {
		if char < '0' || char > '9' {
			return text, -1
		}
		number = number*10 + int(char-'0')
	}
	return base, number
}

// FormatVersion renders a version the way vcpkg records it, including the
// port-version when the upstream version was revised in place.
func (version PortVersion) FormatVersion() string {
	if version.Version == "" {
		return "unknown"
	}
	if version.PortVersion <= 0 {
		return version.Version
	}
	return fmt.Sprintf("%s#%d", version.Version, version.PortVersion)
}
