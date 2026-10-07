package gantt

import (
	"errors"
	"testing"
	"time"
)

var monday = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

func stdPlan() *Plan {
	p := NewPlan("t", monday)
	cal := NewCalendar("std", time.UTC)
	p.Calendars["std"] = cal
	p.DefaultCal = "std"
	return p
}

func at(day, hour int) time.Time {
	return time.Date(2026, 1, day, hour, 0, 0, 0, time.UTC)
}

// 依赖类型：SS/FF/SF 与延迟。
func TestDepTypesAndLag(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 16 * time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour}
	// SS + 8h 延迟：B 在 A 开始后 8 个工作小时开始
	p.Dependencies = []Dependency{{From: "A", To: "B", Type: DepSS, Lag: 8 * time.Hour}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	// A 周一 9:00 开始；+8h 工作 = 周二 9:00
	if got := res.Tasks["B"].Start; !got.Equal(at(6, 9)) {
		t.Errorf("SS+lag: B start %s", got)
	}

	// FF：B 完成不早于 A 完成
	p2 := stdPlan()
	p2.Tasks["A"] = &Task{ID: "A", Duration: 24 * time.Hour}
	p2.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour}
	p2.Dependencies = []Dependency{{From: "A", To: "B", Type: DepFF}}
	res2, err := Schedule(p2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Tasks["B"].Finish.Before(res2.Tasks["A"].Finish) {
		t.Errorf("FF violated: B finish %s < A finish %s",
			res2.Tasks["B"].Finish, res2.Tasks["A"].Finish)
	}

	// SF：B 完成不早于 A 开始
	p3 := stdPlan()
	p3.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	p3.Tasks["B"] = &Task{ID: "B", Duration: 4 * time.Hour}
	p3.Dependencies = []Dependency{{From: "A", To: "B", Type: DepSF}}
	res3, err := Schedule(p3)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Tasks["B"].Finish.Before(res3.Tasks["A"].Start) {
		t.Errorf("SF violated")
	}
}

// MSO 固定开始日期约束。
func TestConstraintMSO(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour,
		Constraint: Constraint{Type: ConstraintMSO, Date: at(7, 0)}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Tasks["A"].Start; !got.Equal(at(7, 9)) {
		t.Errorf("MSO: start %s want %s", got, at(7, 9))
	}
}

