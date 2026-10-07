package scheduler

import "time"

// ClockTime 一天中的时刻（日历所在地本地时间）。
type ClockTime struct {
	Hour, Minute int
}

// WorkInterval 一天内的一段工作时段，如 09:00-12:00。
type WorkInterval struct {
	Start, End ClockTime
}

// Calendar 工作日历：周末、节假日、每日工作时段与时区。
//
// 时间语义：所有输入输出都是绝对时刻（time.Time）。每日工作时段按
// 日历所在地的本地时钟解释，再换算为绝对时刻区间。因此夏令时切换
// 只会改变当天可用的绝对工时（如拨快当天 9:00-17:00 只有 7 个绝对
// 小时），绝不会让时间轴倒退或跳变；同一任务在不同时区下看到的
// 开始与结束是同一绝对时刻。
type Calendar struct {
	ID       string
	Location *time.Location
	// WorkDays 为 true 的工作日；nil 表示周一到周五。
	WorkDays map[time.Weekday]bool
	// Holidays 节假日集合，键为 "2006-01-02" 格式的本地日期。
	Holidays map[string]bool
	// Daily 每个工作日的工作时段；为空表示 09:00-17:00。
	Daily []WorkInterval
}

// DefaultCalendar 返回 UTC、周一至周五、09:00-17:00 的默认日历。
func DefaultCalendar(id string) *Calendar {
	return &Calendar{ID: id, Location: time.UTC}
}

const maxScanDays = 366 * 10

func (c *Calendar) loc() *time.Location {
	if c.Location == nil {
		return time.UTC
	}
	return c.Location
}

func (c *Calendar) isWorkWeekday(w time.Weekday) bool {
	if c.WorkDays == nil {
		return w >= time.Monday && w <= time.Friday
	}
	return c.WorkDays[w]
}

func (c *Calendar) daily() []WorkInterval {
	if len(c.Daily) == 0 {
		return []WorkInterval{{Start: ClockTime{9, 0}, End: ClockTime{17, 0}}}
	}
	return c.Daily
}

// dayStart 返回 t 所在本地日的零点（绝对时刻）。
func (c *Calendar) dayStart(t time.Time) time.Time {
	lt := t.In(c.loc())
	y, m, d := lt.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, c.loc())
}

// IsWorkingDay 判断 t 所在本地日是否为工作日。
func (c *Calendar) IsWorkingDay(t time.Time) bool {
	lt := t.In(c.loc())
	if !c.isWorkWeekday(lt.Weekday()) {
		return false
	}
	if c.Holidays != nil && c.Holidays[lt.Format("2006-01-02")] {
		return false
	}
	return true
}

// absInterval 一天内的一段工作时段，已换算为绝对时刻。
type absInterval struct {
	Start, End time.Time
}

// intervalsOn 返回 day（本地日零点）当天的绝对工作区间，按开始时刻升序。
func (c *Calendar) intervalsOn(day time.Time) []absInterval {
	if !c.IsWorkingDay(day) {
		return nil
	}
	y, m, d := day.In(c.loc()).Date()
	var out []absInterval
	for _, w := range c.daily() {
		s := time.Date(y, m, d, w.Start.Hour, w.Start.Minute, 0, 0, c.loc())
		e := time.Date(y, m, d, w.End.Hour, w.End.Minute, 0, 0, c.loc())
		if e.After(s) {
			out = append(out, absInterval{Start: s, End: e})
		}
	}
	// 合并重叠/相邻区间，保证后续扫描正确。
	merged := out[:0]
	for _, iv := range out {
		if len(merged) > 0 && !iv.Start.After(merged[len(merged)-1].End) {
			if iv.End.After(merged[len(merged)-1].End) {
				merged[len(merged)-1].End = iv.End
			}
			continue
		}
		merged = append(merged, iv)
	}
	return merged
}

// intervalContaining 返回包含 t 的工作区间（Start <= t < End），无则返回 false。
func (c *Calendar) intervalContaining(t time.Time) (absInterval, bool) {
	for _, iv := range c.intervalsOn(c.dayStart(t)) {
		if !t.Before(iv.Start) && t.Before(iv.End) {
			return iv, true
		}
	}
	return absInterval{}, false
}

