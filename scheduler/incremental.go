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

	dirtyF := g.descendants(changed)
	dirtyB := g.ancestors(changed)

	ctx := &scheduleContext{
		in: next, graph: g, order: order,
		res:       map[string]*TaskResult{},
		alapFloor: map[string]time.Time{},
		effCal:    map[string]WorkCalendar{},
	}
	// 干净任务复用旧结果（结构拷贝，随后调平会重写 Start/Finish）。
	for _, id := range order {
		r := &TaskResult{TaskID: id}
		if !dirtyF[id] && !dirtyB[id] {
			if oldR, ok := old.Result.Tasks[id]; ok {
				*r = *oldR
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
