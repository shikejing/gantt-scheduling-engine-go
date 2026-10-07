// Package scheduler 提供项目排期与甘特视图引擎：
// 从任务/依赖/资源/日历的输入定义，一路计算到可复现、可增量重算、
// 可并发读取的时间轴视图。
package scheduler

import (
	"fmt"
	"time"
)

// DependencyType 依赖类型。
type DependencyType int

const (
	// FinishToStart 前置完成后，后续才能开始（默认）。
	FinishToStart DependencyType = iota
	// StartToStart 前置开始后，后续才能开始。
	StartToStart
	// FinishToFinish 前置完成后，后续才能完成。
	FinishToFinish
	// StartToFinish 前置开始后，后续才能完成。
	StartToFinish
)

func (d DependencyType) String() string {
	switch d {
	case FinishToStart:
		return "FS"
	case StartToStart:
		return "SS"
	case FinishToFinish:
		return "FF"
	case StartToFinish:
		return "SF"
	}
	return "?"
}

// Dependency 一条前置依赖，挂在后续任务上。
// Lag 为依赖延迟，按后续任务日历的工作时间计。
type Dependency struct {
	PredecessorID string
	Type          DependencyType
	Lag           time.Duration
}

// ConstraintType 时间约束类型。
type ConstraintType int

const (
	// ConstraintASAP 尽早开始（默认）。
	ConstraintASAP ConstraintType = iota
	// ConstraintALAP 尽晚开始（不推迟后续任务的前提下尽量晚）。
	ConstraintALAP
	// ConstraintMustStartOn 固定日期开始。
	ConstraintMustStartOn
	// ConstraintStartNoEarlierThan 不早于某日开始。
	ConstraintStartNoEarlierThan
	// ConstraintFinishNoLaterThan 不晚于某日完成。
	ConstraintFinishNoLaterThan
)

// Constraint 任务时间约束。
type Constraint struct {
	Type ConstraintType
	Date time.Time
}

// TaskStatus 任务执行状态。
type TaskStatus int

const (
	StatusNotStarted TaskStatus = iota
	StatusInProgress
	StatusCompleted
)

// ResourceAssignment 任务对资源的占用。
type ResourceAssignment struct {
	ResourceID string
	Units      float64 // 占用的产能单位数
}

// Task 一个待排任务。Duration 为工作时间（按日历折算为自然时间）。
type Task struct {
	ID           string
	Name         string
	Duration     time.Duration
	CalendarID   string // 为空则用默认日历
	Predecessors []Dependency
	Constraint   Constraint
	Resources    []ResourceAssignment
	Priority     int // 资源调平优先级，数值小者优先

	// 钉住：已开始/已完成的任务固定不动，重算不得重排。
	Pinned      bool
	PinnedStart time.Time

	Status          TaskStatus
	PercentComplete float64
}

// Resource 资源及其产能。
type Resource struct {
	ID         string
	Name       string
	Capacity   float64 // 每个工作时段可用的产能单位数
	CalendarID string  // 资源日历，为空用默认日历
}

// Input 排期输入定义（不可变使用，编辑通过 Plan.ApplyEdit 产生新副本）。
type Input struct {
	Tasks             map[string]*Task
	Resources         map[string]*Resource
	Calendars         map[string]*Calendar
	DefaultCalendarID string
	ProjectStart      time.Time
}

// DelayKind 推迟原因类别。
type DelayKind int

const (
	DelayByDependency DelayKind = iota // 被前置依赖推迟
	DelayByResource                    // 被资源冲突调平推迟
	DelayByConstraint                  // 被时间约束推迟
)

// DelayReason 说明某任务为何被推迟到 CPM 最早开始之后。
type DelayReason struct {
	Kind       DelayKind
	TaskID     string // 造成推迟的任务（依赖或占用同一资源者）
	ResourceID string // 冲突资源
	Until      time.Time
}

// TaskResult 单个任务的排期结果。
type TaskResult struct {
	TaskID      string
	Start       time.Time // 调平后的计划开始
	Finish      time.Time
	EarlyStart  time.Time // CPM 最早开始
	EarlyFinish time.Time
	LateStart   time.Time // CPM 尽晚开始
	LateFinish  time.Time
	TotalFloat  time.Duration // 总浮动（工作时间），可为负
	Critical    bool // 总浮动为零
	NegativeFloat bool // 总浮动为负（约束冲突），显式标出不归零
	Delayed     bool
	DelayReasons []DelayReason
}

// Result 一次完整排期结果，不可变。
type Result struct {
	Tasks          map[string]*TaskResult
	CriticalPath   []string
	ProjectStart   time.Time
	ProjectFinish  time.Time
	// cpmFinish 未调平网络的最大最早完成，供增量重算判断尽晚时间是否失效。
	cpmFinish time.Time
}

// CycleError 依赖图存在环路，排期被拒绝。
type CycleError struct {
	Path []string // 环路上的任务 ID，首尾相同
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("scheduler: dependency cycle detected: %v", e.Path)
}

// ValidationError 输入定义不合法（缺任务、缺资源、缺日历等）。
type ValidationError struct {
	Detail string
}

func (e *ValidationError) Error() string {
	return "scheduler: invalid input: " + e.Detail
}

// Clone 深拷贝任务（切片字段独立），保证已提交版本不可变。
func (t *Task) Clone() *Task {
	cp := *t
	cp.Predecessors = append([]Dependency(nil), t.Predecessors...)
	cp.Resources = append([]ResourceAssignment(nil), t.Resources...)
	return &cp
}
