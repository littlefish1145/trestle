package vcpkg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PcFile struct {
	Name            string
	Cflags          []string
	Libs            []string
	LibsPrivate     []string
	Requires        []string
	RequiresPrivate []string
}

func ParsePC(path string) (PcFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return PcFile{}, err
	}
	defer file.Close()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return PcFile{}, err
	}
	values := map[string]string{"pcfiledir": filepath.ToSlash(filepath.Dir(absolute))}
	fields := map[string]string{}
	var result PcFile
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		separator := strings.IndexAny(line, "=:")
		if separator < 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value := strings.TrimSpace(line[separator+1:])
		if line[separator] == '=' {
			values[key] = value
			continue
		}
		fields[key] = value
	}
	if err := scanner.Err(); err != nil {
		return PcFile{}, err
	}
	for key, raw := range fields {
		value := expandPC(raw, values)
		if strings.Contains(value, "${") {
			return PcFile{}, fmt.Errorf("unresolved pkg-config variable in %s: %s", key, value)
		}
		switch key {
		case "Name":
			result.Name = value
		case "Cflags":
			result.Cflags = splitPC(value)
		case "Libs":
			result.Libs = splitPC(value)
		case "Libs.private":
			result.LibsPrivate = splitPC(value)
		case "Requires":
			result.Requires = splitRequires(value)
		case "Requires.private":
			result.RequiresPrivate = splitRequires(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return PcFile{}, err
	}
	return result, nil
}

func expandPC(value string, values map[string]string) string {
	for pass := 0; pass < 16; pass++ {
		changed := false
		for name, replacement := range values {
			token := "${" + name + "}"
			if strings.Contains(value, token) {
				value = strings.ReplaceAll(value, token, replacement)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return value
}

func splitPC(value string) []string {
	var fields []string
	var token strings.Builder
	var quote rune
	for _, char := range value {
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				token.WriteRune(char)
			}
		} else if char == '\'' || char == '"' {
			quote = char
		} else if char == ' ' || char == '\t' {
			if token.Len() > 0 {
				fields = append(fields, token.String())
				token.Reset()
			}
		} else {
			token.WriteRune(char)
		}
	}
	if token.Len() > 0 {
		fields = append(fields, token.String())
	}
	var result []string
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if (field == "-I" || field == "-L" || field == "-D") && index+1 < len(fields) {
			index++
			field += fields[index]
		}
		result = append(result, field)
	}
	return result
}

func splitRequires(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) > 0 {
			result = append(result, fields[0])
		}
	}
	return result
}

func FindPC(layout Layout, name string) (string, error) {
	for _, dir := range layout.PkgConfigDirs {
		candidate := filepath.Join(dir, name+".pc")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("E_PACKAGE_METADATA_UNSUPPORTED: no pkg-config metadata for %s", name)
}
