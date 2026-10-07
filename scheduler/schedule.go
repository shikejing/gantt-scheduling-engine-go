package scheduler

import (
	"fmt"
	"sort"
	"time"
)

// scheduleContext 一次排期计算的上下文。
type scheduleContext struct {
	in    *Input
	graph *depGraph
	order []string
	// 每个任务的计算结果（按 ID）。
	res map[string]*TaskResult
	// alapFloor ALAP 任务在后向传递后得到的尽晚开始，作为第二次前向传递的下界。
	alapFloor map[string]time.Time
	// effCal 每个任务的有效日历（任务日历 ∩ 所占用资源日历），惰性构建。
	effCal map[string]WorkCalendar
	// cpmFinish 全网络（未调平）的最大最早完成，作为后向传递的项目完成。
	cpmFinish time.Time
}

// calendarOf 返回任务使用的日历。
func (c *scheduleContext) calendarOf(t *Task) *Calendar {
	if t.CalendarID != "" {
		if cal, ok := c.in.Calendars[t.CalendarID]; ok {
			return cal
		}
	}
	return c.in.Calendars[c.in.DefaultCalendarID]
}

// effectiveCalendar 返回任务的有效日历：任务日历与全部占用资源日历的交集。
// 跨时区协作时，任务只能在其自身日历与资源日历共同的工作时段内推进。
func (c *scheduleContext) effectiveCalendar(t *Task) WorkCalendar {
	if wc, ok := c.effCal[t.ID]; ok {
		return wc
	}
	cals := []*Calendar{c.calendarOf(t)}
	for _, ra := range t.Resources {
		if r, ok := c.in.Resources[ra.ResourceID]; ok {
			cals = append(cals, c.resourceCalendarOf(r))
		}
	}
	wc := intersectOf(cals...)
	c.effCal[t.ID] = wc
	return wc
}

// resourceCalendarOf 返回资源使用的日历。
func (c *scheduleContext) resourceCalendarOf(r *Resource) *Calendar {
	if r.CalendarID != "" {
		if cal, ok := c.in.Calendars[r.CalendarID]; ok {
			return cal
		}
	}
	return c.in.Calendars[c.in.DefaultCalendarID]
}

// validate 校验输入定义完整性。
func validate(in *Input) error {
	if in == nil {
		return &ValidationError{Detail: "nil input"}
	}
	if in.ProjectStart.IsZero() {
		return &ValidationError{Detail: "ProjectStart is required (zero value not allowed)"}
	}
	if _, ok := in.Calendars[in.DefaultCalendarID]; !ok {
		return &ValidationError{Detail: fmt.Sprintf("default calendar %q not found", in.DefaultCalendarID)}
	}
	for id, t := range in.Tasks {
		if t == nil {
			return &ValidationError{Detail: "nil task " + id}
		}
		if t.ID != id {
			return &ValidationError{Detail: fmt.Sprintf("task key %q != task.ID %q", id, t.ID)}
		}
		if t.Duration < 0 {
			return &ValidationError{Detail: "task " + id + " has negative duration"}
		}
		if t.CalendarID != "" {
			if _, ok := in.Calendars[t.CalendarID]; !ok {
				return &ValidationError{Detail: fmt.Sprintf("task %s references unknown calendar %q", id, t.CalendarID)}
			}
		}
		for _, ra := range t.Resources {
			r, ok := in.Resources[ra.ResourceID]
			if !ok {
				return &ValidationError{Detail: fmt.Sprintf("task %s references unknown resource %q", id, ra.ResourceID)}
			}
			if ra.Units <= 0 {
				return &ValidationError{Detail: fmt.Sprintf("task %s assignment to %s has non-positive units", id, ra.ResourceID)}
			}
			if ra.Units > r.Capacity {
				return &ValidationError{Detail: fmt.Sprintf("task %s requires %g units of %s but capacity is %g", id, ra.Units, ra.ResourceID, r.Capacity)}
			}
		}
		if t.Pinned && t.PinnedStart.IsZero() {
			return &ValidationError{Detail: "pinned task " + id + " missing PinnedStart"}
		}
	}
	for id, r := range in.Resources {
		if r.Capacity <= 0 {
			return &ValidationError{Detail: "resource " + id + " has non-positive capacity"}
		}
		if r.CalendarID != "" {
			if _, ok := in.Calendars[r.CalendarID]; !ok {
				return &ValidationError{Detail: fmt.Sprintf("resource %s references unknown calendar %q", id, r.CalendarID)}
			}
		}
	}
	return nil
}

