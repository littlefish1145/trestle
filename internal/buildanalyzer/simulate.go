package buildanalyzer

import (
	"container/heap"
	"slices"
)

type simTask struct {
	duration, previous, predicted int64
}

type Simulation struct {
	Jobs         int   `json:"jobs"`
	Tasks        int   `json:"tasks"`
	LastSampleMS int64 `json:"last_sample_policy_ms"`
	MedianMS     int64 `json:"median_policy_ms"`
}

type completion struct {
	at   int64
	edge int
}

type completions []completion

func (queue completions) Len() int           { return len(queue) }
func (queue completions) Less(i, j int) bool { return queue[i].at < queue[j].at }
func (queue completions) Swap(i, j int)      { queue[i], queue[j] = queue[j], queue[i] }
func (queue *completions) Push(value any)    { *queue = append(*queue, value.(completion)) }
func (queue *completions) Pop() any {
	last := len(*queue) - 1
	value := (*queue)[last]
	*queue = (*queue)[:last]
	return value
}

type readyQueue struct {
	edges    []int
	priority []int64
}

func (queue readyQueue) Len() int { return len(queue.edges) }
func (queue readyQueue) Less(i, j int) bool {
	return precedes(queue.edges[i], queue.edges[j], queue.priority)
}
func (queue readyQueue) Swap(i, j int) {
	queue.edges[i], queue.edges[j] = queue.edges[j], queue.edges[i]
}
func (queue *readyQueue) Push(value any) { queue.edges = append(queue.edges, value.(int)) }
func (queue *readyQueue) Pop() any {
	last := len(queue.edges) - 1
	value := queue.edges[last]
	queue.edges = queue.edges[:last]
	return value
}

func precedes(a, b int, priority []int64) bool {
	return b < 0 || priority[a] > priority[b] || (priority[a] == priority[b] && a < b)
}

func simulate(graph *Graph, tasks map[int]simTask, jobs int) *Simulation {
	order, children, err := graph.topological()
	if err != nil || jobs <= 0 {
		return nil
	}
	for index := range tasks {
		pool := graph.Edges[index].Pool
		if pool != "" && graph.Pools[pool] <= 0 {
			return nil
		}
	}
	baseline, ok := simulatePolicy(graph, tasks, jobs, order, children, false)
	if !ok {
		return nil
	}
	historical, ok := simulatePolicy(graph, tasks, jobs, order, children, true)
	if !ok {
		return nil
	}
	return &Simulation{jobs, len(tasks), baseline, historical}
}

func simulatePolicy(graph *Graph, tasks map[int]simTask, jobs int, order []int, children [][]int, history bool) (int64, bool) {
	priority := make([]int64, len(order))
	for _, index := range slices.Backward(order) {
		task := tasks[index]
		weight := task.previous
		if history {
			weight = task.predicted
		}
		for _, child := range children[index] {
			priority[index] = max(priority[index], priority[child])
		}
		priority[index] += weight
	}
	remaining := make([]int, len(order))
	var newlyReady []int
	for _, index := range order {
		remaining[index] = len(graph.Edges[index].Prerequisites)
		if remaining[index] == 0 {
			newlyReady = append(newlyReady, index)
		}
	}
	unblock := func(index int) {
		for _, child := range children[index] {
			remaining[child]--
			if remaining[child] == 0 {
				newlyReady = append(newlyReady, child)
			}
		}
	}
	var running completions
	ready := map[string]*readyQueue{}
	poolRunning := map[string]int{}
	finished := 0
	var now int64
	for finished < len(order) {
		for i := 0; i < len(newlyReady); i++ {
			index := newlyReady[i]
			if _, dirty := tasks[index]; !dirty {
				finished++
				unblock(index)
				continue
			}
			pool := graph.Edges[index].Pool
			if ready[pool] == nil {
				ready[pool] = &readyQueue{priority: priority}
			}
			heap.Push(ready[pool], index)
		}
		newlyReady = newlyReady[:0]
		for running.Len() < jobs {
			index, selectedPool := -1, ""
			for pool, queue := range ready {
				if queue.Len() > 0 && (pool == "" || poolRunning[pool] < graph.Pools[pool]) &&
					precedes(queue.edges[0], index, priority) {
					index, selectedPool = queue.edges[0], pool
				}
			}
			if index < 0 {
				break
			}
			heap.Pop(ready[selectedPool])
			poolRunning[selectedPool]++
			heap.Push(&running, completion{now + tasks[index].duration, index})
		}
		if running.Len() == 0 {
			return now, finished == len(order)
		}
		now = running[0].at
		for running.Len() > 0 && running[0].at == now {
			index := heap.Pop(&running).(completion).edge
			poolRunning[graph.Edges[index].Pool]--
			finished++
			unblock(index)
		}
	}
	return now, true
}
