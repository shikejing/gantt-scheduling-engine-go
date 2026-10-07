package gantt

import "time"

// WorkInterval 一天内的工作时段（本地时间）。
type WorkInterval struct {
	StartHour, StartMin int
	EndHour, EndMin     int
}

// Calendar 工作日历：每周工作日、节假日、每日工作时段、时区。
// 所有输出均为绝对时刻；同一时刻在不同时区日历下是同一瞬时值。
type Calendar struct {
	ID        string
	Location  *time.Location
	WorkDays  [7]bool // 索引为 time.Weekday
	Holidays  map[string]bool
	Intervals []WorkInterval // 为空表示全天 24h 工作
}

// NewCalendar 创建标准日历（周一至周五 9:00-17:00）。
func NewCalendar(id string, loc *time.Location) *Calendar {
	c := &Calendar{
		ID:       id,
		Location: loc,
		Holidays: map[string]bool{},
		Intervals: []WorkInterval{
			{StartHour: 9, EndHour: 17},
		},
	}
	for d := time.Monday; d <= time.Friday; d++ {
		c.WorkDays[d] = true
	}
	return c
}

// AddHoliday 添加节假日（本地日期）。
func (c *Calendar) AddHoliday(t time.Time) {
	c.Holidays[t.In(c.Location).Format("2006-01-02")] = true
}

// IsWorkDay 判断某本地日期是否为工作日。
func (c *Calendar) IsWorkDay(t time.Time) bool {
	lt := t.In(c.Location)
	if !c.WorkDays[lt.Weekday()] {
		return false
	}
	return !c.Holidays[lt.Format("2006-01-02")]
}

// dailyWork 每日工作时长（名义值，夏令时日的实际绝对时长会伸缩）。
func (c *Calendar) dailyWork() time.Duration {
	if len(c.Intervals) == 0 {
		return 24 * time.Hour
	}
	var d time.Duration
	for _, iv := range c.Intervals {
		d += time.Duration(iv.EndHour-iv.StartHour)*time.Hour +
			time.Duration(iv.EndMin-iv.StartMin)*time.Minute
	}
	return d
}

// dayStart 返回 t 所在本地日的零点（绝对时刻）。
func (c *Calendar) dayStart(t time.Time) time.Time {
	lt := t.In(c.Location)
	y, m, d := lt.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, c.Location)
}

// windowsOnDay 返回本地日 day（其零点为 dayStart）的工作窗口（绝对时刻，升序）。
// 窗口由本地时间定义，经 time.Date 转换：夏令时切换日窗口绝对时长伸缩，
// 但窗口之间不会重叠也不会倒退（本地日严格递增）。
func (c *Calendar) windowsOnDay(dayStart time.Time) [][2]time.Time {
	if !c.IsWorkDay(dayStart) {
		return nil
	}
	ld := dayStart.In(c.Location)
	y, m, d := ld.Date()
	if len(c.Intervals) == 0 {
		return [][2]time.Time{{dayStart, dayStart.AddDate(0, 0, 1)}}
	}
	out := make([][2]time.Time, 0, len(c.Intervals))
	for _, iv := range c.Intervals {
		s := time.Date(y, m, d, iv.StartHour, iv.StartMin, 0, 0, c.Location)
		e := time.Date(y, m, d, iv.EndHour, iv.EndMin, 0, 0, c.Location)
		if e.After(s) {
			out = append(out, [2]time.Time{s, e})
		}
	}
	return out
}

// NextWorkStart 返回 >= t 的第一个工作时刻。
func (c *Calendar) NextWorkStart(t time.Time) time.Time {
	day := c.dayStart(t)
	for i := 0; i < 3700; i++ { // 上限防止异常日历死循环
		for _, w := range c.windowsOnDay(day) {
			if !w[1].After(t) {
				continue
			}
			if w[0].After(t) {
				return w[0]
			}
			return t
		}
		day = day.AddDate(0, 0, 1)
	}
	return t
}

// IsWorkTime 判断 t 是否落在工作时间内。
func (c *Calendar) IsWorkTime(t time.Time) bool {
	return c.NextWorkStart(t).Equal(t)
}

// AddWork 从 t 起推进 work 工作时长，返回结束时刻。
// t 若非工作时刻会先推进到下一工作时刻。work<=0 时返回 NextWorkStart(t)。
func (c *Calendar) AddWork(t time.Time, work time.Duration) time.Time {
	cur := c.NextWorkStart(t)
	if work <= 0 {
		return cur
	}
	day := c.dayStart(cur)
	for i := 0; i < 37000; i++ {
		for _, w := range c.windowsOnDay(day) {
			if !w[1].After(cur) {
				continue
			}
			s := w[0]
			if cur.After(s) {
				s = cur
			}
			avail := w[1].Sub(s)
			if work <= avail {
				return s.Add(work)
			}
			work -= avail
		}
		day = day.AddDate(0, 0, 1)
	}
	return cur
}

// PrevWorkEnd 返回 <= t 的最后一个工作时刻（工作窗口的右端点语义）。
func (c *Calendar) PrevWorkEnd(t time.Time) time.Time {
	day := c.dayStart(t)
	for i := 0; i < 3700; i++ {
		ws := c.windowsOnDay(day)
		for j := len(ws) - 1; j >= 0; j-- {
			w := ws[j]
			if !w[0].Before(t) {
				continue
			}
			if w[1].Before(t) {
				return w[1]
			}
			return t
		}
		day = day.AddDate(0, 0, -1)
	}
	return t
}

// SubWork 从 t 倒退 work 工作时长，返回开始时刻。
// t 若非工作时刻会先回退到前一工作时刻。
func (c *Calendar) SubWork(t time.Time, work time.Duration) time.Time {
	cur := c.PrevWorkEnd(t)
	if work <= 0 {
		return cur
	}
	day := c.dayStart(cur)
	for i := 0; i < 37000; i++ {
		ws := c.windowsOnDay(day)
		for j := len(ws) - 1; j >= 0; j-- {
			w := ws[j]
			if !w[0].Before(cur) {
				continue
			}
			e := w[1]
			if cur.Before(e) {
				e = cur
			}
			avail := e.Sub(w[0])
			if work <= avail {
				return e.Add(-work)
			}
			work -= avail
		}
		day = day.AddDate(0, 0, -1)
	}
	return cur
}

// WorkBetween 返回 [from, to) 内的工作时长；to 早于 from 时返回负值（对称）。
func (c *Calendar) WorkBetween(from, to time.Time) time.Duration {
	if to.Before(from) {
		return -c.WorkBetween(to, from)
	}
	var total time.Duration
	day := c.dayStart(from)
	for !day.After(to) {
		for _, w := range c.windowsOnDay(day) {
			s := w[0]
			if from.After(s) {
				s = from
			}
			e := w[1]
			if to.Before(e) {
				e = to
			}
			if e.After(s) {
				total += e.Sub(s)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return total
}
