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
// 所有计算基于绝对时刻（time.Time），夏令时切换不会使时间轴倒退或跳变：
// 同一任务在不同时区下看到的开始与结束是同一时刻。
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

// IsWorkingDay 判断 t 所在本地日是否为工作日。
func (c *Calendar) IsWorkingDay(t time.Time) bool { return false }

// IsWorking 判断时刻 t 是否落在工作时段内。
func (c *Calendar) IsWorking(t time.Time) bool { return false }

// NextWorkStart 返回不早于 t 的第一个工作时刻；t 已在工作时段内则返回 t。
func (c *Calendar) NextWorkStart(t time.Time) time.Time { return t }

// AddWork 从 start 起推进 d 的工作时间，返回结束时刻。d 可为负（回推）。
func (c *Calendar) AddWork(start time.Time, d time.Duration) time.Time { return start }

// WorkBetween 返回 [a, b] 内的工作时间总量；b 早于 a 时返回负值。
func (c *Calendar) WorkBetween(a, b time.Time) time.Duration { return 0 }

// FloorToWork 若 t 在工作时段内返回 t，否则返回 t 之前最后一个工作时刻。
func (c *Calendar) FloorToWork(t time.Time) time.Time { return t }
