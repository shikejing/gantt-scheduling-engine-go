package scheduler

import (
	"errors"
	"testing"
	"time"
)

var testStart = time.Date(2025, 3, 3, 9, 0, 0, 0, time.UTC) // 周一

func baseInput() *Input {
	return &Input{
		Tasks:             map[string]*Task{},
		Resources:         map[string]*Resource{},
		Calendars:         map[string]*Calendar{"std": DefaultCalendar("std")},
		DefaultCalendarID: "std",
		ProjectStart:      testStart,
	}
}

func TestSimpleChainAndLag(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	in.Tasks["B"] = &Task{ID: "B", Duration: 4 * time.Hour, Predecessors: []Dependency{
		{PredecessorID: "A", Type: FinishToStart, Lag: 2 * time.Hour},
	}}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Tasks["A"], res.Tasks["B"]
	// A: 周一 9:00 + 8h = 周一 17:00
	wantAEnd := time.Date(2025, 3, 3, 17, 0, 0, 0, time.UTC)
	if !a.Finish.Equal(wantAEnd) {
		t.Fatalf("A.Finish = %v, want %v", a.Finish, wantAEnd)
	}
	// B: A 完成 + 2h lag = 周二 11:00 开始，+4h = 周二 15:00
	wantBStart := time.Date(2025, 3, 4, 11, 0, 0, 0, time.UTC)
	if !b.Start.Equal(wantBStart) {
		t.Fatalf("B.Start = %v, want %v", b.Start, wantBStart)
	}
	if !b.Critical || !a.Critical {
		t.Fatal("both tasks should be critical in a simple chain")
	}
	if len(res.CriticalPath) != 2 {
		t.Fatalf("CriticalPath = %v", res.CriticalPath)
	}
}

func TestCycleRejected(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: time.Hour, Predecessors: []Dependency{{PredecessorID: "C"}}}
	in.Tasks["B"] = &Task{ID: "B", Duration: time.Hour, Predecessors: []Dependency{{PredecessorID: "A"}}}
	in.Tasks["C"] = &Task{ID: "C", Duration: time.Hour, Predecessors: []Dependency{{PredecessorID: "B"}}}
	_, err := Schedule(in)
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected CycleError, got %v", err)
	}
	if len(cyc.Path) < 2 || cyc.Path[0] != cyc.Path[len(cyc.Path)-1] {
		t.Fatalf("cycle path malformed: %v", cyc.Path)
	}
}

func TestSelfLoopRejected(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: time.Hour, Predecessors: []Dependency{{PredecessorID: "A"}}}
	var cyc *CycleError
	if _, err := Schedule(in); !errors.As(err, &cyc) {
		t.Fatalf("expected CycleError, got %v", cyc)
	}
}

func TestResourceLeveling(t *testing.T) {
	in := baseInput()
	in.Resources["R1"] = &Resource{ID: "R1", Capacity: 1}
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour, Priority: 1, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	in.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour, Priority: 2, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Tasks["A"], res.Tasks["B"]
	// A 优先级高先占资源：周一 9-17；B 被推到周二 9-17。
	if !a.Start.Equal(testStart) {
		t.Fatalf("A.Start = %v", a.Start)
	}
	wantB := time.Date(2025, 3, 4, 9, 0, 0, 0, time.UTC)
	if !b.Start.Equal(wantB) {
		t.Fatalf("B.Start = %v, want %v", b.Start, wantB)
	}
	if !b.Delayed || len(b.DelayReasons) == 0 {
		t.Fatal("B should be marked delayed with reasons")
	}
	found := false
	for _, d := range b.DelayReasons {
		if d.Kind == DelayByResource && d.ResourceID == "R1" && d.TaskID == "A" {
			found = true
		}
	}
	if !found {
		t.Fatalf("B delay reasons should cite resource R1 blocked by A: %+v", b.DelayReasons)
	}
	if a.Delayed {
		t.Fatal("A should not be delayed")
	}
}

