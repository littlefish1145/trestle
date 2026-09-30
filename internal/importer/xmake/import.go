package xmake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"trestle/internal/processx"

	"trestle/internal/config"
)

type target struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	TargetFile  string   `json:"targetfile"`
	LinkerKind  string   `json:"-"`
	Files       []string `json:"files"`
	SourceFiles []string `json:"sourcefiles"`
	Links       []string `json:"links"`
	SysLinks    []string `json:"syslinks"`
	Defines     []string `json:"defines"`
	IncludeDirs []string `json:"includedirs"`
	CFlags      []string `json:"cflags"`
	CXFlags     []string `json:"cxflags"`
	CXXFlags    []string `json:"cxxflags"`
	LDFlags     []string `json:"ldflags"`
	Deps        []string `json:"deps"`
}

func (result *target) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("Xmake target must be a JSON object")
	}
	result.Name = jsonValue(fields["name"])
	result.Kind = firstJSONValue(fields, "kind", "targetkind")
	result.TargetFile = firstJSONValue(fields, "targetfile", "filename")
	if linker := jsonObject(fields["linker"]); linker != nil {
		result.LinkerKind = firstJSONValue(linker, "kind", "name")
	}
	result.Files = jsonValues(fields["files"])
	result.SourceFiles = jsonValues(fields["sourcefiles"])
	result.Links = jsonValues(fields["links"])
	result.SysLinks = jsonValues(fields["syslinks"])
	result.Defines = jsonValues(fields["defines"])
	result.IncludeDirs = jsonValues(fields["includedirs"])
	result.CFlags = jsonValues(fields["cflags"])
	result.CXFlags = jsonValues(fields["cxflags"])
	result.CXXFlags = jsonValues(fields["cxxflags"])
	result.LDFlags = append(jsonValues(fields["ldflags"]), jsonValues(fields["shflags"])...)
	result.Deps = jsonValues(fields["deps"])
	if result.Kind == "" {
		result.Kind = inferTargetKind(*result)
	}
	return nil
}

func jsonObject(data json.RawMessage) map[string]json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return nil
	}
	return object
}

func firstJSONValue(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := jsonValue(fields[key]); value != "" {
			return value
		}
	}
	return ""
}

func jsonValues(data json.RawMessage) []string {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err == nil {
		values := make([]string, 0, len(items))
		for _, item := range items {
			if value := jsonValue(item); value != "" {
				values = append(values, value)
			}
		}
		return values
	}
	if value := jsonValue(data); value != "" {
		return []string{value}
	}
	return nil
}

func jsonValue(data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		return value
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return ""
	}
	for _, key := range []string{"value", "name", "path", "target"} {
		if value = jsonValue(object[key]); value != "" {
			return value
		}
	}
	return ""
}

type commandRunner func(context.Context, string, string, ...string) ([]byte, error)

var legacyNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.+@/-]+$`)

func Parse(data []byte) ([]target, error) {
	payload, decodeErr := extractJSON(data)
	if decodeErr != nil {
		return nil, fmt.Errorf("parse xmake target JSON: %w", decodeErr)
	}
	data = payload
	var envelope struct {
		Targets json.RawMessage `json:"targets"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && len(envelope.Targets) > 0 && string(envelope.Targets) != "null" {
		if targets, err := parseTargetCollection(envelope.Targets); err == nil && len(targets) > 0 {
			return targets, nil
		}
	}
	if targets, err := parseTargetCollection(data); err == nil && len(targets) > 0 {
		return targets, nil
	}
	var single target
	if err := json.Unmarshal(data, &single); err == nil && (single.Name != "" || single.Kind != "") {
		return []target{single}, nil
	}
	return nil, fmt.Errorf("parse xmake target JSON: unsupported JSON shape")
}

func extractJSON(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	objectStart := bytes.IndexByte(data, '{')
	arrayStart := bytes.IndexByte(data, '[')
	start := objectStart
	if start < 0 || (arrayStart >= 0 && arrayStart < start) {
		start = arrayStart
	}
	if start < 0 {
		return nil, fmt.Errorf("no JSON object or array found")
	}
	decoder := json.NewDecoder(bytes.NewReader(data[start:]))
	var payload json.RawMessage
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func parseTargetCollection(data []byte) ([]target, error) {
	var list []target
	if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	var keyed map[string]target
	if err := json.Unmarshal(data, &keyed); err != nil || len(keyed) == 0 {
		return nil, fmt.Errorf("not an Xmake target collection")
	}
	keys := make([]string, 0, len(keyed))
	for key := range keyed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := keyed[key]
		if value.Name == "" {
			value.Name = key
		}
		list = append(list, value)
	}
	return list, nil
}

