package vcpkg

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// InstalledDependencies reads vcpkg's Debian-style installed status database.
// It is used only as a fallback for ports without pkg-config metadata.
func InstalledDependencies(layout Layout, port string) []string {
	file, err := os.Open(filepath.Join(layout.Root, "installed", "vcpkg", "status"))
	if err != nil {
		return nil
	}
	defer file.Close()
	var result []string
	fields := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			result = append(result, dependenciesForPort(fields, layout, port)...)
			fields = map[string]string{}
			continue
		}
		fields = parseStatusField(fields, line)
	}
	return append(result, dependenciesForPort(fields, layout, port)...)
}

// parseStatusField adds one "Key: value" line to the paragraph being read,
// allocating the field map on first use. Lines without a usable key are ignored.
func parseStatusField(fields map[string]string, line string) map[string]string {
	index := strings.Index(line, ":")
	if index <= 0 {
		return fields
	}
	if fields == nil {
		fields = map[string]string{}
	}
	fields[line[:index]] = strings.TrimSpace(line[index+1:])
	return fields
}

// dependenciesForPort returns the Depends list of a status paragraph when it
// describes the requested port for the layout's triplet, and nil otherwise. An
// empty Architecture means the entry applies to every triplet.
func dependenciesForPort(fields map[string]string, layout Layout, port string) []string {
	if fields["Package"] != port {
		return nil
	}
	if architecture := fields["Architecture"]; architecture != "" && architecture != layout.Triplet {
		return nil
	}
	var result []string
	for _, raw := range strings.Split(fields["Depends"], ",") {
		if name := dependencyName(raw); name != "" {
			result = append(result, name)
		}
	}
	return result
}

// dependencyName strips the version range and architecture decorations from one
// entry of a Debian Depends list, leaving the bare port name.
func dependencyName(raw string) string {
	name := strings.TrimSpace(raw)
	if cut := strings.IndexAny(name, " [: "); cut >= 0 {
		name = name[:cut]
	}
	return name
}
