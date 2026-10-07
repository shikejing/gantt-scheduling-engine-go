package gantt

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// 可区分的失败原因。
var (
	ErrCycle         = errors.New("gantt: dependency cycle detected")
	ErrUnknownTask   = errors.New("gantt: unknown task in dependency")
	ErrInfeasible    = errors.New("gantt: constraints infeasible")
	ErrUnknownCal    = errors.New("gantt: unknown calendar")
	ErrUnknownRes    = errors.New("gantt: unknown resource")
	ErrDuplicateTask = errors.New("gantt: duplicate task id")
)

// CycleError 携带环路上的任务，便于定位。
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("%v: %v", ErrCycle, e.Path)
}

func (e *CycleError) Unwrap() error { return ErrCycle }

// calFor 解析任务的日历：任务日历 > 计划默认日历 > 内置 24/7 UTC 日历。
func calFor(p *Plan, calID string) (*Calendar, error) {
	id := calID
	if id == "" {
		id = p.DefaultCal
	}
	if id == "" {
		return defaultUTC, nil
	}
	c, ok := p.Calendars[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCal, id)
	}
	return c, nil
}

var defaultUTC = &Calendar{
	ID:       "$utc24",
	Location: time.UTC,
	WorkDays: [7]bool{true, true, true, true, true, true, true},
	Holidays: map[string]bool{},
}

// validate 校验计划定义。
func validate(p *Plan) error {
	if _, err := calFor(p, ""); err != nil {
		return err
	}
	for _, t := range p.Tasks {
		if t == nil {
			return fmt.Errorf("%w: nil task", ErrUnknownTask)
		}
		if t.Duration < 0 {
			return fmt.Errorf("%w: task %s negative duration", ErrInfeasible, t.ID)
		}
		if _, err := calFor(p, t.CalendarID); err != nil {
			return err
		}
		for _, ra := range t.Resources {
			r, ok := p.Resources[ra.ResourceID]
			if !ok {
				return fmt.Errorf("%w: %s", ErrUnknownRes, ra.ResourceID)
			}
			if ra.Units <= 0 || ra.Units > r.Capacity {
				return fmt.Errorf("%w: task %s units %g on resource %s capacity %g",
					ErrInfeasible, t.ID, ra.Units, r.ID, r.Capacity)
			}
		}
	}
	for _, d := range p.Dependencies {
		if _, ok := p.Tasks[d.From]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTask, d.From)
		}
		if _, ok := p.Tasks[d.To]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTask, d.To)
		}
	}
	return nil
}

