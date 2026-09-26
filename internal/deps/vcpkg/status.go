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
	fields := map[string]string{}
	var result []string
	flush := func() {
		if fields["Package"] == port && (fields["Architecture"] == "" || fields["Architecture"] == layout.Triplet) {
			for _, raw := range strings.Split(fields["Depends"], ",") {
				name := strings.TrimSpace(raw)
				if cut := strings.IndexAny(name, " [: "); cut >= 0 {
					name = name[:cut]
				}
				if name != "" {
					result = append(result, name)
				}
			}
		}
		fields = map[string]string{}
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if index := strings.Index(line, ":"); index > 0 {
			fields[line[:index]] = strings.TrimSpace(line[index+1:])
		}
	}
	flush()
	return result
}