// MFO 死线产生负浮动时必须如实标出（不得归零）。
func TestConstraintMFONegativeFloat(t *testing.T) {
	p := stdPlan()
	// 工期 5 天，死线在第 2 天结束 -> 负浮动
	p.Tasks["A"] = &Task{ID: "A", Duration: 40 * time.Hour,
		Constraint: Constraint{Type: ConstraintMFO, Date: at(6, 17)}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	a := res.Tasks["A"]
	if a.TotalFloat >= 0 {
		t.Errorf("expected negative float, got %v", a.TotalFloat)
	}
	if !a.Critical {
		t.Error("negative-float task must be critical")
	}
}

// ALAP：尽晚任务被推到不拖延项目的最晚时刻。
func TestConstraintALAP(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour}
	p.Tasks["C"] = &Task{ID: "C", Duration: 8 * time.Hour,
		Constraint: Constraint{Type: ConstraintALAP}}
	p.Dependencies = []Dependency{{From: "A", To: "B", Type: DepFS}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	// 项目 2 天（A、B 链），C 无依赖、ALAP -> 应排在第二天
	c := res.Tasks["C"]
	if !c.Start.Equal(at(6, 9)) {
		t.Errorf("ALAP: C start %s want %s", c.Start, at(6, 9))
	}
	if c.TotalFloat != 0 {
		t.Errorf("ALAP task float should be 0, got %v", c.TotalFloat)
	}
}

// 资源调平：同一资源上两个任务不能并行，低优先级者被推迟且原因可解释。
func TestResourceLeveling(t *testing.T) {
	p := stdPlan()
	p.Resources["R1"] = &Resource{ID: "R1", Capacity: 1}
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour, Priority: 1,
		Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour, Priority: 2,
		Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Tasks["A"], res.Tasks["B"]
	// A 优先级高，先占资源；B 推迟到 A 完成后
	if !a.Start.Equal(at(5, 9)) {
		t.Errorf("A start %s", a.Start)
	}
	if !b.Start.Equal(at(6, 9)) {
		t.Errorf("B should be leveled to next day, got %s", b.Start)
	}
	// 推迟原因必须可解释
	found := false
	for _, r := range b.DelayReasons {
		if r.Kind == "resource" && r.Resource == "R1" {
			found = true
		}
	}
	if !found {
		t.Errorf("B missing resource delay reason: %+v", b.DelayReasons)
	}
	// 无资源冲突：时间上不得重叠
	if a.Finish.After(b.Start) && b.Finish.After(a.Start) {
		t.Error("leveled tasks still overlap on R1")
	}
}

// 产能：容量 2 的资源允许两个 units=1 的任务并行。
func TestResourceCapacity(t *testing.T) {
	p := stdPlan()
	p.Resources["R1"] = &Resource{ID: "R1", Capacity: 2}
	for _, id := range []string{"A", "B"} {
		p.Tasks[id] = &Task{ID: id, Duration: 8 * time.Hour,
			Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Tasks["A"].Start.Equal(res.Tasks["B"].Start) {
		t.Errorf("capacity 2 should allow parallel: %s vs %s",
			res.Tasks["A"].Start, res.Tasks["B"].Start)
	}
}

// 总浮动：支路任务有正浮动，主链为零。
func TestTotalFloat(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 16 * time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour}
	p.Tasks["C"] = &Task{ID: "C", Duration: 8 * time.Hour}
	p.Dependencies = []Dependency{
		{From: "A", To: "C", Type: DepFS},
		{From: "B", To: "C", Type: DepFS},
	}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tasks["A"].TotalFloat != 0 || res.Tasks["C"].TotalFloat != 0 {
		t.Errorf("main chain float should be 0: A=%v C=%v",
			res.Tasks["A"].TotalFloat, res.Tasks["C"].TotalFloat)
	}
	if res.Tasks["B"].TotalFloat != 8*time.Hour {
		t.Errorf("B float got %v want 8h", res.Tasks["B"].TotalFloat)
	}
	// 零浮动集合与关键路径一致
	zero := map[string]bool{}
	for id, st := range res.Tasks {
		if st.TotalFloat == 0 {
			zero[id] = true
		}
	}
	for _, id := range res.CriticalPath {
		if !zero[id] {
			t.Errorf("critical path task %s not zero-float", id)
		}
	}
}

// 确定性：同一输入重复计算结果逐项一致。
func TestDeterministic(t *testing.T) {
	build := func() *Plan {
		p := stdPlan()
		p.Resources["R1"] = &Resource{ID: "R1", Capacity: 1}
		p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour, Priority: 2,
			Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
		p.Tasks["B"] = &Task{ID: "B", Duration: 12 * time.Hour, Priority: 1,
			Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
		p.Tasks["C"] = &Task{ID: "C", Duration: 4 * time.Hour}
		p.Dependencies = []Dependency{{From: "B", To: "C", Type: DepFS, Lag: time.Hour}}
		return p
	}
	r1, err := Schedule(build())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		r2, err := Schedule(build())
		if err != nil {
			t.Fatal(err)
		}
		for id, st1 := range r1.Tasks {
			st2 := r2.Tasks[id]
			if !st1.Start.Equal(st2.Start) || !st1.Finish.Equal(st2.Finish) ||
				st1.TotalFloat != st2.TotalFloat {
				t.Fatalf("run %d diverged on %s", i, id)
			}
		}
	}
}

// 环路错误可区分：errors.Is(err, ErrCycle)。
func TestCycleDistinctError(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: time.Hour}
	p.Tasks["C"] = &Task{ID: "C", Duration: time.Hour}
	p.Dependencies = []Dependency{
		{From: "A", To: "B", Type: DepFS},
		{From: "B", To: "C", Type: DepFS},
		{From: "C", To: "A", Type: DepFS},
	}
	_, err := Schedule(p)
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
	var ce *CycleError
	if !errors.As(err, &ce) || len(ce.Path) < 2 {
		t.Fatalf("cycle path missing: %+v", err)
	}
}

// 未知任务引用可区分。
func TestUnknownTaskError(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: time.Hour}
	p.Dependencies = []Dependency{{From: "A", To: "ghost", Type: DepFS}}
	_, err := Schedule(p)
	if !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}
}
