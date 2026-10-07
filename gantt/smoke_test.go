package gantt

import (
	"testing"
	"time"
)

func TestSmokeBasic(t *testing.T) {
	loc := time.UTC
	cal := NewCalendar("std", loc)
	p := NewPlan("p1", time.Date(2026, 1, 5, 0, 0, 0, 0, loc)) // 周一
	p.Calendars["std"] = cal
	p.DefaultCal = "std"
	p.Tasks["A"] = &Task{ID: "A", Name: "A", Duration: 16 * time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Name: "B", Duration: 8 * time.Hour}
	p.Dependencies = []Dependency{{From: "A", To: "B", Type: DepFS}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Tasks["A"], res.Tasks["B"]
	t.Logf("A: %s -> %s", a.Start, a.Finish)
	t.Logf("B: %s -> %s", b.Start, b.Finish)
	// A: 周一起 16h 工作 = 2 个工作日 -> 周一 9:00 到周二 17:00
	wantAS := time.Date(2026, 1, 5, 9, 0, 0, 0, loc)
	wantAF := time.Date(2026, 1, 6, 17, 0, 0, 0, loc)
	if !a.Start.Equal(wantAS) || !a.Finish.Equal(wantAF) {
		t.Errorf("A got %s -> %s", a.Start, a.Finish)
	}
	wantBS := time.Date(2026, 1, 7, 9, 0, 0, 0, loc)
	if !b.Start.Equal(wantBS) {
		t.Errorf("B start got %s want %s", b.Start, wantBS)
	}
	if !a.Critical || !b.Critical {
		t.Errorf("both should be critical: A=%v B=%v floats %v %v", a.Critical, b.Critical, a.TotalFloat, b.TotalFloat)
	}
	if len(res.CriticalPath) != 2 {
		t.Errorf("critical path %v", res.CriticalPath)
	}
}

func TestSmokeCycle(t *testing.T) {
	p := NewPlan("p2", time.Now())
	p.Tasks["A"] = &Task{ID: "A", Duration: time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: time.Hour}
	p.Dependencies = []Dependency{
		{From: "A", To: "B", Type: DepFS},
		{From: "B", To: "A", Type: DepFS},
	}
	_, err := Schedule(p)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	t.Logf("cycle path: %v", ce.Path)
}
