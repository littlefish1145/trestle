package modules

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/modules/p1689"
)

type Node struct {
	TranslationUnit string
	Provides        []p1689.ModuleKey
	Requires        []p1689.ModuleKey
}

type Graph struct {
	Nodes []Node
	Edges map[string][]string
}

func BuildGraph(scans []p1689.Document) (Graph, error) {
	providers := map[string]string{}
	nodes := make([]Node, 0, len(scans))
	for _, scan := range scans {
		for _, rule := range scan.Rules {
			tu := rule.PrimaryOutput
			if tu == "" && len(rule.Outputs) > 0 {
				tu = rule.Outputs[0]
			}
			if tu == "" {
				return Graph{}, fmt.Errorf("E_MODULE_SCAN_FAILED: rule has no translation unit output")
			}
			tu = filepath.Clean(tu)
			node := Node{TranslationUnit: tu}
			for _, module := range rule.Provides {
				key := module.Key()
				if previous, ok := providers[key.String()]; ok && previous != tu {
					return Graph{}, fmt.Errorf("E_MODULE_DUPLICATE_PROVIDER: module %q is provided by %s and %s", key.String(), previous, tu)
				}
				providers[key.String()] = tu
				node.Provides = append(node.Provides, key)
			}
			for _, module := range rule.Requires {
				node.Requires = append(node.Requires, module.Key())
			}
			nodes = append(nodes, node)
		}
	}
	byTU := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		byTU[node.TranslationUnit] = node
	}
	edges := make(map[string][]string)
	for _, node := range nodes {
		for _, requirement := range node.Requires {
			provider, ok := providers[requirement.String()]
			if !ok {
				return Graph{}, fmt.Errorf("E_MODULE_MISSING_PROVIDER: %s requires %q but no provider exists", node.TranslationUnit, requirement.String())
			}
			edges[provider] = appendUnique(edges[provider], node.TranslationUnit)
		}
	}
	if cycle := findCycle(edges); len(cycle) > 0 {
		return Graph{}, fmt.Errorf("E_MODULE_CYCLE: %s", strings.Join(cycle, " -> "))
	}
	return Graph{Nodes: nodes, Edges: edges}, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func findCycle(edges map[string][]string) []string {
	state := map[string]int{}
	var path []string
	var visit func(string) []string
	visit = func(node string) []string {
		if state[node] == 1 {
			for i, value := range path {
				if value == node {
					return append(append([]string{}, path[i:]...), node)
				}
			}
		}
		if state[node] == 2 {
			return nil
		}
		state[node] = 1
		path = append(path, node)
		for _, next := range edges[node] {
			if cycle := visit(next); len(cycle) > 0 {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[node] = 2
		return nil
	}
	keys := make([]string, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if cycle := visit(key); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

type Scanner interface {
	Scan(context.Context, string) (p1689.Document, error)
}