// topoSort 拓扑排序（确定性：就绪集按 ID 排序）；发现环时返回 *CycleError。
func topoSort(tasks map[string]*Task, deps []Dependency) ([]string, error) {
	indeg := map[string]int{}
	adj := map[string][]string{}
	for id := range tasks {
		indeg[id] = 0
	}
	for _, d := range deps {
		indeg[d.To]++
		adj[d.From] = append(adj[d.From], d.To)
	}
	var ready []string
	for id, n := range indeg {
		if n == 0 {
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
		for _, m := range adj[id] {
			indeg[m]--
			if indeg[m] == 0 {
				next = append(next, m)
			}
		}
		sort.Strings(next)
		// 归并保持 ready 有序
		merged := make([]string, 0, len(ready)+len(next))
		i, j := 0, 0
		for i < len(ready) && j < len(next) {
			if ready[i] < next[j] {
				merged = append(merged, ready[i])
				i++
			} else {
				merged = append(merged, next[j])
				j++
			}
		}
		merged = append(merged, ready[i:]...)
		merged = append(merged, next[j:]...)
		ready = merged
	}
	if len(order) == len(tasks) {
		return order, nil
	}
	// 提取环路路径
	remaining := map[string]bool{}
	for id, n := range indeg {
		if n > 0 {
			remaining[id] = true
		}
	}
	path := findCycle(remaining, adj)
	return nil, &CycleError{Path: path}
}

func findCycle(nodes map[string]bool, adj map[string][]string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var dfs func(u string) bool
	dfs = func(u string) bool {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range adj[u] {
			if !nodes[v] {
				continue
			}
			if color[v] == gray {
				// 找到环：从 v 在栈中的位置起
				idx := 0
				for i, s := range stack {
					if s == v {
						idx = i
						break
					}
				}
				cycle = append([]string{}, stack[idx:]...)
				cycle = append(cycle, v)
				return true
			}
			if color[v] == white && dfs(v) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return false
	}
	for u := range nodes {
		if color[u] == white && dfs(u) {
			return cycle
		}
	}
	return nil
}

// depBounds 依赖对后继任务的最早时刻约束。
// 返回对 to 的 start 约束与 finish 约束（零值表示无）。
func depBounds(p *Plan, d Dependency, es, ef map[string]time.Time) (minStart, minFinish time.Time, err error) {
	calA, err := calFor(p, p.Tasks[d.From].CalendarID)
	if err != nil {
		return
	}
	lag := d.Lag
	switch d.Type {
	case DepFS:
		minStart = calA.AddWork(ef[d.From], lag)
	case DepSS:
		minStart = calA.AddWork(es[d.From], lag)
	case DepFF:
		minFinish = calA.AddWork(ef[d.From], lag)
	case DepSF:
		minFinish = calA.AddWork(es[d.From], lag)
	}
	return
}

// cpmForward 前推计算最早开始/完成（含 MSO 下界、钉住任务）。
// pins 覆盖计划内 Pins（用于 ALAP 二次迭代）。
func cpmForward(p *Plan, order []string, pins map[string]time.Time) (map[string]time.Time, map[string]time.Time, error) {
	es := map[string]time.Time{}
	ef := map[string]time.Time{}
	preds := map[string][]Dependency{}
	for _, d := range p.Dependencies {
		preds[d.To] = append(preds[d.To], d)
	}
	for _, id := range order {
		t := p.Tasks[id]
		cal, err := calFor(p, t.CalendarID)
		if err != nil {
			return nil, nil, err
		}
		start := cal.NextWorkStart(p.ProjectStart)
		if t.Constraint.Type == ConstraintMSO {
			if s := cal.NextWorkStart(t.Constraint.Date); s.After(start) {
				start = s
			}
		}
		for _, d := range preds[id] {
			ms, mf, err := depBounds(p, d, es, ef)
			if err != nil {
				return nil, nil, err
			}
			if !ms.IsZero() && ms.After(start) {
				start = ms
			}
			if !mf.IsZero() {
				// finish 下界换算为 start 下界
				if s := cal.SubWork(mf, t.Duration); s.After(start) {
					start = s
				}
			}
		}
		if pin, ok := pins[id]; ok {
			start = pin
		}
		start = cal.NextWorkStart(start)
		es[id] = start
		ef[id] = cal.AddWork(start, t.Duration)
	}
	return es, ef, nil
}

// cpmBackward 回推计算总浮动与最晚开始。projectEnd 为汇点默认最晚完成。
// MFO 约束作为死线参与回推，可能产生负浮动（如实保留）。
func cpmBackward(p *Plan, order []string, es, ef map[string]time.Time, projectEnd time.Time) (map[string]time.Duration, map[string]time.Time, error) {
	lf := map[string]time.Time{}
	lsMap := map[string]time.Time{}
	succs := map[string][]Dependency{}
	for _, d := range p.Dependencies {
		succs[d.From] = append(succs[d.From], d)
	}
	floatMap := map[string]time.Duration{}
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		t := p.Tasks[id]
		cal, err := calFor(p, t.CalendarID)
		if err != nil {
			return nil, nil, err
		}
		end := projectEnd
		if t.Constraint.Type == ConstraintMFO && t.Constraint.Date.Before(end) {
			end = t.Constraint.Date
		}
		if t.Pinned {
			if pin, ok := p.Pins[id]; ok {
				end = cal.AddWork(pin, t.Duration)
			}
		}
		best := end
		for _, d := range succs[id] {
			calA, err := calFor(p, t.CalendarID)
			if err != nil {
				return nil, nil, err
			}
			lsB := calBSub(p, d.To, lf[d.To], p.Tasks[d.To].Duration)
			lfB := lf[d.To]
			var bound time.Time
			switch d.Type {
			case DepFS:
				bound = calA.SubWork(lsB, d.Lag)
			case DepSS:
				// start_A <= lsB - lag  =>  finish_A <= lsB - lag + durA
				bound = calA.AddWork(calA.SubWork(lsB, d.Lag), t.Duration)
			case DepFF:
				bound = calA.SubWork(lfB, d.Lag)
			case DepSF:
				bound = calA.SubWork(lfB, d.Lag)
			}
			if bound.Before(best) {
				best = bound
			}
		}
		lf[id] = best
		ls := cal.SubWork(best, t.Duration)
		lsMap[id] = ls
		floatMap[id] = cal.WorkBetween(es[id], ls)
	}
	return floatMap, lsMap, nil
}

// calBSub 用任务 taskID 的日历执行 SubWork。
func calBSub(p *Plan, taskID string, t time.Time, d time.Duration) time.Time {
	cal, err := calFor(p, p.Tasks[taskID].CalendarID)
	if err != nil {
		return t.Add(-d)
	}
	return cal.SubWork(t, d)
}

// levelResources 串行调度法资源调平。
// 输入 CPM 最早时刻；输出调平后的开始/完成与推迟原因。确定性。
func levelResources(p *Plan, order []string, es, ef map[string]time.Time) (map[string]time.Time, map[string]time.Time, map[string][]DelayReason, error) {
	start := map[string]time.Time{}
	finish := map[string]time.Time{}
	reasons := map[string][]DelayReason{}
	// 资源占用：resourceID -> 已排任务区间列表
	used := map[string][]resUsage{}
	preds := map[string][]Dependency{}
	for _, d := range p.Dependencies {
		preds[d.To] = append(preds[d.To], d)
	}
	scheduled := map[string]bool{}
	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}
	remaining := map[string]bool{}
	for id := range p.Tasks {
		remaining[id] = true
	}
	for len(remaining) > 0 {
		// 候选：前置全部已排
		var cand []string
		for id := range remaining {
			ok := true
			for _, d := range preds[id] {
				if !scheduled[d.From] {
					ok = false
					break
				}
			}
			if ok {
				cand = append(cand, id)
			}
		}
		if len(cand) == 0 {
			return nil, nil, nil, &CycleError{Path: []string{"(leveling)"}}
		}
		// 优先级小者优先，其次拓扑序，最后 ID，保证确定
		sort.Slice(cand, func(i, j int) bool {
			a, b := p.Tasks[cand[i]], p.Tasks[cand[j]]
			if a.Priority != b.Priority {
				return a.Priority < b.Priority
			}
			if rank[cand[i]] != rank[cand[j]] {
				return rank[cand[i]] < rank[cand[j]]
			}
			return cand[i] < cand[j]
		})
		id := cand[0]
		t := p.Tasks[id]
		cal, err := calFor(p, t.CalendarID)
		if err != nil {
			return nil, nil, nil, err
		}
		// 依赖下界（基于已排定的前置实际时刻）
		s := es[id]
		for _, d := range preds[id] {
			ms, mf, err := depBounds(p, d, start, finish)
			if err != nil {
				return nil, nil, nil, err
			}
			if !ms.IsZero() && ms.After(s) {
				s = ms
				reasons[id] = append(reasons[id], DelayReason{Kind: "dependency", TaskID: d.From, Until: ms})
			}
			if !mf.IsZero() {
				if ss := cal.SubWork(mf, t.Duration); ss.After(s) {
					s = ss
					reasons[id] = append(reasons[id], DelayReason{Kind: "dependency", TaskID: d.From, Until: ss})
				}
			}
		}
		if pin, ok := p.Pins[id]; ok && t.Pinned {
			s = pin
		}
		s = cal.NextWorkStart(s)
		// 资源冲突调平：推进到所有占用资源均有产能的最早时刻
		for {
			f := cal.AddWork(s, t.Duration)
			conflictRes, until := findConflict(p, t, used, s, f)
			if conflictRes == "" {
				break
			}
			ns := cal.NextWorkStart(until)
			if !ns.After(s) {
				ns = cal.NextWorkStart(s.Add(time.Minute))
			}
			reasons[id] = append(reasons[id], DelayReason{Kind: "resource", Resource: conflictRes, Until: ns})
			s = ns
		}
		f := cal.AddWork(s, t.Duration)
		start[id] = s
		finish[id] = f
		for _, ra := range t.Resources {
			used[ra.ResourceID] = append(used[ra.ResourceID], resUsage{s: s, f: f, units: ra.Units, task: id})
		}
		scheduled[id] = true
		delete(remaining, id)
	}
	return start, finish, reasons, nil
}