func ParseLegacyTarget(name string, data []byte) (target, error) {
	result := target{Name: name}
	section := ""
	for _, rawLine := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "The information of target(") {
			continue
		}
		if strings.HasPrefix(line, "->") {
			appendLegacyValue(&result, section, cleanLegacyValue(strings.TrimSpace(strings.TrimPrefix(line, "->"))))
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		section = strings.ToLower(strings.TrimSpace(key))
		value = cleanLegacyValue(strings.TrimSpace(value))
		if value != "" {
			appendLegacyValue(&result, section, value)
		}
	}
	if result.Kind == "" {
		result.Kind = inferTargetKind(result)
	}
	if result.Kind == "" {
		return target{}, fmt.Errorf("target %q: Xmake output did not contain a kind", name)
	}
	return result, nil
}

func appendLegacyValue(result *target, section, value string) {
	if value == "" || value == "none" {
		return
	}
	switch section {
	case "kind":
		result.Kind = value
	case "targetfile", "filename":
		result.TargetFile = value
	case "files", "sourcefiles":
		result.Files = append(result.Files, value)
	case "includedirs":
		result.IncludeDirs = append(result.IncludeDirs, value)
	case "defines":
		result.Defines = append(result.Defines, value)
	case "links":
		result.Links = append(result.Links, value)
	case "syslinks":
		result.SysLinks = append(result.SysLinks, value)
	case "deps", "dependencies":
		result.Deps = append(result.Deps, value)
	case "cflags":
		result.CFlags = append(result.CFlags, value)
	case "cxflags":
		result.CXFlags = append(result.CXFlags, value)
	case "cxxflags":
		result.CXXFlags = append(result.CXXFlags, value)
	case "ldflags", "shflags":
		result.LDFlags = append(result.LDFlags, value)
	}
}

func inferTargetKind(item target) string {
	switch strings.ToLower(strings.TrimSpace(item.LinkerKind)) {
	case "sh", "shared":
		return "shared"
	case "ar", "static":
		return "static"
	case "ld", "binary", "executable":
		return "binary"
	}
	extension := strings.ToLower(filepath.Ext(item.TargetFile))
	switch extension {
	case ".dll", ".so", ".dylib":
		return "shared"
	case ".a", ".lib":
		return "static"
	case ".exe", ".com":
		return "binary"
	}
	return ""
}

func cleanLegacyValue(value string) string {
	if index := strings.LastIndex(value, " -> "); index >= 0 {
		suffix := strings.TrimSpace(value[index+4:])
		if strings.Contains(strings.ToLower(suffix), ".lua:") {
			value = value[:index]
		}
	}
	return strings.TrimSpace(value)
}

func parseTargetNames(data []byte) []string {
	var names []string
	if json.Unmarshal(data, &names) == nil {
		return uniqueSorted(names)
	}
	if targets, err := Parse(data); err == nil {
		for _, item := range targets {
			if item.Name != "" {
				names = append(names, item.Name)
			}
		}
		return uniqueSorted(names)
	}
	for _, rawLine := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawLine), "->"))
		if line == "" || strings.Contains(line, ":") || !legacyNamePattern.MatchString(line) {
			continue
		}
		names = append(names, line)
	}
	return uniqueSorted(names)
}

func uniqueSorted(values []string) []string {
	result := uniqueStable(values)
	sort.Strings(result)
	return result
}

