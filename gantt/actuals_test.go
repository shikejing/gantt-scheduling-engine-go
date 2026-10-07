package gantt

import (
	"testing"
	"time"
)

// 实际进度录入：已开始的任务被钉住，后续重算不重排；
// 相对基线的偏移可解释。
func TestActualsPinAndVariance(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour}
	p.Dependencies = []Dependency{{From: "A", To: "B", Type: DepFS}}
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	base := NewBaseline(e.Snapshot().Result)

	// A 实际周三 9:00 才开始（比计划晚 2 天）
	actualStart := at(7, 9)
	v, err := e.Apply(Edit{Seq: 1, Op: OpPinTask, TaskID: "A", Pin: actualStart})
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Result.Tasks["A"].Start; !got.Equal(actualStart) {
		t.Fatalf("pinned A should start at actuals: %s", got)
	}
	// B 随依赖后移
	if got := v.Result.Tasks["B"].Start; !got.Equal(at(8, 9)) {
		t.Errorf("B should follow A: %s", got)
	}
	// 相对基线偏移
	vars := base.Compare(v.Result)
	if len(vars) == 0 || vars[0].FinishVar <= 0 {
		t.Errorf("expected positive variance: %+v", vars)
	}
	// 再重算，A 仍钉住
	v2, err := e.Apply(Edit{Seq: 2, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	if got := v2.Result.Tasks["A"].Start; !got.Equal(actualStart) {
		t.Errorf("pinned A moved after recompute: %s", got)
	}
}

// PinStarted 直接作用于计划对象。
func TestPinStartedHelper(t *testing.T) {
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	PinStarted(p, &Actuals{Started: map[string]time.Time{"A": at(6, 10)}})
	if !p.Tasks["A"].Pinned {
		t.Fatal("A should be pinned")
	}
	res, err := Schedule(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Tasks["A"].Start; !got.Equal(at(6, 10)) {
		t.Errorf("pinned start %s", got)
	}
}