// depStartBound 由前置的（最早或调平后）时间推后续开始的下界。
// lag 按后续任务日历的工作时间计。
func depStartBound(cal WorkCalendar, dep Dependency, predStart, predFinish time.Time, succDur time.Duration) time.Time {
	switch dep.Type {
	case StartToStart:
		return cal.AddWork(predStart, dep.Lag)
	case FinishToFinish:
		return cal.AddWork(cal.AddWork(predFinish, dep.Lag), -succDur)
	case StartToFinish:
		return cal.AddWork(cal.AddWork(predStart, dep.Lag), -succDur)
	default: // FinishToStart
		return cal.AddWork(predFinish, dep.Lag)
	}
}

// depLateCap 由后续的尽晚时间推前置完成的下界（前向依赖的反向）。
func depLateCap(cal WorkCalendar, dep Dependency, succLS, succLF time.Time, predDur time.Duration) time.Time {
	switch dep.Type {
	case StartToStart:
		return cal.AddWork(cal.AddWork(succLS, -dep.Lag), predDur)
	case FinishToFinish:
		return cal.AddWork(succLF, -dep.Lag)
	case StartToFinish:
		return cal.AddWork(cal.AddWork(succLF, -dep.Lag), predDur)
	default: // FinishToStart
		return cal.AddWork(succLS, -dep.Lag)
	}
}

// forwardPass 计算任务的最早开始/完成（含约束，不含资源调平）。
// dirty 非空时只重算脏集中的任务，其余复用 ctx.res 中已有值。
func (c *scheduleContext) forwardPass(dirty map[string]bool) {
	for _, id := range c.order {
		if dirty != nil && !dirty[id] {
			continue
		}
		t := c.in.Tasks[id]
		cal := c.effectiveCalendar(t)
		r := c.res[id]
		if t.Pinned {
			// 钉住任务不参与重排：开始时间固定。
			r.EarlyStart = t.PinnedStart
			r.EarlyFinish = cal.AddWork(t.PinnedStart, t.Duration)
			continue
		}
		es := cal.NextWorkStart(c.in.ProjectStart)
		for _, dep := range t.Predecessors {
			pr := c.res[dep.PredecessorID]
			if b := depStartBound(cal, dep, pr.EarlyStart, pr.EarlyFinish, t.Duration); b.After(es) {
				es = b
			}
		}
		switch t.Constraint.Type {
		case ConstraintMustStartOn:
			es = cal.NextWorkStart(t.Constraint.Date)
		case ConstraintStartNoEarlierThan:
			if d := cal.NextWorkStart(t.Constraint.Date); d.After(es) {
				es = d
			}
		}
		if floor, ok := c.alapFloor[id]; ok && floor.After(es) {
			es = floor
		}
		r.EarlyStart = es
		r.EarlyFinish = cal.AddWork(es, t.Duration)
	}
	c.cpmFinish = c.in.ProjectStart
	for _, id := range c.order {
		if ef := c.res[id].EarlyFinish; ef.After(c.cpmFinish) {
			c.cpmFinish = ef
		}
	}
}

// backwardPass 计算尽晚开始/完成与总浮动（基于未调平的 CPM 网络）。
// dirty 非空时只重算脏集中的任务。
func (c *scheduleContext) backwardPass(dirty map[string]bool) {
	for i := len(c.order) - 1; i >= 0; i-- {
		id := c.order[i]
		if dirty != nil && !dirty[id] {
			continue
		}
		t := c.in.Tasks[id]
		cal := c.effectiveCalendar(t)
		r := c.res[id]
		lf := c.cpmFinish
		for _, sid := range c.graph.succs[id] {
			sr := c.res[sid]
			for _, dep := range c.in.Tasks[sid].Predecessors {
				if dep.PredecessorID != id {
					continue
				}
				if cap := depLateCap(cal, dep, sr.LateStart, sr.LateFinish, t.Duration); cap.Before(lf) {
					lf = cap
				}
			}
		}
		switch t.Constraint.Type {
		case ConstraintFinishNoLaterThan:
			if d := cal.FloorToWork(t.Constraint.Date); d.Before(lf) {
				lf = d
			}
		case ConstraintMustStartOn:
			// 固定开始：尽晚完成不得晚于最早完成，浮动归零或变负。
			if ef := r.EarlyFinish; ef.Before(lf) {
				lf = ef
			}
		}
		r.LateFinish = lf
		r.LateStart = cal.AddWork(lf, -t.Duration)
		r.TotalFloat = cal.WorkBetween(r.EarlyStart, r.LateStart)
		r.Critical = r.TotalFloat == 0
		r.NegativeFloat = r.TotalFloat < 0
	}
}

