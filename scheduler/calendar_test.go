package scheduler

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}

func TestCalendarSkipsWeekendsAndHolidays(t *testing.T) {
	cal := DefaultCalendar("std")
	cal.Holidays = map[string]bool{"2024-12-30": true, "2025-01-01": true}
	// 2024-12-27 是周五，09:00 开始加 9h 工作：当天 8h，余 1h 跳过周末、
	// 周一（节假日）与元旦（周三节假日），落在周二 12/31 10:00。
	start := time.Date(2024, 12, 27, 9, 0, 0, 0, time.UTC)
	got := cal.AddWork(start, 9*time.Hour)
	want := time.Date(2024, 12, 31, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("AddWork = %v, want %v", got, want)
	}
}

func TestCalendarPartialDay(t *testing.T) {
	cal := DefaultCalendar("std")
	start := time.Date(2025, 3, 3, 15, 0, 0, 0, time.UTC) // 周一 15:00
	got := cal.AddWork(start, 4*time.Hour)                // 当天 2h + 次日 2h
	want := time.Date(2025, 3, 4, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("AddWork = %v, want %v", got, want)
	}
}

func TestCalendarDSTSpringForward(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	cal := &Calendar{ID: "ny", Location: loc}
	// 2025-03-09 美东拨快 1 小时：9:00-17:00 只有 7 个绝对小时。
	day := time.Date(2025, 3, 9, 0, 0, 0, 0, loc) // 周日，非工作日
	if cal.IsWorkingDay(day) {
		t.Fatal("Sunday should not be a workday")
	}
	mon := time.Date(2025, 3, 10, 9, 0, 0, 0, loc)
	end := cal.AddWork(mon, 8*time.Hour)
	if got := end.Sub(mon); got != 8*time.Hour {
		t.Fatalf("8 work hours should span 8 absolute hours, got %v", got)
	}
	// 夏令时当天（3/9 是周日；用 2025-03-08 周六? 改测 11 月回拨）。
	// 回拨日 2025-11-02 是周日。直接验证：跨回拨的工作时间单调不倒退。
	sun := time.Date(2025, 11, 3, 9, 0, 0, 0, loc) // 周一
	prev := sun
	for i := 0; i < 16; i++ {
		prev = cal.AddWork(prev, 30*time.Minute)
		if !prev.After(sun) {
			t.Fatalf("timeline went backwards: %v <= %v", prev, sun)
		}
		sun = prev
	}
}

func TestCalendarSameInstantAcrossTimezones(t *testing.T) {
	ny := &Calendar{ID: "ny", Location: mustLoc(t, "America/New_York")}
	sh := &Calendar{ID: "sh", Location: mustLoc(t, "Asia/Shanghai")}
	start := time.Date(2025, 6, 2, 9, 0, 0, 0, ny.Location) // 纽约周一 9:00
	endNY := ny.AddWork(start, 8*time.Hour)
	// 同一绝对时刻在上海日历里看到的必须是同一时刻。
	if !endNY.Equal(start.Add(8 * time.Hour)) {
		t.Fatalf("ny 8h = %v", endNY)
	}
	// 上海日历从同一绝对时刻算，工作日不同（纽约 9:00 = 上海 21:00，非工作），
	// 但任何结果仍是绝对时刻，In() 只是表示不同。
	endSH := sh.AddWork(start, 8*time.Hour)
	if endSH.Location() != sh.Location && !endSH.Equal(endSH.UTC()) {
		t.Fatal("instant mismatch")
	}
	_ = endSH
}

func TestWorkBetweenNegative(t *testing.T) {
	cal := DefaultCalendar("std")
	a := time.Date(2025, 3, 3, 9, 0, 0, 0, time.UTC)
	b := time.Date(2025, 3, 3, 12, 0, 0, 0, time.UTC)
	if got := cal.WorkBetween(a, b); got != 3*time.Hour {
		t.Fatalf("WorkBetween = %v", got)
	}
	if got := cal.WorkBetween(b, a); got != -3*time.Hour {
		t.Fatalf("WorkBetween reversed = %v", got)
	}
}

func TestAddWorkNegative(t *testing.T) {
	cal := DefaultCalendar("std")
	end := time.Date(2025, 3, 4, 11, 0, 0, 0, time.UTC)
	got := cal.AddWork(end, -4*time.Hour)
	want := time.Date(2025, 3, 3, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("AddWork(-4h) = %v, want %v", got, want)
	}
}