// resUsage 资源上的一段占用。
type resUsage struct {
	s, f  time.Time
	units float64
	task  string
}

// findConflict 检查任务在 [s,f) 内是否超出某资源产能；
// 若有，返回资源 ID 与可解除冲突的最早时刻（冲突段内某占用区间的最早结束）。
// 按资源 ID 升序检查，保证确定性。
func findConflict(p *Plan, t *Task, used map[string][]resUsage, s, f time.Time) (string, time.Time) {
	resIDs := make([]string, 0, len(t.Resources))
	unitsOf := map[string]float64{}
	for _, ra := range t.Resources {
		resIDs = append(resIDs, ra.ResourceID)
		unitsOf[ra.ResourceID] = ra.Units
	}
	sort.Strings(resIDs)
	for _, rid := range resIDs {
		r := p.Resources[rid]
		need := unitsOf[rid]
		// 收集 [s,f) 内的占用区间端点
		var points []time.Time
		var segs []resUsage
		for _, u := range used[rid] {
			if u.f.After(s) && u.s.Before(f) {
				segs = append(segs, u)
				points = append(points, maxTime(u.s, s), minTime(u.f, f))
			}
		}
		if len(segs) == 0 {
			continue
		}
		points = append(points, s)
		sortTimes(points)
		for _, ts := range points {
			if !ts.Before(f) {
				continue
			}
			var load float64
			var minEnd time.Time
			for _, u := range segs {
				if !u.s.After(ts) && u.f.After(ts) {
					load += u.units
					if minEnd.IsZero() || u.f.Before(minEnd) {
						minEnd = u.f
					}
				}
			}
			if load+need > r.Capacity+1e-9 {
				return rid, minEnd
			}
		}
	}
	return "", time.Time{}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func sortTimes(ts []time.Time) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
}

