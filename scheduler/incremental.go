package scheduler

import "time"

// rescheduleIncremental 在旧版本基础上做增量重算。
//
// 正确性论证（保证与从头全量重算逐项一致）：
//   - 任务的最早时间只依赖其自身定义与全部前置的最早时间，因此前向脏集
//     dirtyF = 被改任务 ∪ 其依赖下游之外的任务，最早时间必然不变，可复用；
//   - 任务的尽晚时间只依赖其自身定义与全部后续的尽晚时间以及全局
//     cpmFinish，因此后向脏集 dirtyB = 被改任务 ∪ 其依赖上游之外的任务
//     尽晚时间不变；若 cpmFinish 发生变化，则所有任务的尽晚时间都可能
//     变化，dirtyB 扩大为全集；
//   - 资源调平是（全部任务的最早时间, 优先级, 产能）的纯函数，而最早时间
//     已与全量重算一致，故调平结果与全量一致；
//   - 含 ALAP 约束时直接回退全量重算（ALAP 的下界来自全局后向传递）。
func rescheduleIncremental(old *Version, next *Input, changed map[string]bool) (*Result, error) {
	if len(changed) == 0 {
		return old.Result, nil
	}
	if err := validate(next); err != nil {
		return nil, err
	}
	g, err := buildGraph(next)
	if err != nil {
		return nil, err
	}
	order, err := g.topoOrder()
	if err != nil {
		return nil, err
	}
	for _, t := range next.Tasks {
		if t.Constraint.Type == ConstraintALAP {
			return Schedule(next) // ALAP 回退全量
		}
	}

	// 脏集必须同时在新旧两张依赖图上求闭包：被删除的依赖边在新图上
	// 已不存在，但旧边上的前驱（尽晚时间失去一个后继上界）与后继
	// （最早时间失去一个前驱下界）同样受影响。
	dirtyF := g.descendants(changed)
	dirtyB := g.ancestors(changed)
	if gOld, err := buildGraph(old.Input); err == nil {
		for id := range gOld.descendants(changed) {
			dirtyF[id] = true
		}
		for id := range gOld.ancestors(changed) {
			dirtyB[id] = true
		}
	}

	ctx := &scheduleContext{
		in: next, graph: g, order: order,
		res:       map[string]*TaskResult{},
		alapFloor: map[string]time.Time{},
		effCal:    map[string]WorkCalendar{},
	}
	// 按字段复用旧结果：前向字段（Early*）只在前向干净时复用，
	// 后向字段（Late*/浮动）只在后向干净时复用；Start/Finish/推迟原因
	// 由调平阶段统一重写，无需复用。
	for _, id := range order {
		r := &TaskResult{TaskID: id}
		if oldR, ok := old.Result.Tasks[id]; ok {
			if !dirtyF[id] {
				r.EarlyStart, r.EarlyFinish = oldR.EarlyStart, oldR.EarlyFinish
			}
			if !dirtyB[id] {
				r.LateStart, r.LateFinish = oldR.LateStart, oldR.LateFinish
				r.TotalFloat, r.Critical, r.NegativeFloat = oldR.TotalFloat, oldR.Critical, oldR.NegativeFloat
			}
		}
		ctx.res[id] = r
	}
	// 只对可能受影响的任务探测有效日历可用性。
	for id := range dirtyF {
		t, ok := next.Tasks[id]
		if !ok {
			continue
		}
		eff := ctx.effectiveCalendarProbe(t)
		if probe := eff.NextWorkStart(next.ProjectStart); !eff.IsWorking(probe) {
			return nil, &ValidationError{Detail: "task " + id + " has no working time within 10 years of ProjectStart in the intersection of its calendars"}
		}
	}

	ctx.forwardPass(dirtyF)

	// cpmFinish 变化会使所有任务的尽晚时间失效。
	ctx.cpmFinish = next.ProjectStart
	for _, id := range order {
		if ef := ctx.res[id].EarlyFinish; ef.After(ctx.cpmFinish) {
			ctx.cpmFinish = ef
		}
	}
	if !ctx.cpmFinish.Equal(old.Result.cpmFinish) {
		dirtyB = nil // 全量后向
	}
	ctx.backwardPass(dirtyB)

	// 浮动同时依赖最早与尽晚时间：仅前向变脏的任务（后向干净、浮动字段
	// 被复用）需要用新最早时间重算浮动。后向脏集内的任务已在 backwardPass
	// 中重算；dirtyB == nil 表示全量后向，全部已重算。
	if dirtyB != nil {
		for id := range dirtyF {
			if dirtyB[id] {
				continue
			}
			t, ok := next.Tasks[id]
			if !ok {
				continue
			}
			r := ctx.res[id]
			cal := ctx.effectiveCalendar(t)
			r.TotalFloat = cal.WorkBetween(r.EarlyStart, r.LateStart)
			r.Critical = r.TotalFloat == 0
			r.NegativeFloat = r.TotalFloat < 0
		}
	}

	levelResources(ctx)
	return &Result{
		Tasks:         ctx.res,
		CriticalPath:  criticalPath(ctx),
		ProjectStart:  next.ProjectStart,
		ProjectFinish: projectFinish(ctx),
		cpmFinish:     ctx.cpmFinish,
	}, nil
}

// effectiveCalendarProbe 同 effectiveCalendar，供增量字面量保持可读。
func (c *scheduleContext) effectiveCalendarProbe(t *Task) WorkCalendar {
	return c.effectiveCalendar(t)
}
