package scheduler

import "sort"

// depGraph 任务依赖图，按任务 ID 建索引。
type depGraph struct {
	// preds[t] = t 的前置任务 ID 列表（排序去重后）。
	preds map[string][]string
	// succs[t] = t 的后续任务 ID 列表（排序去重后）。
	succs map[string][]string
}

// buildGraph 从输入构建依赖图。未知任务 ID 报 ValidationError。
func buildGraph(in *Input) (*depGraph, error) {
	g := &depGraph{preds: map[string][]string{}, succs: map[string][]string{}}
	for id := range in.Tasks {
		g.preds[id] = nil
		g.succs[id] = nil
	}
	for id, t := range in.Tasks {
		seen := map[string]bool{}
		for _, d := range t.Predecessors {
			if d.PredecessorID == id {
				return nil, &CycleError{Path: []string{id, id}}
			}
			if _, ok := in.Tasks[d.PredecessorID]; !ok {
				return nil, &ValidationError{Detail: "task " + id + " depends on unknown task " + d.PredecessorID}
			}
			if seen[d.PredecessorID] {
				continue
			}
			seen[d.PredecessorID] = true
			g.preds[id] = append(g.preds[id], d.PredecessorID)
			g.succs[d.PredecessorID] = append(g.succs[d.PredecessorID], id)
		}
	}
	for id := range g.preds {
		sort.Strings(g.preds[id])
	}
	for id := range g.succs {
		sort.Strings(g.succs[id])
	}
	return g, nil
}

// topoOrder 返回按依赖序排列的任务 ID（前置先于后续，同级按 ID 字典序，保证确定性）。
// 存在环路时返回 *CycleError，Path 为环上的任务序列。
func (g *depGraph) topoOrder() ([]string, error) {
	indeg := map[string]int{}
	for id := range g.preds {
		indeg[id] = len(g.preds[id])
	}
	var ready []string
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		var next []string
		for _, s := range g.succs[id] {
			indeg[s]--
			if indeg[s] == 0 {
				next = append(next, s)
			}
		}
		if len(next) > 0 {
			ready = append(ready, next...)
			sort.Strings(ready)
		}
	}
	if len(order) == len(g.preds) {
		return order, nil
	}
	return nil, &CycleError{Path: g.findCycle()}
}

// findCycle 用 DFS 找出一条环路（用于错误报告）。
func (g *depGraph) findCycle() []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var dfs func(id string) bool
	dfs = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		for _, s := range g.succs[id] {
			if color[s] == gray {
				// 找到环：从 s 在栈中的位置到栈顶。
				for i, v := range stack {
					if v == s {
						cycle = append(append([]string{}, stack[i:]...), s)
						return true
					}
				}
			}
			if color[s] == white && dfs(s) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	ids := make([]string, 0, len(g.preds))
	for id := range g.preds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white && dfs(id) {
			return cycle
		}
	}
	return nil
}

// descendants 返回从 seeds 出发沿后续边可达的全部任务（含 seeds）。
func (g *depGraph) descendants(seeds map[string]bool) map[string]bool {
	out := map[string]bool{}
	var stack []string
	for id := range seeds {
		if !out[id] {
			out[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range g.succs[id] {
			if !out[s] {
				out[s] = true
				stack = append(stack, s)
			}
		}
	}
	return out
}

// ancestors 返回沿前置边可达的全部任务（含 seeds）。
func (g *depGraph) ancestors(seeds map[string]bool) map[string]bool {
	out := map[string]bool{}
	var stack []string
	for id := range seeds {
		if !out[id] {
			out[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range g.preds[id] {
			if !out[p] {
				out[p] = true
				stack = append(stack, p)
			}
		}
	}
	return out
}