// Schedule 全量排算：CPM 前推/回推 + 约束 + 资源调平。
// 纯函数：不修改 p，结果确定可复现。
func Schedule(p *Plan) (*ScheduleResult, error) {
	if err := validate(p); err != nil {
		return nil, err
	}
	order, err := topoSort(p.Tasks, p.Dependencies)
	if err != nil {
		return nil, err
	}
	// 第一轮：前推 + 回推
	es, ef, err := cpmForward(p, order, p.Pins)
	if err != nil {
		return nil, err
	}
	projectEnd := maxOf(ef)
	floats, ls, err := cpmBackward(p, order, es, ef, projectEnd)
	if err != nil {
		return nil, err
	}
	// ALAP：把尽晚任务强制到其最晚开始，重算一轮（确定性两轮收敛）
	hasALAP := false
	for _, t := range p.Tasks {
		if t.Constraint.Type == ConstraintALAP {
			hasALAP = true
			break
		}
	}
	if hasALAP {
		forced := map[string]time.Time{}
		for k, v := range p.Pins {
			forced[k] = v
		}
		var alapIDs []string
		for id, t := range p.Tasks {
			if t.Constraint.Type == ConstraintALAP {
				alapIDs = append(alapIDs, id)
			}
		}
		sort.Strings(alapIDs)
		for _, id := range alapIDs {
			forced[id] = ls[id]
		}
		es, ef, err = cpmForward(p, order, forced)
		if err != nil {
			return nil, err
		}
		projectEnd = maxOf(ef)
		floats, _, err = cpmBackward(p, order, es, ef, projectEnd)
		if err != nil {
			return nil, err
		}
	}
	// 资源调平
	start, finish, reasons, err := levelResources(p, order, es, ef)
	if err != nil {
		return nil, err
	}
	res := &ScheduleResult{
		Tasks:        map[string]*ScheduledTask{},
		ProjectStart: minOf(start),
		ProjectEnd:   maxOf(finish),
	}
	for id := range p.Tasks {
		res.Tasks[id] = &ScheduledTask{
			TaskID:       id,
			Start:        start[id],
			Finish:       finish[id],
			TotalFloat:   floats[id],
			Critical:     floats[id] <= 0,
			DelayReasons: reasons[id],
		}
	}
	res.CriticalPath = criticalPath(p, floats, start)
	return res, nil
}

func maxOf(m map[string]time.Time) time.Time {
	var max time.Time
	for _, v := range m {
		if v.After(max) {
			max = v
		}
	}
	return max
}

func minOf(m map[string]time.Time) time.Time {
	var min time.Time
	first := true
	for _, v := range m {
		if first || v.Before(min) {
			min = v
			first = false
		}
	}
	return min
}

// criticalPath 从零浮动任务中提取一条从首到尾相连的关键路径（确定性）。
func criticalPath(p *Plan, floats map[string]time.Duration, start map[string]time.Time) []string {
	zero := map[string]bool{}
	for id, f := range floats {
		if f == 0 {
			zero[id] = true
		}
	}
	if len(zero) == 0 {
		return nil
	}
	succs := map[string][]string{}
	for _, d := range p.Dependencies {
		succs[d.From] = append(succs[d.From], d.To)
	}
	// 起点：开始最早的零浮动任务（并列取 ID 小者）
	var first string
	for id := range zero {
		if first == "" ||
			start[id].Before(start[first]) ||
			(start[id].Equal(start[first]) && id < first) {
			first = id
		}
	}
	path := []string{first}
	cur := first
	for {
		var next string
		for _, m := range succs[cur] {
			if !zero[m] {
				continue
			}
			if next == "" ||
				start[m].Before(start[next]) ||
				(start[m].Equal(start[next]) && m < next) {
				next = m
			}
		}
		if next == "" {
			break
		}
		path = append(path, next)
		cur = next
	}
	return path
}
