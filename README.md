# gantt-scheduling-engine-go

项目排期与甘特视图引擎：把计划从输入定义一路算到可视化时间轴。
结果**可复现**、**可增量重算**、**可并发读取**。

```
gantt/
  types.go     核心类型：Task / Dependency / Constraint / Resource / Plan / ScheduleResult
  calendar.go  工作日历：周末、节假日、每日工时、时区与夏令时
  schedule.go  排期核心：拓扑排序、CPM 前推/回推、约束、资源调平、关键路径
  engine.go    引擎：版本化（MVCC）、编辑收敛、增量重算、并发读写
  baseline.go  基线快照、实际进度录入、偏移分析
  view.go      甘特视图：时间窗口 / 资源过滤、分页
```

## 快速开始

```go
loc, _ := time.LoadLocation("America/New_York")
cal := gantt.NewCalendar("std", loc)      // 周一至周五 9:00-17:00
cal.AddHoliday(time.Date(2026, 7, 3, 0, 0, 0, 0, loc))

p := gantt.NewPlan("demo", time.Date(2026, 1, 5, 0, 0, 0, 0, loc))
p.Calendars["std"] = cal
p.DefaultCal = "std"
p.Tasks["A"] = &gantt.Task{ID: "A", Duration: 16 * time.Hour}
p.Tasks["B"] = &gantt.Task{ID: "B", Duration: 8 * time.Hour}
p.Dependencies = []gantt.Dependency{{From: "A", To: "B", Type: gantt.DepFS}}

eng, _ := gantt.NewEngine(p)              // 首次全量排算
v := eng.Snapshot()                       // 任意时刻读取完整版本
bars := gantt.View(v, gantt.ViewQuery{Limit: 50})
```

## 排期语义

排算分四个确定性阶段（同一输入必然得到同一输出）：

1. **前推（CPM forward）**：从 `ProjectStart` 起，按依赖与约束下界计算每个任务的最早开始/完成。
   工期按任务日历的*工作时间*折算（8h 工期在 9-17 日历上恰好 1 个工作日）。
2. **回推（CPM backward）**：从项目完工时刻回推最晚开始/完成，得到**总浮动**。
   浮动以任务日历的工作时间度量；**负浮动如实保留**（`TotalFloat < 0`），
   表示按当前死线（MFO）无法按期完成，绝不悄悄归零。
3. **ALAP 处理**：尽晚任务被固定到其最晚开始时刻后重算一轮。
4. **资源调平（serial SGS）**：按（优先级 → 拓扑序 → ID）的确定顺序逐个放置任务，
   同一资源同一时刻的占用总量不得超过 `Capacity`。被推迟的任务在
   `ScheduledTask.DelayReasons` 中记录原因（`dependency` 或 `resource`）。
   推迟**只**由依赖关系与资源冲突引起。

**关键路径** = 总浮动为零且首尾相连的任务链（`ScheduleResult.CriticalPath`）。
增量重算与全量重算使用同一 `Schedule` 函数，零浮动集合逐项一致。

### 依赖类型与延迟

| 类型 | 语义 |
|------|------|
| `DepFS` | 前置完成后后继才能开始 |
| `DepSS` | 前置开始后后继才能开始 |
| `DepFF` | 前置完成后后继才能完成 |
| `DepSF` | 前置开始后后继才能完成 |

`Lag` 为依赖延迟，按**前置任务日历**的工作时间计（可为负，表示提前量）。

### 时间约束

| 约束 | 语义 |
|------|------|
| `ConstraintNone` / `ConstraintASAP` | 尽早开始（默认） |
| `ConstraintALAP` | 尽晚开始（不拖延项目的前提下排到最后） |
| `ConstraintMSO` | 必须不早于固定日期开始（下界） |
| `ConstraintMFO` | 必须于固定日期前完成（死线，可能产生负浮动） |

### 失败原因（可区分）

- `ErrCycle`（携带 `*CycleError.Path` 环路任务列表）：依赖图有环，拒绝排算，不死循环。
- `ErrUnknownTask` / `ErrUnknownCal` / `ErrUnknownRes`：引用不存在。
- `ErrInfeasible`：定义自相矛盾（负工期、资源占用超产能等）。

## 工作日历与时区

- `Calendar` 定义每周工作日（`WorkDays`）、节假日（`Holidays`）、
  每日工作时段（`Intervals`，为空表示 24h）与时区（`Location`）。
- 所有排期输出都是**绝对时刻**（`time.Time`）：同一任务在不同时区下
  看到的开始与结束是同一瞬时（`t.In(loc)` 只是表示不同）。
- 夏令时切换日，工作窗口按本地时间定义、绝对时长自然伸缩，
  窗口严格单调——时间轴不倒退、不跳变。

## 编辑与增量重算

```go
eng.Apply(
    gantt.Edit{Seq: 1, Op: gantt.OpUpsertTask, Task: &gantt.Task{ID: "C", Duration: 8*time.Hour}},
    gantt.Edit{Seq: 2, Op: gantt.OpAddDep, Dep: &gantt.Dependency{From: "B", To: "C"}},
)
```

- 每个实体（任务/依赖/资源/日历）按 `Seq` **最后写入者胜出**：
  编辑乱序到达或重复提交，最终状态收敛。
- 任一批次排算失败（如引入环路）**整体不生效**，引擎保持上一已提交版本。
- 增量重算与 `Recompute()` 全量重算逐项一致（同一确定性算法）。

## 基线与实际进度

```go
base := gantt.NewBaseline(v.Result)                 // 基线快照
eng.Apply(gantt.Edit{Seq: 9, Op: gantt.OpPinTask, TaskID: "A", Pin: actualStart})
vars := base.Compare(eng.Snapshot().Result)         // 偏移，偏差最大者在前
```

已开始/已完成的任务用 `OpPinTask`（或 `PinStarted` + `Actuals`）钉住，
钉住的任务在后续重算中**不会被重排**；`OpUnpinTask` 解除。

## 并发与视图

- `Engine` 使用读写锁 + 写时复制：读取方任何时刻看到的都是某个
  **已提交的完整版本**，不会看到字段级混合状态；已发布版本不可变。
- `View(v, ViewQuery{From, To, ResourceID, Offset, Limit})` 按时间窗口 /
  资源过滤并分页，输出按（开始时刻，任务 ID）排序，确定且可复现。

## 测试

```sh
go test ./... -race
```

覆盖：环路拒绝、跨时区与夏令时、资源冲突调平、增量与全量一致、
钉住任务、并发读写、基线偏移、视图分页。
