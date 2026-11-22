package buildanalyzer

import (
	"fmt"
	"os"
	"strings"
)

type dynamicEdge struct {
	outputs []string
	inputs  []string
}

func (g *Graph) loadDyndeps(dir string) {
	files := map[string]map[string]dynamicEdge{}
	for i := range g.Edges {
		edge := &g.Edges[i]
		if edge.Dyndep == "" {
			continue
		}
		key := pathKey(edge.Dyndep)
		records, loaded := files[key]
		if !loaded {
			content, err := os.ReadFile(diskPath(dir, edge.Dyndep))
			if err == nil {
				records, err = parseDyndep(string(content))
			}
			if err != nil {
				g.warn(fmt.Errorf("dyndep %q: %w", edge.Dyndep, err))
			}
			files[key] = records
		}
		if records == nil {
			continue
		}
		primary := pathKey(edge.Outputs[0])
		record, ok := records[primary]
		if !ok {
			g.warn(fmt.Errorf("dyndep %q lacks output %q", edge.Dyndep, primary))
			continue
		}
		delete(records, primary)
		edge.DynamicOutputs, edge.DynamicInputs = record.outputs, record.inputs
	}
	for _, edge := range g.Edges {
		if edge.Dyndep != "" && len(files[pathKey(edge.Dyndep)]) > 0 {
			delete(files, pathKey(edge.Dyndep))
			g.warn(fmt.Errorf("dyndep %q names unbound outputs", edge.Dyndep))
		}
	}
}

type dyndepToken struct {
	text      string
	separator byte
}

func dyndepTokens(line string) ([]dyndepToken, error) {
	var tokens []dyndepToken
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, dyndepToken{text: word.String()})
			word.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; c {
		case '\t', ' ':
			flush()
		case '|', ':':
			flush()
			tokens = append(tokens, dyndepToken{separator: c})
		case '$':
			if i+1 == len(line) || !strings.ContainsRune("$ :", rune(line[i+1])) {
				return nil, fmt.Errorf("invalid dyndep escape in %q", line)
			}
			i++
			word.WriteByte(line[i])
		default:
			word.WriteByte(c)
		}
	}
	flush()
	return tokens, nil
}

func parseDyndep(content string) (map[string]dynamicEdge, error) {
	// Dollar-newline continuations disappear together with indentation.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if len(lines) > 0 {
			previous := lines[len(lines)-1]
			suffix := len(previous) - len(strings.TrimRight(previous, "$"))
			if suffix%2 == 1 {
				lines[len(lines)-1] = previous[:len(previous)-1] + strings.TrimLeft(line, " ")
				continue
			}
		}
		lines = append(lines, line)
	}
	result := map[string]dynamicEdge{}
	versionSeen := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !versionSeen {
			key, value, _ := strings.Cut(line, "=")
			if strings.TrimSpace(key) != "ninja_dyndep_version" || (strings.TrimSpace(value) != "1" && strings.TrimSpace(value) != "1.0") {
				return nil, fmt.Errorf("unsupported dyndep header %q", line)
			}
			versionSeen = true
			continue
		}
		if key, _, _ := strings.Cut(line, "="); strings.TrimSpace(key) == "restat" {
			continue
		}
		tokens, err := dyndepTokens(line)
		if err != nil {
			return nil, err
		}
		if len(tokens) < 4 || tokens[0].text != "build" || tokens[1].text == "" {
			return nil, fmt.Errorf("invalid dyndep statement %q", line)
		}
		entry, ok := parseDynamicEdge(tokens[2:])
		if !ok {
			return nil, fmt.Errorf("invalid dyndep statement %q", line)
		}
		path := pathKey(tokens[1].text)
		if _, exists := result[path]; exists {
			return nil, fmt.Errorf("duplicate dyndep output %q", path)
		}
		result[path] = entry
	}
	if !versionSeen {
		return nil, fmt.Errorf("missing dyndep version")
	}
	return result, nil
}

func parseDynamicEdge(tokens []dyndepToken) (dynamicEdge, bool) {
	var result dynamicEdge
	if tokens[0].separator == '|' {
		tokens = tokens[1:]
		for len(tokens) > 0 && tokens[0].text != "" {
			result.outputs = append(result.outputs, tokens[0].text)
			tokens = tokens[1:]
		}
		if len(result.outputs) == 0 {
			return result, false
		}
	}
	if len(tokens) < 2 || tokens[0].separator != ':' || tokens[1].text != "dyndep" {
		return result, false
	}
	tokens = tokens[2:]
	if len(tokens) == 0 {
		return result, true
	}
	if len(tokens) < 2 || tokens[0].separator != '|' {
		return result, false
	}
	for _, token := range tokens[1:] {
		if token.text == "" {
			return result, false
		}
		result.inputs = append(result.inputs, token.text)
	}
	return result, true
}