func uniqueStable(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func runCommand(ctx context.Context, executable, root string, args ...string) ([]byte, error) {
	cmd := processx.Command(ctx, executable, args...)
	cmd.Dir = root
	return processx.Capture(ctx, cmd)
}

func introspect(ctx context.Context, executable, root string, progress func(string), run commandRunner) ([]target, error) {
	var attemptErrors []string
	for _, args := range [][]string{
		{"show", "-t", "target", "--format=json"},
		{"show", "-t", "target", "--json"},
	} {
		out, err := run(ctx, executable, root, args...)
		if err == nil {
			if targets, parseErr := Parse(out); parseErr == nil && hasTargetKinds(targets) {
				if progress != nil {
					progress("Xmake target metadata: aggregate JSON protocol")
				}
				return targets, nil
			} else if parseErr != nil {
				attemptErrors = append(attemptErrors, formatAttempt(args, parseErr, out))
			}
		} else {
			attemptErrors = append(attemptErrors, formatAttempt(args, err, out))
		}
	}

	var names []string
	for _, args := range [][]string{
		{"show", "-l", "targets", "--format=json"},
		{"show", "-l", "targets", "--json"},
		{"show", "-l", "targets"},
	} {
		out, err := run(ctx, executable, root, args...)
		if err != nil {
			attemptErrors = append(attemptErrors, formatAttempt(args, err, out))
			continue
		}
		names = parseTargetNames(out)
		if len(names) > 0 {
			break
		}
		attemptErrors = append(attemptErrors, formatAttempt(args, errors.New("no target names found"), out))
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("xmake target introspection failed; no targets were listed\n%s", strings.Join(attemptErrors, "\n"))
	}
	if progress != nil {
		progress(fmt.Sprintf("Xmake target metadata: per-target compatibility protocol (%d targets)", len(names)))
	}

	result := make([]target, 0, len(names))
	for _, name := range names {
		var imported target
		var targetErrors []string
		for _, args := range [][]string{
			{"show", "-t", name, "--format=json"},
			{"show", "-t", name, "--json"},
			{"show", "-t", name},
		} {
			out, err := run(ctx, executable, root, args...)
			if err != nil {
				targetErrors = append(targetErrors, formatAttempt(args, err, out))
				continue
			}
			isJSON := args[len(args)-1] == "--format=json" || args[len(args)-1] == "--json"
			if isJSON {
				parsed, parseErr := Parse(out)
				if parseErr == nil && len(parsed) > 0 && parsed[0].Kind != "" {
					imported = parsed[0]
					if imported.Name == "" {
						imported.Name = name
					}
					break
				}
				if parseErr == nil {
					parseErr = fmt.Errorf("target metadata did not identify a target kind")
				}
				targetErrors = append(targetErrors, formatAttempt(args, parseErr, out))
				continue
			}
			parsed, parseErr := ParseLegacyTarget(name, out)
			if parseErr == nil {
				imported = parsed
				break
			}
			targetErrors = append(targetErrors, formatAttempt(args, parseErr, out))
		}
		if imported.Kind == "" {
			return nil, fmt.Errorf("xmake target %q introspection failed\n%s", name, strings.Join(targetErrors, "\n"))
		}
		result = append(result, imported)
	}
	return result, nil
}

func hasTargetKinds(targets []target) bool {
	for _, item := range targets {
		if item.Name != "" && item.Kind != "" {
			return true
		}
	}
	return false
}

func formatAttempt(args []string, err error, output []byte) string {
	message := fmt.Sprintf("  xmake %s: %v", strings.Join(args, " "), err)
	if detail := strings.TrimSpace(string(output)); detail != "" {
		if len(detail) > 240 {
			detail = detail[:240] + "..."
		}
		message += ": " + strings.ReplaceAll(detail, "\n", " ")
	}
	return message
}

func Import(ctx context.Context, root, configPath string, progress func(string)) error {
	expected, snapshotErr := config.Snapshot(configPath)
	if snapshotErr != nil {
		return snapshotErr
	}
	if _, err := os.Stat(filepath.Join(root, "xmake.lua")); err != nil {
		return fmt.Errorf("xmake.lua was not found in %s", root)
	}
	xmake, err := exec.LookPath("xmake")
	if err != nil {
		return fmt.Errorf("xmake is required for import: %w", err)
	}
	targets, err := introspect(ctx, xmake, root, progress, runCommand)
	if err != nil {
		return err
	}
	cfg, loadErr := config.Load(configPath)
	if loadErr != nil {
		cfg = config.Default(filepath.Base(root))
	}
	cfg.Targets = map[string]config.Target{}
	for _, source := range targets {
		kind := map[string]string{"binary": "executable", "executable": "executable", "static": "static", "shared": "shared", "object": "static"}[strings.ToLower(source.Kind)]
		if kind == "" || source.Name == "" {
			continue
		}
		sources := append(append([]string{}, source.Files...), source.SourceFiles...)
		compileOptions := append(append(append([]string{}, source.CFlags...), source.CXFlags...), source.CXXFlags...)
		libraries := append(append([]string{}, source.Links...), source.SysLinks...)
		t := config.Target{Type: kind, Sources: uniqueStable(sources), IncludeDirs: uniqueStable(source.IncludeDirs), Defines: uniqueStable(source.Defines), CompileOptions: uniqueStable(compileOptions), LinkOptions: uniqueStable(source.LDFlags), Libraries: uniqueStable(libraries)}
		for _, dep := range uniqueStable(source.Deps) {
			t.Dependencies = append(t.Dependencies, config.Dependency{Target: dep, Scope: "private"})
		}
		cfg.Targets[source.Name] = t
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("xmake did not expose any buildable targets")
	}
	names := make([]string, 0, len(cfg.Targets))
	for name := range cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	cfg.Build.DefaultTargets = nil
	for _, name := range names {
		if cfg.Targets[name].Type == "executable" {
			cfg.Build.DefaultTargets = []string{name}
			break
		}
	}
	if len(cfg.Build.DefaultTargets) == 0 {
		cfg.Build.DefaultTargets = []string{names[0]}
	}
	cfg.Package.Targets = append([]string{}, cfg.Build.DefaultTargets...)
	if progress != nil {
		progress(fmt.Sprintf("Imported %d Xmake targets; default target: %s", len(cfg.Targets), cfg.Build.DefaultTargets[0]))
	}
	return config.SaveExpected(configPath, cfg, expected)
}
