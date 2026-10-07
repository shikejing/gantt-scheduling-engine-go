package scheduler

import "time"

// WorkCalendar 排期引擎使用的日历抽象：*Calendar 或若干日历的交集。
type WorkCalendar interface {
	// NextWorkStart 返回不早于 t 的第一个工作时刻。
	NextWorkStart(t time.Time) time.Time
	// FloorToWork 返回不晚于 t 的最后一个工作时刻（区间末尾边界可用）。
	FloorToWork(t time.Time) time.Time
	// AddWork 从 t 推进 d 的工作时间（d 可为负）。
	AddWork(t time.Time, d time.Duration) time.Time
	// WorkBetween 返回 [a, b] 内的工作时间；b 早于 a 时返回负值。
	WorkBetween(a, b time.Time) time.Duration
	// IsWorking 判断 t 是否为工作时刻。
	IsWorking(t time.Time) bool
}

// intersectCalendar 多个日历的交集：只有所有成员日历都工作的时刻才算工作。
// 用于"任务日历 ∩ 各资源日历"，跨时区协作时任务只能在各方共同的工作
// 时段内推进。所有计算仍基于绝对时刻，夏令时安全。
type intersectCalendar struct {
	cals []*Calendar
}

// intersectOf 构造交集日历；单成员时直接返回该成员。
func intersectOf(cals ...*Calendar) WorkCalendar {
	flat := make([]*Calendar, 0, len(cals))
	for _, c := range cals {
		if c != nil {
			flat = append(flat, c)
		}
	}
	if len(flat) == 1 {
		return flat[0]
	}
	return &intersectCalendar{cals: flat}
}

const maxFixpointIters = 10000

func (x *intersectCalendar) IsWorking(t time.Time) bool {
	for _, c := range x.cals {
		if !c.IsWorking(t) {
			return false
		}
	}
	return true
}

// currentEnd 返回包含 t 的公共工作区间的结束时刻（各成员区间末的最小值）。
func (x *intersectCalendar) currentEnd(t time.Time) (time.Time, bool) {
	var end time.Time
	for i, c := range x.cals {
		iv, ok := c.intervalContaining(t)
		if !ok {
			return time.Time{}, false
		}
		if i == 0 || iv.End.Before(end) {
			end = iv.End
		}
	}
	return end, true
}

// NextWorkStart 各成员日历 NextWorkStart 的不动点（单调不减，必收敛或触底返回）。
// 若交集在扫描视界（10 年）内没有公共工作时刻，返回原始 t（调用方可用
// IsWorking 检测失败，避免死循环）。
func (x *intersectCalendar) NextWorkStart(t time.Time) time.Time {
	orig := t
	horizon := t.AddDate(10, 0, 0)
	for i := 0; i < maxFixpointIters; i++ {
		prev := t
		for _, c := range x.cals {
			t = c.NextWorkStart(t)
		}
		if !t.After(prev) {
			return t
		}
		if t.After(horizon) {
			return orig
		}
	}
	return orig
}

func (x *intersectCalendar) FloorToWork(t time.Time) time.Time {
	orig := t
	horizon := t.AddDate(-10, 0, 0)
	for i := 0; i < maxFixpointIters; i++ {
		prev := t
		for _, c := range x.cals {
			t = c.FloorToWork(t)
		}
		if !t.Before(prev) {
			return t
		}
		if t.Before(horizon) {
			return orig
		}
	}
	return orig
}

func (x *intersectCalendar) AddWork(start time.Time, d time.Duration) time.Time {
	if d >= 0 {
		t := x.NextWorkStart(start)
		remaining := d
		for i := 0; i < maxFixpointIters && remaining > 0; i++ {
			end, ok := x.currentEnd(t)
			if !ok {
				next := x.NextWorkStart(t.Add(time.Nanosecond))
				if !next.After(t) {
					break // 交集无可用工作时刻，避免死循环
				}
				t = next
				continue
			}
			avail := end.Sub(t)
			if avail >= remaining {
				return t.Add(remaining)
			}
			remaining -= avail
			next := x.NextWorkStart(end.Add(time.Nanosecond))
			if !next.After(end) {
				break
			}
			t = next
		}
		return t
	}
	t := x.FloorToWork(start)
	remaining := -d
	for i := 0; i < maxFixpointIters && remaining > 0; i++ {
		beg, ok := x.currentBegin(t)
		if !ok {
			next := x.FloorToWork(t.Add(-time.Nanosecond))
			if !next.Before(t) {
				break
			}
			t = next
			continue
		}
		avail := t.Sub(beg)
		if avail >= remaining {
			return t.Add(-remaining)
		}
		remaining -= avail
		next := x.FloorToWork(beg)
		if !next.Before(beg) {
			break
		}
		t = next
	}
	return t
}

// currentBegin 返回 t 所在公共工作区间的开始时刻（各成员区间始的最大值）。
func (x *intersectCalendar) currentBegin(t time.Time) (time.Time, bool) {
	var beg time.Time
	for i, c := range x.cals {
		iv, ok := c.intervalEndingAt(t)
		if !ok {
			return time.Time{}, false
		}
		if i == 0 || iv.Start.After(beg) {
			beg = iv.Start
		}
	}
	return beg, true
}

func (x *intersectCalendar) WorkBetween(a, b time.Time) time.Duration {
	if !b.After(a) {
		if b.Equal(a) {
			return 0
		}
		return -x.WorkBetween(b, a)
	}
	var total time.Duration
	t := a
	for i := 0; i < maxFixpointIters && t.Before(b); i++ {
		t = x.NextWorkStart(t)
		if !t.Before(b) {
			break
		}
		end, ok := x.currentEnd(t)
		if !ok || !end.After(t) {
			break
		}
		if end.After(b) {
			end = b
		}
		total += end.Sub(t)
		t = end
	}
	return total
}
