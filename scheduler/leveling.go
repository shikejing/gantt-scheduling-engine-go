package scheduler

import (
	"sort"
	"time"
)

// booking 资源上的一段已排占用（绝对时间区间）。
type booking struct {
	start, finish time.Time
	units         float64
	taskID        string
}

// resourcePool 调平过程中单个资源的占用台账。
type resourcePool struct {
	capacity float64
	bookings []booking // 按 start 升序
}

// conflicts 返回 [start, finish) 内与 units 叠加后超出产能的占用。
func (rp *resourcePool) conflicts(start, finish time.Time, units float64) []booking {
	var out []booking
	for _, b := range rp.bookings {
		if b.finish.After(start) && b.start.Before(finish) {
			if b.units+units > rp.capacity {
				out = append(out, b)
			}
		}
	}
	return out
}

func (rp *resourcePool) add(b booking) {
	rp.bookings = append(rp.bookings, b)
	sort.Slice(rp.bookings, func(i, j int) bool {
		if !rp.bookings[i].start.Equal(rp.bookings[j].start) {
			return rp.bookings[i].start.Before(rp.bookings[j].start)
		}
		return rp.bookings[i].taskID < rp.bookings[j].taskID
	})
}

// levelResources 在 CPM 最早时间基础上做资源调平。
//
// 算法：串行调度生成方案（SGS）。每一步从“前置已全部排定”的任务中，
// 按 (优先级升序, 最早开始升序, 任务ID升序) 选出下一个任务，排在满足
// 依赖下界与资源产能的最早时刻。选择顺序是全序，因此同一输入必然得到
// 同一结果。调平只会把任务往后推，且推迟只会来自依赖或资源冲突。
func levelResources(c *scheduleContext) {
	pools := map[string]*resourcePool{}
	for id, r := range c.in.Resources {
		pools[id] = &resourcePool{capacity: r.Capacity}
	}

	// 每个任务剩余未排前置数。
	remaining := map[string]int{}
	for _, id := range c.order {
		remaining[id] = len(c.graph.preds[id])
	}
	scheduled := map[string]bool{}
	eligible := map[string]bool{}
	for _, id := range c.order {
		if remaining[id] == 0 {
			eligible[id] = true
		}
	}

	pick := func() string {
		best := ""
		for id := range eligible {
			if best == "" || lessForLeveling(c, id, best) {
				best = id
			}
		}
		return best
	}

	for len(eligible) > 0 {
		id := pick()
		delete(eligible, id)
		t := c.in.Tasks[id]
		cal := c.effectiveCalendar(t)
		r := c.res[id]

		// 依赖下界：用前置调平后的实际时间重算。
		depBound := cal.NextWorkStart(c.in.ProjectStart)
		var depCulprit string
		for _, dep := range t.Predecessors {
			pr := c.res[dep.PredecessorID]
			if b := depStartBound(cal, dep, pr.Start, pr.Finish, t.Duration); b.After(depBound) {
				depBound = b
				depCulprit = dep.PredecessorID
			}
		}

		var start, finish time.Time
		var blockers []blocker
		if t.Pinned {
			// 钉住任务固定不动，但仍占用资源，其他任务绕着排。
			start = t.PinnedStart
			finish = cal.AddWork(start, t.Duration)
		} else {
			start = r.EarlyStart
			if depBound.After(start) {
				start = depBound
			}
			start, finish, blockers = fitResources(c, pools, t, cal, start)
		}

		r.Start, r.Finish = start, finish
		scheduled[id] = true

		// 记录推迟原因：只可能是依赖或资源冲突。
		if r.Start.After(r.EarlyStart) {
			r.Delayed = true
			if depBound.After(r.EarlyStart) && depCulprit != "" {
				r.DelayReasons = append(r.DelayReasons, DelayReason{
					Kind: DelayByDependency, TaskID: depCulprit, Until: depBound,
				})
			}
			if r.Start.After(depBound) {
				for _, b := range blockers {
					r.DelayReasons = append(r.DelayReasons, DelayReason{
						Kind: DelayByResource, TaskID: b.taskID, ResourceID: b.resourceID, Until: b.finish,
					})
				}
			}
		}

		for _, ra := range t.Resources {
			pools[ra.ResourceID].add(booking{start: start, finish: finish, units: ra.Units, taskID: id})
		}
		for _, s := range c.graph.succs[id] {
			remaining[s]--
			if remaining[s] == 0 {
				eligible[s] = true
			}
		}
	}
}

// lessForLeveling 调平选择顺序：优先级升序、最早开始升序、ID 升序。
func lessForLeveling(c *scheduleContext, a, b string) bool {
	ta, tb := c.in.Tasks[a], c.in.Tasks[b]
	if ta.Priority != tb.Priority {
		return ta.Priority < tb.Priority
	}
	ea, eb := c.res[a].EarlyStart, c.res[b].EarlyStart
	if !ea.Equal(eb) {
		return ea.Before(eb)
	}
	return a < b
}

// blocker 一次资源冲突的肇事占用。
type blocker struct {
	taskID     string
	resourceID string
	finish     time.Time
}

// fitResources 找到不早于 want 且满足所有资源产能的最早开始。
// 任务占用按 [start, finish) 绝对区间计；冲突时跳到冲突占用结束后的
// 下一个工作时刻重试，直到可行。返回途中遇到的冲突来源（去重后）。
func fitResources(c *scheduleContext, pools map[string]*resourcePool, t *Task, cal WorkCalendar, want time.Time) (time.Time, time.Time, []blocker) {
	var blockers []blocker
	start := cal.NextWorkStart(want)
	for {
		finish := cal.AddWork(start, t.Duration)
		var worst *booking
		var worstRes string
		for _, ra := range t.Resources {
			rp := pools[ra.ResourceID]
			for _, b := range rp.conflicts(start, finish, ra.Units) {
				if worst == nil || b.finish.After(worst.finish) {
					bb := b
					worst = &bb
					worstRes = ra.ResourceID
				}
			}
		}
		if worst == nil {
			return start, finish, dedupBlockers(blockers)
		}
		blockers = append(blockers, blocker{taskID: worst.taskID, resourceID: worstRes, finish: worst.finish})
		start = cal.NextWorkStart(worst.finish)
	}
}

// dedupBlockers 按 (taskID, resourceID) 去重，保留首次出现，保证输出确定。
func dedupBlockers(in []blocker) []blocker {
	seen := map[blocker]bool{}
	out := in[:0]
	for _, b := range in {
		key := blocker{taskID: b.taskID, resourceID: b.resourceID}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, b)
	}
	return out
}
