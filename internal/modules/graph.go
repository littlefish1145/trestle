package modules

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"trestle/internal/modules/p1689"
)

// Node is one translation unit in the module graph, with the modules it
// provides and the modules it requires.
type Node struct {
	TranslationUnit string
	Provides        []p1689.ModuleKey
	Requires        []p1689.ModuleKey
}

// Graph is the module dependency graph: every scanned translation unit as a
// node, and edges from a provider to each translation unit that requires it.
type Graph struct {
	Nodes []Node
	Edges map[string][]string
}

// BuildGraph assembles scans into a graph, rejecting duplicate providers,
// missing providers, and provider cycles so the build order is well-defined.
func BuildGraph(scans []p1689.Document) (Graph, error) {
	providers := map[string]string{}
	nodes := make([]Node, 0, len(scans))
	for _, scan := range scans {
		for _, rule := range scan.Rules {
			node, err := nodeFromRule(rule, providers)
			if err != nil {
				return Graph{}, err
			}
			nodes = append(nodes, node)
		}
	}
	byTU := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		byTU[node.TranslationUnit] = node
	}
	edges, err := buildEdges(nodes, providers)
	if err != nil {
		return Graph{}, err
	}
	if cycle := findCycle(edges); len(cycle) > 0 {
		return Graph{}, fmt.Errorf("E_MODULE_CYCLE: %s", strings.Join(cycle, " -> "))
	}
	return Graph{Nodes: nodes, Edges: edges}, nil
}

// nodeFromRule converts one p1689 rule into a graph node, registering the
// modules it provides and rejecting a module that two translation units provide.
func nodeFromRule(rule p1689.Rule, providers map[string]string) (Node, error) {
	tu := rule.PrimaryOutput
	if tu == "" && len(rule.Outputs) > 0 {
		tu = rule.Outputs[0]
	}
	if tu == "" {
		return Node{}, fmt.Errorf("E_MODULE_SCAN_FAILED: rule has no translation unit output")
	}
	tu = filepath.Clean(tu)
	node := Node{TranslationUnit: tu}
	for _, module := range rule.Provides {
		key := module.Key()
		if previous, ok := providers[key.String()]; ok && previous != tu {
			return Node{}, fmt.Errorf("E_MODULE_DUPLICATE_PROVIDER: module %q is provided by %s and %s", key.String(), previous, tu)
		}
		providers[key.String()] = tu
		node.Provides = append(node.Provides, key)
	}
	for _, module := range rule.Requires {
		node.Requires = append(node.Requires, module.Key())
	}
	return node, nil
}

// buildEdges links each provider to the translation units that require it,
// rejecting a requirement whose module has no provider.
func buildEdges(nodes []Node, providers map[string]string) (map[string][]string, error) {
	edges := make(map[string][]string)
	for _, node := range nodes {
		for _, requirement := range node.Requires {
			provider, ok := providers[requirement.String()]
			if !ok {
				return nil, fmt.Errorf("E_MODULE_MISSING_PROVIDER: %s requires %q but no provider exists", node.TranslationUnit, requirement.String())
			}
			edges[provider] = appendUnique(edges[provider], node.TranslationUnit)
		}
	}
	return edges, nil
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

// Scanner produces a p1689 module dependency document for a single source file.
type Scanner interface {
	Scan(context.Context, string) (p1689.Document, error)
}
