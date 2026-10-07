package gantt

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func engineForTest(t *testing.T) *Engine {
	t.Helper()
	p := stdPlan()
	p.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	p.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour}
	p.Dependencies = []Dependency{{From: "A", To: "B", Type: DepFS}}
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sameResult(a, b *ScheduleResult) error {
	if len(a.Tasks) != len(b.Tasks) {
		return fmt.Errorf("task count %d vs %d", len(a.Tasks), len(b.Tasks))
	}
	for id, x := range a.Tasks {
		y, ok := b.Tasks[id]
		if !ok {
			return fmt.Errorf("missing task %s", id)
		}
		if !x.Start.Equal(y.Start) || !x.Finish.Equal(y.Finish) ||
			x.TotalFloat != y.TotalFloat || x.Critical != y.Critical {
			return fmt.Errorf("task %s differs: %+v vs %+v", id, x, y)
		}
	}
	if len(a.CriticalPath) != len(b.CriticalPath) {
		return fmt.Errorf("critical path differs")
	}
	for i := range a.CriticalPath {
		if a.CriticalPath[i] != b.CriticalPath[i] {
			return fmt.Errorf("critical path differs at %d", i)
		}
	}
	return nil
}

// 增量重算与从头全量重算逐项一致。
func TestIncrementalMatchesFull(t *testing.T) {
	e := engineForTest(t)
	edits := []Edit{
		{Seq: 1, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: 16 * time.Hour}},
		{Seq: 2, Op: OpAddDep, Dep: &Dependency{From: "B", To: "C", Type: DepFS}},
		{Seq: 3, Op: OpUpsertResource, Res: &Resource{ID: "R1", Capacity: 1}},
		{Seq: 4, Op: OpUpsertTask, Task: &Task{ID: "A", Duration: 8 * time.Hour,
			Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}},
		{Seq: 5, Op: OpUpsertTask, Task: &Task{ID: "D", Duration: 8 * time.Hour, Priority: 1,
			Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}},
	}
	v1, err := e.Apply(edits...)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := e.Recompute()
	if err != nil {
		t.Fatal(err)
	}
	if err := sameResult(v1.Result, v2.Result); err != nil {
		t.Fatalf("incremental != full: %v", err)
	}
	// 零浮动任务集合一致
	z1 := map[string]bool{}
	for id, st := range v1.Result.Tasks {
		if st.TotalFloat == 0 {
			z1[id] = true
		}
	}
	for id, st := range v2.Result.Tasks {
		if (st.TotalFloat == 0) != z1[id] {
			t.Fatalf("zero-float set differs on %s", id)
		}
	}
}

// 乱序与重复提交：最终状态收敛。
func TestEditsConverge(t *testing.T) {
	e := engineForTest(t)
	// 同一实体两次更新，Seq 大者胜出；乱序到达
	if _, err := e.Apply(
		Edit{Seq: 10, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: 24 * time.Hour}},
		Edit{Seq: 5, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: 8 * time.Hour}},
	); err != nil {
		t.Fatal(err)
	}
	v := e.Snapshot()
	if v.Plan.Tasks["C"].Duration != 24*time.Hour {
		t.Errorf("last-writer-wins failed: %v", v.Plan.Tasks["C"].Duration)
	}
	// 重复提交同一 Seq：幂等
	v1, err := e.Apply(Edit{Seq: 10, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: 99 * time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Plan.Tasks["C"].Duration != 24*time.Hour {
		t.Error("duplicate seq should be ignored")
	}
	if v1.Seq != v.Seq {
		t.Error("no-op edit should not bump version")
	}
}

// 失败编辑不留半成品：状态保持上一个已提交版本。
func TestFailedApplyAtomic(t *testing.T) {
	e := engineForTest(t)
	before := e.Snapshot()
	// 制造环路：B->A
	_, err := e.Apply(Edit{Seq: 1, Op: OpAddDep, Dep: &Dependency{From: "B", To: "A", Type: DepFS}})
	if err == nil {
		t.Fatal("expected cycle error")
	}
	after := e.Snapshot()
	if after.Seq != before.Seq {
		t.Error("failed apply bumped version")
	}
	if len(after.Plan.Dependencies) != 1 {
		t.Error("failed apply left partial state")
	}
	// 后续合法编辑仍可正常提交
	if _, err := e.Apply(Edit{Seq: 2, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: time.Hour}}); err != nil {
		t.Fatalf("engine should recover after failed apply: %v", err)
	}
}

