package buildanalyzer

import (
	"fmt"
	"path/filepath"
	"slices"

	"trestle/internal/ninja"
	"trestle/internal/plan"
)

type Edge struct {
	ID              string   `json:"id"`
	Rule            string   `json:"rule"`
	Outputs         []string `json:"outputs"`
	ImplicitOutputs []string `json:"implicit_outputs,omitempty"`
	Inputs          []string `json:"inputs,omitempty"`
	Implicit        []string `json:"implicit,omitempty"`
	OrderOnly       []string `json:"order_only,omitempty"`
	Discovered      []string `json:"discovered,omitempty"`
	DynamicInputs   []string `json:"dynamic_inputs,omitempty"`
	DynamicOutputs  []string `json:"dynamic_outputs,omitempty"`
	Depfile         string   `json:"depfile,omitempty"`
	Dyndep          string   `json:"dyndep,omitempty"`
	Pool            string   `json:"pool,omitempty"`
	Prerequisites   []int    `json:"prerequisites,omitempty"`
}

type Graph struct {
	Edges     []Edge         `json:"edges"`
	Pools     map[string]int `json:"pools"`
	Complete  bool           `json:"complete"`
	Warnings  []string       `json:"warnings,omitempty"`
	producers map[string]int
}

func newGraph(directory string, buildPlan plan.BuildPlan) *Graph {
	graph := &Graph{Edges: make([]Edge, 0, len(buildPlan.Actions)), Pools: ninja.PoolDepths(buildPlan), Complete: true}
	rules := make(map[plan.ID]plan.Action)
	for _, action := range buildPlan.Actions {
		rule, exists := rules[action.Rule]
		if !exists {
			rule = action
			rules[action.Rule] = action
		}
		edge := Edge{
			ID: string(action.ID), Rule: string(action.Rule), Outputs: slices.Clone(action.Outputs),
			ImplicitOutputs: slices.Clone(action.ImplicitOutputs), Inputs: slices.Clone(action.Inputs),
			Implicit: slices.Clone(action.Implicit), OrderOnly: slices.Clone(action.OrderOnly),
			Dyndep: action.Dyndep, Pool: rule.Pool,
		}
		if rule.Depfile != nil {
			edge.Depfile = rule.Depfile.Path
		}
		graph.Edges = append(graph.Edges, edge)
	}
	graph.loadDynamic(directory, buildPlan, rules)
	graph.index()
	if _, _, err := graph.topological(); err != nil {
		graph.warn(err)
	}
	return graph
}

func (edge Edge) phony() bool { return edge.Rule == "phony" }

func pathKey(path string) string { return filepath.ToSlash(filepath.Clean(path)) }

func diskPath(dir, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dir, name)
}

func (g *Graph) warn(err error) {
	g.Complete = false
	g.Warnings = append(g.Warnings, err.Error())
}

func (g *Graph) index() {
	g.producers = make(map[string]int)
	for i, edge := range g.Edges {
		for _, outputs := range [][]string{edge.Outputs, edge.ImplicitOutputs, edge.DynamicOutputs} {
			for _, output := range outputs {
				key := pathKey(output)
				if previous, exists := g.producers[key]; exists && previous != i {
					g.warn(fmt.Errorf("multiple producers for %q", output))
					continue
				}
				g.producers[key] = i
			}
		}
	}
	for i := range g.Edges {
		edge := &g.Edges[i]
		edge.Prerequisites = nil
		seen := make(map[int]bool)
		for _, inputs := range [][]string{edge.Inputs, edge.Implicit, edge.OrderOnly, edge.Discovered, edge.DynamicInputs} {
			for _, input := range inputs {
				parent, exists := g.producers[pathKey(input)]
				if exists && !seen[parent] {
					edge.Prerequisites = append(edge.Prerequisites, parent)
					seen[parent] = true
				}
			}
		}
		slices.Sort(edge.Prerequisites)
	}
}

func (g *Graph) topological() ([]int, [][]int, error) {
	children := make([][]int, len(g.Edges))
	remaining := make([]int, len(g.Edges))
	order := make([]int, 0, len(g.Edges))
	for index, edge := range g.Edges {
		remaining[index] = len(edge.Prerequisites)
		if remaining[index] == 0 {
			order = append(order, index)
		}
		for _, parent := range edge.Prerequisites {
			if parent < 0 || parent >= len(g.Edges) {
				return nil, nil, fmt.Errorf("invalid prerequisite index %d", parent)
			}
			children[parent] = append(children[parent], index)
		}
	}
	for i := 0; i < len(order); i++ {
		for _, child := range children[order[i]] {
			remaining[child]--
			if remaining[child] == 0 {
				order = append(order, child)
			}
		}
	}
	if len(order) != len(g.Edges) {
		return nil, nil, fmt.Errorf("edge graph contains a cycle")
	}
	return order, children, nil
}
