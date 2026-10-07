package gantt

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("timezone %s unavailable: %v", name, err)
	}
	return loc
}

// 周末与节假日：跨周末任务应在下周一继续。
func TestCalendarWeekendHoliday(t *testing.T) {
	loc := time.UTC
	cal := NewCalendar("std", loc)
	// 2026-01-05 是周一；1 月 7 日设为节假日
	cal.AddHoliday(time.Date(2026, 1, 7, 0, 0, 0, 0, loc))
	start := time.Date(2026, 1, 9, 9, 0, 0, 0, loc)  // 周五
	end := cal.AddWork(start, 16*time.Hour)          // 周五8h + 下周一8h
	want := time.Date(2026, 1, 12, 17, 0, 0, 0, loc) // 下周一 17:00
	if !end.Equal(want) {
		t.Errorf("weekend skip: got %s want %s", end, want)
	}
	// 节假日：1/6(周二) 起 16h -> 周二8h + 周四8h（周三节假日）
	end2 := cal.AddWork(time.Date(2026, 1, 6, 9, 0, 0, 0, loc), 16*time.Hour)
	want2 := time.Date(2026, 1, 8, 17, 0, 0, 0, loc)
	if !end2.Equal(want2) {
		t.Errorf("holiday skip: got %s want %s", end2, want2)
	}
}

// 夏令时：春季拨快当日，时间轴不得倒退或跳变；
// 同一时刻在不同时区日历下是同一瞬时。
func TestCalendarDSTSpringForward(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	cal := NewCalendar("ny", loc)
	// 2026-03-08 周日拨快；用 2026-03-09(周一) 验证窗口单调
	// 验证整个 DST 周 NextWorkStart 单调不减
	cur := time.Date(2026, 3, 6, 0, 0, 0, 0, loc)
	prev := cur
	for i := 0; i < 200; i++ {
		cur = cal.NextWorkStart(cur.Add(time.Hour))
		if cur.Before(prev) {
			t.Fatalf("timeline went backwards: %s < %s", cur, prev)
		}
		prev = cur
	}
	// 2026 年春季切换是 3 月 8 日（周日），验证跨切换周末的任务：
	// 周五 9:00 起 24h 工作 = 周五8h + 周一8h + 周二8h
	start := time.Date(2026, 3, 6, 9, 0, 0, 0, loc)
	end := cal.AddWork(start, 24*time.Hour)
	want := time.Date(2026, 3, 10, 17, 0, 0, 0, loc)
	if !end.Equal(want) {
		t.Errorf("got %s want %s", end, want)
	}
	// 同一时刻在东京日历下是同一瞬时
	tokyo := mustLoc(t, "Asia/Tokyo")
	if !end.In(tokyo).Equal(end) {
		t.Error("same instant must be equal across timezones")
	}
}

// 秋季拨回：当地时间 11 月 1 日（周日）拨回，窗口不得重叠倒退。
func TestCalendarDSTFallBack(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	cal := NewCalendar("ny", loc)
	cur := time.Date(2026, 10, 30, 0, 0, 0, 0, loc)
	prev := cur
	for i := 0; i < 200; i++ {
		cur = cal.NextWorkStart(cur.Add(time.Hour))
		if cur.Before(prev) {
			t.Fatalf("timeline went backwards: %s < %s", cur, prev)
		}
		prev = cur
	}
}

// 跨时区协作：同一任务在不同时区日历下看到的开始与结束是同一时刻。
func TestCrossTimezoneSameInstant(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	tokyo := mustLoc(t, "Asia/Tokyo")
	calNY := NewCalendar("ny", ny)
	calTK := NewCalendar("tk", tokyo)
	p := NewPlan("tz", time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC))
	p.Calendars["ny"] = calNY
	p.Calendars["tk"] = calTK
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour, CalendarID: "ny"}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour, CalendarID: "tk"}
	p.Dependencies = []Dependency{{From: "A", To: "B", Type: DepFS}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Tasks["A"], res.Tasks["B"]
	// B 的开始 >= A 的结束（绝对时刻），且二者是同一瞬时语义
	if b.Start.Before(a.Finish) {
		t.Errorf("B starts before A finishes: %s < %s", b.Start, a.Finish)
	}
	// 不同时区表示同一时刻
	if !a.Start.In(tokyo).Equal(a.Start) || !a.Finish.In(ny).Equal(a.Finish) {
		t.Error("instants differ across timezone representations")
	}
	t.Logf("A(NY): %s -> %s | B(TK): %s -> %s",
		a.Start.In(ny), a.Finish.In(ny), b.Start.In(tokyo), b.Finish.In(tokyo))
}

// WorkBetween 与 AddWork 互逆。
func TestWorkBetweenInverse(t *testing.T) {
	cal := NewCalendar("std", time.UTC)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	end := cal.AddWork(start, 30*time.Hour)
	got := cal.WorkBetween(start, end)
	if got != 30*time.Hour {
		t.Errorf("WorkBetween got %v want 30h", got)
	}
	if w := cal.WorkBetween(end, start); w != -30*time.Hour {
		t.Errorf("negative WorkBetween got %v", w)
	}
}
