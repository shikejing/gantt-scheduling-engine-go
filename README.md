# gantt-scheduling-engine-go

项目排期与甘特视图引擎：从任务 / 依赖 / 资源 / 日历的输入定义，一路计算到
可复现、可增量重算、可并发读取的时间轴视图。

```go
import "github.com/shikejing/gantt-scheduling-engine-go/scheduler"
```

## 快速开始

```go
in := &scheduler.Input{
    Tasks: map[string]*scheduler.Task{
        "design": {ID: "design", Duration: 16 * time.Hour, Priority: 1},
        "build":  {ID: "build", Duration: 24 * time.Hour,
                   Predecessors: []scheduler.Dependency{{PredecessorID: "design"}}},
    },
    Resources:         map[string]*scheduler.Resource{},
    Calendars:         map[string]*scheduler.Calendar{"std": scheduler.DefaultCalendar("std")},
    DefaultCalendarID: "std",
    ProjectStart:      time.Date(2025, 3, 3, 9, 0, 0, 0, time.UTC),
}
plan, err := scheduler.NewPlan(in)   // 首次全量排期
v := plan.Snapshot()                  // 当前已提交版本（不可变，可并发读）
page := v.View(scheduler.ViewQuery{Limit: 20})
```

## 排期语义

排期分四个阶段，均为输入的纯函数，同一输入必然得到同一结果：

1. **环路检测**：依赖图做拓扑排序，发现环路返回 `*CycleError`
   （含环路上的任务序列），可用 `errors.As` 区分；不会死循环，
   也不会返回自相矛盾的结果。
2. **CPM 前向/后向传递**：计算每个任务的最早开始/完成（ES/EF）、
   尽晚开始/完成（LS/LF）与总浮动（`TotalFloat = WorkBetween(ES, LS)`，
   按任务有效日历的工作时间计）。浮动为负时 `NegativeFloat = true`，
   **不会被钳成零**；浮动为零即关键任务（`Critical = true`），
   `Result.CriticalPath` 给出一条按时间排序的关键路径。
3. **ALAP 调整**：`ConstraintALAP` 任务被钉到其尽晚开始，再做一次
   前向/后向传递使全网一致。
4. **资源调平**：串行调度生成方案（SGS）。每一步从"前置已全部排定"
   的任务中按 **(优先级升序, 最早开始升序, 任务 ID 升序)** 的全序选出
   下一个任务，排在满足依赖下界与资源产能的最早时刻。选择顺序是全序，
   因此结果确定可复现。被推迟的任务带 `Delayed = true` 与
   `DelayReasons`（`DelayByDependency` / `DelayByResource` /
   `DelayByConstraint`），能逐项说清被谁、因何推迟；推迟只来自依赖
   关系与资源冲突。

### 依赖类型与延迟

| 类型 | 常量 | 语义 |
|---|---|---|
| FS | `FinishToStart` | 前置完成后后续才能开始（默认） |
| SS | `StartToStart` | 前置开始后后续才能开始 |
| FF | `FinishToFinish` | 前置完成后后续才能完成 |
| SF | `StartToFinish` | 前置开始后后续才能完成 |

`Dependency.Lag` 为依赖延迟，按**后续任务有效日历**的工作时间计，可为负。

### 时间约束

| 约束 | 语义 |
|---|---|
| `ConstraintASAP` | 尽早开始（默认） |
| `ConstraintALAP` | 尽晚开始（不推迟项目的前提下尽量晚） |
| `ConstraintMustStartOn` | 固定日期开始（浮动归零，冲突时变负） |
| `ConstraintStartNoEarlierThan` | 不早于某日 |
| `ConstraintFinishNoLaterThan` | 不晚于某日完成（冲突时浮动变负并显式标记） |

## 工作日历与时区

`Calendar` 描述周末（`WorkDays`）、节假日（`Holidays`，`"2006-01-02"`
键）、每日工作时段（`Daily`，如 09:00-12:00 / 13:00-17:00）与时区
（`Location`）。

- 所有输入输出都是**绝对时刻**（`time.Time`）。每日时段按日历所在地
  本地时钟解释后换算为绝对区间：夏令时拨快当天 9:00-17:00 只有 7 个
  绝对小时，时间轴绝不倒退或跳变；同一任务在不同时区下看到的开始与
  结束是同一时刻。
- **有效日历 = 任务日历 ∩ 所占用资源的日历**。跨时区协作时，任务只能
  在各方共同的工作时段内推进（例如纽约 20:00-23:00 ∩ 上海 08:00-12:00）。
  交集在 ProjectStart 后 10 年内没有公共工作时刻时返回
  `*ValidationError`，不会死循环。

## 编辑、增量重算与并发

```go
v2, err := plan.ApplyEdit(scheduler.Edit{
    ID:   "client-edit-42",          // 幂等键：重复提交不产生效果
    Type: scheduler.EditUpsertTask,
    Task: &scheduler.Task{ID: "build", Duration: 30 * time.Hour},
})
```

- **原子性**：`ApplyEdit` 整体校验并计算成功后才发布新版本；失败或
  取消时计划保持上一个已提交版本，绝不留下算了一半的状态。
- **幂等与收敛**：编辑 ID 去重，重复提交是空操作；内容相同的编辑不
  产生新版本；互不冲突的编辑任意顺序到达，最终状态一致。
- **增量重算**：脏集 = 被改任务 ∪ 依赖下游（前向）/ 依赖上游（后向），
  在新旧两张依赖图上求闭包（被删除的边也会让旧端点变脏）；`cpmFinish`
  变化时后向退化为全量；含 ALAP 约束时整体回退全量。增量结果与从头
  全量重算**逐项一致**（有随机化测试持续验证）。
- **并发读写**：写操作互斥，读操作通过 `Snapshot()` 拿到不可变版本
  指针，任何时刻看到的都是某个已提交的完整版本，不存在字段级混合状态。

### 编辑类型

`EditUpsertTask` / `EditRemoveTask`（自动清理悬挂依赖边）/
`EditUpsertResource` / `EditRemoveResource`（仍被占用时报错）/
`EditUpsertCalendar` / `EditSetProjectStart` / `EditReportProgress` /
`EditPinTask`。

## 基线、进度与钉住

- `plan.SetBaseline("v1")` 对当前时间轴拍快照；
  `Version.Variance()` 给出逐任务的 `StartVariance` / `FinishVariance`
  以及按 |完成偏移| 降序的 `MostDeviated` 列表。
- `EditReportProgress` 录入 `Status` / `PercentComplete`；已开始或已
  完成的任务自动钉住（`Pinned = true`，钉在当前计划开始或显式
  `PinnedStart`）。钉住的任务在后续重算中**不被重排**，但仍占用资源，
  其他任务绕着排。

## 甘特视图

`Version.View(ViewQuery)` 支持按时间窗口（`WindowStart`/`WindowEnd`）、
按资源（`ResourceID`）过滤，按 `Offset`/`Limit` 分页。行序固定为
`(Start, TaskID)` 升序，`Total` 为过滤后总行数——同一版本同一查询的
输出确定且可复现。

## 测试

```sh
go test ./... -race
```

覆盖：环路拒绝（含自环）、跨时区与夏令时、资源冲突调平与推迟归因、
增量与全量一致性（随机化编辑序列）、钉住任务、乱序/重复编辑收敛、
并发读写（race detector）、基线偏差、视图分页确定性。