// adjustALAP 把 ALAP 任务钉到其尽晚开始，作为下界再做一次前向/后向传递。
func (c *scheduleContext) adjustALAP() {
	has := false
	for i := len(c.order) - 1; i >= 0; i-- {
		id := c.order[i]
		if c.in.Tasks[id].Constraint.Type == ConstraintALAP && !c.in.Tasks[id].Pinned {
			c.alapFloor[id] = c.res[id].LateStart
			has = true
		}
	}
	if !has {
		return
	}
	c.forwardPass(nil)
	c.backwardPass(nil)
}

// Schedule 对输入做全量排期：环路检测 -> CPM -> 资源调平 -> 浮动与关键路径。
// 返回的 Result 不可变，可安全并发读取。
func Schedule(in *Input) (*Result, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	g, err := buildGraph(in)
	if err != nil {
		return nil, err
	}
	order, err := g.topoOrder()
	if err != nil {
		return nil, err
	}
	ctx := &scheduleContext{in: in, graph: g, order: order, res: map[string]*TaskResult{}, alapFloor: map[string]time.Time{}, effCal: map[string]WorkCalendar{}}
	for _, id := range order {
		ctx.res[id] = &TaskResult{TaskID: id}
	}
	for _, id := range ctx.order {
		eff := ctx.effectiveCalendar(in.Tasks[id])
		if probe := eff.NextWorkStart(in.ProjectStart); !eff.IsWorking(probe) {
			return nil, &ValidationError{Detail: "task " + id + " has no working time within 10 years of ProjectStart in the intersection of its calendars"}
		}
	}
	ctx.forwardPass(nil)
	ctx.backwardPass(nil)
	ctx.adjustALAP()
	levelResources(ctx)
	return &Result{
		Tasks:         ctx.res,
		CriticalPath:  criticalPath(ctx),
		ProjectStart:  in.ProjectStart,
		ProjectFinish: projectFinish(ctx),
		cpmFinish:     ctx.cpmFinish,
	}, nil
}

// criticalPath 返回关键路径上的任务 ID（按时间顺序，确定可复现）。
func criticalPath(c *scheduleContext) []string {
	crit := map[string]bool{}
	for id, r := range c.res {
		if r.Critical {
			crit[id] = true
		}
	}
	if len(crit) == 0 {
		return nil
	}
	// 起点：没有关键前置的关键任务，取 (EarlyStart, ID) 最小者。
	var start string
	for id := range crit {
		hasCritPred := false
		for _, p := range c.graph.preds[id] {
			if crit[p] {
				hasCritPred = true
				break
			}
		}
		if hasCritPred {
			continue
		}
		if start == "" || lessByStartID(c.res, id, start) {
			start = id
		}
	}
	var path []string
	for cur := start; cur != ""; {
		path = append(path, cur)
		next := ""
		for _, s := range c.graph.succs[cur] {
			if !crit[s] {
				continue
			}
			if next == "" || lessByStartID(c.res, s, next) {
				next = s
			}
		}
		cur = next
	}
	return path
}

func lessByStartID(res map[string]*TaskResult, a, b string) bool {
	ea, eb := res[a].EarlyStart, res[b].EarlyStart
	if !ea.Equal(eb) {
		return ea.Before(eb)
	}
	return a < b
}

// projectFinish 返回项目完成时刻（调平后的最大完成）。
func projectFinish(c *scheduleContext) time.Time {
	fin := c.in.ProjectStart
	first := true
	for _, r := range c.res {
		if first || r.Finish.After(fin) {
			fin = r.Finish
			first = false
		}
	}
	return fin
}

// sortedTaskIDs 返回排序后的任务 ID，保证输出确定。
func sortedTaskIDs(m map[string]*TaskResult) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
