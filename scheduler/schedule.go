package scheduler

import "time"

// scheduleContext 一次排期计算的上下文。
type scheduleContext struct {
	in    *Input
	graph *depGraph
	order []string
	// 每个任务的计算结果（按 ID）。
	res map[string]*TaskResult
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

// validate 校验输入定义完整性。
func validate(in *Input) error { return nil }

// forwardPass 计算所有任务的最早开始/完成（含约束，不含资源调平）。
func (c *scheduleContext) forwardPass() {}

// backwardPass 计算尽晚开始/完成与总浮动、关键路径。
func (c *scheduleContext) backwardPass() {}

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
	ctx := &scheduleContext{in: in, graph: g, order: order, res: map[string]*TaskResult{}}
	for _, id := range order {
		ctx.res[id] = &TaskResult{TaskID: id}
	}
	ctx.forwardPass()
	levelResources(ctx)
	ctx.backwardPass()
	return &Result{
		Tasks:         ctx.res,
		CriticalPath:  criticalPath(ctx),
		ProjectStart:  in.ProjectStart,
		ProjectFinish: projectFinish(ctx),
	}, nil
}

// criticalPath 返回关键路径上的任务 ID（按时间顺序）。
func criticalPath(c *scheduleContext) []string { return nil }

// projectFinish 返回项目完成时刻。
func projectFinish(c *scheduleContext) time.Time { return c.in.ProjectStart }
