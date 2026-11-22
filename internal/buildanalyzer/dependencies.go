package buildanalyzer

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"trestle/internal/plan"
)

type depsRecord struct {
	mtime int64
	paths []string
}

func readDepsLog(path string) (map[string]depsRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	header := make([]byte, 16)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	uint32At := binary.LittleEndian.Uint32
	if string(header[:12]) != "# ninjadeps\n" || uint32At(header[12:]) != 4 {
		return nil, errors.New("unsupported .ninja_deps format")
	}
	paths := []string{}
	result := map[string]depsRecord{}
	for {
		var sizeBytes [4]byte
		_, err := io.ReadFull(reader, sizeBytes[:])
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		header := uint32At(sizeBytes[:])
		size, isDeps := header&0x7fffffff, header>>31 != 0
		if size < 8 || size >= 1<<19 || size%4 != 0 {
			return nil, errors.New("invalid .ninja_deps record size")
		}
		record := make([]byte, size)
		if _, err := io.ReadFull(reader, record); err != nil {
			return nil, err
		}
		if !isDeps {
			if uint32At(record[size-4:]) != ^uint32(len(paths)) {
				return nil, errors.New("invalid .ninja_deps path checksum")
			}
			paths = append(paths, string(bytes.TrimRight(record[:size-4], "\x00")))
			continue
		}
		id := uint32At(record)
		if size < 12 || int(id) >= len(paths) || int(id) < 0 {
			return nil, errors.New("invalid .ninja_deps output")
		}
		deps := depsRecord{mtime: int64(binary.LittleEndian.Uint64(record[4:12]))}
		for offset := 12; offset < len(record); offset += 4 {
			input := uint32At(record[offset:])
			if uint64(input) >= uint64(len(paths)) {
				return nil, errors.New("invalid .ninja_deps input")
			}
			deps.paths = append(deps.paths, paths[input])
		}
		result[pathKey(paths[id])] = deps
	}
}

func ninjaMTime(timestamp time.Time) int64 {
	if runtime.GOOS == "windows" {
		// Ninja's Win32 timestamp subtracts a fixed offset from FILETIME.
		return timestamp.UnixNano()/100 + (11644473600-12622770400)*10000000
	}
	return timestamp.UnixNano()
}

func (g *Graph) loadDynamic(dir string, buildPlan plan.BuildPlan, rules map[plan.ID]plan.Action) {
	var deps map[string]depsRecord
	for _, rule := range rules {
		if rule.Deps != "" {
			var err error
			deps, err = readDepsLog(filepath.Join(dir, ".ninja_deps"))
			if err != nil {
				g.warn(fmt.Errorf("reading .ninja_deps: %w", err))
			}
			break
		}
	}
	for i := range g.Edges {
		edge := &g.Edges[i]
		if edge.phony() || len(edge.Outputs) == 0 {
			continue
		}
		if rules[buildPlan.Actions[i].Rule].Deps != "" {
			primary := edge.Outputs[0]
			record, found := deps[pathKey(primary)]
			info, err := os.Stat(diskPath(dir, primary))
			if found && err == nil && ninjaMTime(info.ModTime()) <= record.mtime {
				edge.Discovered = record.paths
			} else {
				g.warn(fmt.Errorf("no current deps log entry for %q", primary))
			}
		} else if edge.Depfile != "" {
			content, err := os.ReadFile(diskPath(dir, edge.Depfile))
			if err != nil {
				g.warn(fmt.Errorf("reading depfile %q: %w", edge.Depfile, err))
				continue
			}
			edge.Discovered, err = parseDepfile(string(content), *edge)
			if err != nil {
				g.warn(fmt.Errorf("depfile %q: %w", edge.Depfile, err))
			}
		}
	}
	g.loadDyndeps(dir)
}

// parseDepfile reads the makefile rules emitted by compiler dependency writers.
// Empty rules emitted by -MP supply no inputs.
func parseDepfile(content string, edge Edge) ([]string, error) {
	content = strings.NewReplacer("\\\r\n", " ", "\\\n", " ").Replace(content)
	allowed := map[string]bool{}
	for _, outputs := range [][]string{edge.Outputs, edge.ImplicitOutputs} {
		for _, output := range outputs {
			allowed[pathKey(output)] = true
		}
	}
	var dependencies []string
	matched := false
	for _, line := range strings.Split(content, "\n") {
		outputs, inputs := depfileLine(strings.TrimSuffix(line, "\r"))
		if len(outputs) == 0 || !allowed[pathKey(outputs[0])] {
			if len(inputs) > 0 {
				return nil, errors.New("depfile rule names an unexpected output")
			}
			continue
		}
		for _, output := range outputs {
			if !allowed[pathKey(output)] {
				return nil, fmt.Errorf("unexpected output %q", output)
			}
		}
		if pathKey(outputs[0]) == pathKey(edge.Outputs[0]) {
			matched = true
		}
		dependencies = append(dependencies, inputs...)
	}
	if !matched {
		return nil, errors.New("depfile does not name the primary output")
	}
	return dependencies, nil
}

func depfileLine(line string) ([]string, []string) {
	var outputs, words []string
	var word strings.Builder
	flush := func() {
		if word.Len() != 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line) && strings.ContainsRune(" \t#:", rune(line[i+1])):
			i++
			word.WriteByte(line[i])
		case c == '$' && i+1 < len(line) && line[i+1] == '$':
			i++
			word.WriteByte(c)
		case c == '#':
			flush()
			return outputs, words
		case c == ' ' || c == '\t':
			flush()
		case c == ':' && outputs == nil && !(word.Len() == 1 && i+1 < len(line) && strings.ContainsRune("/\\", rune(line[i+1]))):
			flush()
			outputs, words = words, nil
		default:
			word.WriteByte(c)
		}
	}
	flush()
	return outputs, words
}