// 钉住任务：重算不得重排。
func TestPinnedTaskNotRescheduled(t *testing.T) {
	e := engineForTest(t)
	pinAt := at(8, 9) // 周四 9:00
	if _, err := e.Apply(Edit{Seq: 1, Op: OpPinTask, TaskID: "B", Pin: pinAt}); err != nil {
		t.Fatal(err)
	}
	// 后续编辑触发重算，B 不得移动
	if _, err := e.Apply(Edit{Seq: 2, Op: OpUpsertTask, Task: &Task{ID: "C", Duration: 4 * time.Hour}}); err != nil {
		t.Fatal(err)
	}
	v := e.Snapshot()
	if got := v.Result.Tasks["B"].Start; !got.Equal(pinAt) {
		t.Errorf("pinned task moved to %s", got)
	}
	// 取消钉住后可重新排程
	if _, err := e.Apply(Edit{Seq: 3, Op: OpUnpinTask, TaskID: "B"}); err != nil {
		t.Fatal(err)
	}
	v2 := e.Snapshot()
	if got := v2.Result.Tasks["B"].Start; got.Equal(pinAt) {
		t.Errorf("unpinned task should be rescheduled, still %s", got)
	}
}

// 并发读写：读取方任何时刻看到完整版本。
func TestConcurrentReadWrite(t *testing.T) {
	e := engineForTest(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 写方：持续追加任务
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("W%d", i)
			if _, err := e.Apply(Edit{Seq: uint64(100 + i), Op: OpUpsertTask,
				Task: &Task{ID: id, Duration: time.Hour}}); err != nil {
				t.Errorf("apply: %v", err)
				return
			}
		}
		close(stop)
	}()
	// 读方：持续读取并校验版本完整性
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := e.Snapshot()
				// 完整性校验：每个结果任务都能在计划中找到，且字段一致
				for id, st := range v.Result.Tasks {
					if _, ok := v.Plan.Tasks[id]; !ok {
						t.Errorf("result task %s missing in plan (mixed state)", id)
						return
					}
					if st.Finish.Before(st.Start) {
						t.Errorf("task %s finish before start (mixed state)", id)
						return
					}
				}
				if len(v.Result.Tasks) != len(v.Plan.Tasks) {
					t.Errorf("result/plan task count mismatch: %d vs %d",
						len(v.Result.Tasks), len(v.Plan.Tasks))
					return
				}
			}
		}()
	}
	wg.Wait()
}

// 基线偏移与偏差最大任务。
func TestBaselineVariance(t *testing.T) {
	e := engineForTest(t)
	base := NewBaseline(e.Snapshot().Result)
	// 把 A 工期加倍 -> 全链后移一天
	if _, err := e.Apply(Edit{Seq: 1, Op: OpUpsertTask, Task: &Task{ID: "A", Duration: 16 * time.Hour}}); err != nil {
		t.Fatal(err)
	}
	vars := base.Compare(e.Snapshot().Result)
	if len(vars) != 2 {
		t.Fatalf("expected 2 variances, got %d", len(vars))
	}
	// A 完成从周一 17:00 推到周二 17:00 = 绝对 24h
	if vars[0].FinishVar != 24*time.Hour {
		t.Errorf("max variance should be 24h, got %v (%s)", vars[0].FinishVar, vars[0].TaskID)
	}
}

// 视图分页：时间窗口、资源过滤、确定性。
func TestViewPagination(t *testing.T) {
	p := stdPlan()
	p.Resources["R1"] = &Resource{ID: "R1", Capacity: 10}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("T%02d", i)
		p.Tasks[id] = &Task{ID: id, Name: "task-" + id, Duration: 8 * time.Hour,
			Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	}
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	v := e.Snapshot()
	// 全量
	all := View(v, ViewQuery{})
	if len(all) != 10 {
		t.Fatalf("expected 10 bars, got %d", len(all))
	}
	// 分页拼接 == 全量
	var paged []Bar
	for off := 0; off < 10; off += 3 {
		paged = append(paged, View(v, ViewQuery{Offset: off, Limit: 3})...)
	}
	if len(paged) != len(all) {
		t.Fatalf("paged %d != all %d", len(paged), len(all))
	}
	for i := range all {
		if paged[i].TaskID != all[i].TaskID {
			t.Fatalf("page %d mismatch", i)
		}
	}
	// 资源过滤
	byRes := View(v, ViewQuery{ResourceID: "R1"})
	if len(byRes) != 10 {
		t.Errorf("resource filter got %d", len(byRes))
	}
	none := View(v, ViewQuery{ResourceID: "ghost"})
	if len(none) != 0 {
		t.Errorf("unknown resource should yield 0 bars")
	}
	// 时间窗口
	win := View(v, ViewQuery{From: at(6, 0), To: at(7, 0)})
	for _, b := range win {
		if b.Finish.Before(at(6, 0)) || b.Start.After(at(7, 0)) {
			t.Errorf("bar outside window: %+v", b)
		}
	}
	// 可复现
	again := View(v, ViewQuery{Limit: 5})
	first := View(v, ViewQuery{Limit: 5})
	for i := range again {
		if again[i] != first[i] {
			t.Fatal("view not reproducible")
		}
	}
}