// IsWorking 判断时刻 t 是否落在工作时段内。
func (c *Calendar) IsWorking(t time.Time) bool {
	_, ok := c.intervalContaining(t)
	return ok
}

// NextWorkStart 返回不早于 t 的第一个工作时刻；t 已在工作时段内则返回 t。
func (c *Calendar) NextWorkStart(t time.Time) time.Time {
	day := c.dayStart(t)
	for i := 0; i < maxScanDays; i++ {
		for _, iv := range c.intervalsOn(day) {
			if t.Before(iv.End) {
				if t.After(iv.Start) {
					return t
				}
				return iv.Start
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return t
}

// intervalEndingAt 返回满足 Start < t <= End 的工作区间（反向扫描用，
// 区间末尾视为可用的最后工作时刻）。
func (c *Calendar) intervalEndingAt(t time.Time) (absInterval, bool) {
	for _, iv := range c.intervalsOn(c.dayStart(t)) {
		if t.After(iv.Start) && !t.After(iv.End) {
			return iv, true
		}
	}
	return absInterval{}, false
}

// FloorToWork 若 t 是工作时刻（含区间末尾边界）返回 t，
// 否则返回 t 之前最后一个工作区间的末尾时刻。
func (c *Calendar) FloorToWork(t time.Time) time.Time {
	day := c.dayStart(t)
	for i := 0; i < maxScanDays; i++ {
		ivs := c.intervalsOn(day)
		for j := len(ivs) - 1; j >= 0; j-- {
			iv := ivs[j]
			if t.After(iv.End) {
				return iv.End
			}
			if t.After(iv.Start) {
				return t
			}
			// t 在区间起点或更早：该区间对回推不可用，看更早的区间。
		}
		day = day.AddDate(0, 0, -1)
	}
	return t
}

// AddWork 从 start 起推进 d 的工作时间，返回结束时刻。d 可为负（回推）。
// start 不在工作时段时，正向先对齐到 NextWorkStart，负向先对齐到 FloorToWork。
func (c *Calendar) AddWork(start time.Time, d time.Duration) time.Time {
	if d >= 0 {
		t := c.NextWorkStart(start)
		remaining := d
		for i := 0; i < maxScanDays*4 && remaining > 0; i++ {
			iv, ok := c.intervalContaining(t)
			if !ok {
				// 日历在扫描视界内无工作时刻：停止避免死循环。
				next := c.NextWorkStart(t.Add(time.Nanosecond))
				if !next.After(t) {
					break
				}
				t = next
				continue
			}
			avail := iv.End.Sub(t)
			if avail >= remaining {
				return t.Add(remaining)
			}
			remaining -= avail
			next := c.NextWorkStart(iv.End.Add(time.Nanosecond))
			if !next.After(iv.End) {
				break
			}
			t = next
		}
		return t
	}
	t := c.FloorToWork(start)
	remaining := -d
	for i := 0; i < maxScanDays*4 && remaining > 0; i++ {
		iv, ok := c.intervalEndingAt(t)
		if !ok {
			next := c.FloorToWork(t.Add(-time.Nanosecond))
			if !next.Before(t) {
				break
			}
			t = next
			continue
		}
		avail := t.Sub(iv.Start)
		if avail >= remaining {
			return t.Add(-remaining)
		}
		remaining -= avail
		next := c.FloorToWork(iv.Start)
		if !next.Before(iv.Start) {
			break
		}
		t = next
	}
	return t
}

// WorkBetween 返回 [a, b] 内的工作时间总量；b 早于 a 时返回负值。
func (c *Calendar) WorkBetween(a, b time.Time) time.Duration {
	if b.Equal(a) {
		return 0
	}
	if b.Before(a) {
		return -c.WorkBetween(b, a)
	}
	var total time.Duration
	day := c.dayStart(a)
	for !day.After(b) {
		for _, iv := range c.intervalsOn(day) {
			s := iv.Start
			if a.After(s) {
				s = a
			}
			e := iv.End
			if b.Before(e) {
				e = b
			}
			if e.After(s) {
				total += e.Sub(s)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return total
}