func TestSharedCapacityAllowsParallel(t *testing.T) {
	in := baseInput()
	in.Resources["R1"] = &Resource{ID: "R1", Capacity: 2}
	for _, id := range []string{"A", "B"} {
		in.Tasks[id] = &Task{ID: id, Duration: 8 * time.Hour, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Tasks["A"].Start.Equal(testStart) || !res.Tasks["B"].Start.Equal(testStart) {
		t.Fatalf("capacity 2 should allow both at 9:00: A=%v B=%v", res.Tasks["A"].Start, res.Tasks["B"].Start)
	}
}

func TestDeterminism(t *testing.T) {
	build := func() *Result {
		in := baseInput()
		in.Resources["R1"] = &Resource{ID: "R1", Capacity: 1}
		in.Tasks["A"] = &Task{ID: "A", Duration: 6 * time.Hour, Priority: 2, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
		in.Tasks["B"] = &Task{ID: "B", Duration: 6 * time.Hour, Priority: 1, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
		in.Tasks["C"] = &Task{ID: "C", Duration: 2 * time.Hour, Predecessors: []Dependency{{PredecessorID: "A"}, {PredecessorID: "B"}}}
		r, err := Schedule(in)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1, r2 := build(), build()
	for id, tr1 := range r1.Tasks {
		tr2 := r2.Tasks[id]
		if !tr1.Start.Equal(tr2.Start) || !tr1.Finish.Equal(tr2.Finish) || tr1.TotalFloat != tr2.TotalFloat {
			t.Fatalf("nondeterministic result for %s", id)
		}
	}
}

func TestNegativeFloatSurfaced(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	in.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour,
		Predecessors: []Dependency{{PredecessorID: "A"}},
		Constraint:   Constraint{Type: ConstraintFinishNoLaterThan, Date: time.Date(2025, 3, 3, 17, 0, 0, 0, time.UTC)},
	}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	b := res.Tasks["B"]
	if !b.NegativeFloat {
		t.Fatalf("B should have negative float, got %v", b.TotalFloat)
	}
	if b.TotalFloat >= 0 {
		t.Fatalf("negative float must not be clamped to zero, got %v", b.TotalFloat)
	}
}

func TestMustStartOnAndSNET(t *testing.T) {
	in := baseInput()
	fixed := time.Date(2025, 3, 5, 9, 0, 0, 0, time.UTC) // 周三
	in.Tasks["A"] = &Task{ID: "A", Duration: 4 * time.Hour, Constraint: Constraint{Type: ConstraintMustStartOn, Date: fixed}}
	in.Tasks["B"] = &Task{ID: "B", Duration: 4 * time.Hour, Constraint: Constraint{Type: ConstraintStartNoEarlierThan, Date: fixed}}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Tasks["A"].Start.Equal(fixed) {
		t.Fatalf("A.Start = %v, want %v", res.Tasks["A"].Start, fixed)
	}
	if !res.Tasks["B"].Start.Equal(fixed) {
		t.Fatalf("B.Start = %v, want %v", res.Tasks["B"].Start, fixed)
	}
	if !res.Tasks["A"].Critical {
		t.Fatal("MustStartOn task should be critical (zero float)")
	}
}

func TestDependencyTypes(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	in.Tasks["SS"] = &Task{ID: "SS", Duration: 4 * time.Hour, Predecessors: []Dependency{
		{PredecessorID: "A", Type: StartToStart, Lag: 2 * time.Hour}}}
	in.Tasks["FF"] = &Task{ID: "FF", Duration: 4 * time.Hour, Predecessors: []Dependency{
		{PredecessorID: "A", Type: FinishToFinish}}}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	// SS: 周一 11:00 开始
	if got := res.Tasks["SS"].Start; !got.Equal(time.Date(2025, 3, 3, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("SS.Start = %v", got)
	}
	// FF: 完成不早于 A 完成（周一 17:00），4h 回推 -> 周一 13:00 开始
	if got := res.Tasks["FF"].Start; !got.Equal(time.Date(2025, 3, 3, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("FF.Start = %v", got)
	}
}
