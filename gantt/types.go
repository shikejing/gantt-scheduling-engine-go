// Package gantt 提供项目排期与甘特视图引擎：
// 任务图 + 工作日历 + 资源调平 + 关键路径 + 增量重算 + 版本化并发读取。
package gantt

import "time"

// DepType 依赖类型。
type DepType int

const (
	DepFS DepType = iota // 完成-开始
	DepSS                // 开始-开始
	DepFF                // 完成-完成
	DepSF                // 开始-完成
)

func (d DepType) String() string {
	switch d {
	case DepFS:
		return "FS"
	case DepSS:
		return "SS"
	case DepFF:
		return "FF"
	case DepSF:
		return "SF"
	}
	return "?"
}

// ConstraintType 时间约束类型。
type ConstraintType int

const (
	ConstraintNone ConstraintType = iota
	ConstraintASAP                // 尽早开始
	ConstraintALAP                // 尽晚开始
	ConstraintMSO                 // 必须于某日开始（固定日期）
	ConstraintMFO                 // 必须于某日完成（固定日期）
)

// Dependency 任务间依赖，Lag 为延迟（按前置任务日历的工作时间计）。
type Dependency struct {
	From string
	To   string
	Type DepType
	Lag  time.Duration
}

// Constraint 任务时间约束。
type Constraint struct {
	Type ConstraintType
	Date time.Time // 仅 MSO/MFO 使用
}

// Task 计划中的一个任务。
type Task struct {
	ID         string
	Name       string
	Duration   time.Duration // 工作时长（按日历折算为工作时间）
	CalendarID string        // 为空则用计划默认日历
	Resources  []ResourceAssignment
	Constraint Constraint
	Priority   int // 资源调平优先级，数值小者优先
	Pinned     bool
}

// ResourceAssignment 任务对资源的占用。
type ResourceAssignment struct {
	ResourceID string
	Units      float64 // 占用产能比例，0 < Units <= 1 表示占用比例
}

// Resource 资源及其产能。
type Resource struct {
	ID         string
	Name       string
	Capacity   float64 // 同时可用的单位数，如 1 表示单人
	CalendarID string
}

// ScheduledTask 排期结果中的单个任务。
type ScheduledTask struct {
	TaskID       string
	Start        time.Time
	Finish       time.Time
	TotalFloat   time.Duration
	Critical     bool
	DelayReasons []DelayReason // 因调平/依赖被推迟的原因
}

// DelayReason 说明任务为何被推迟。
type DelayReason struct {
	Kind     string // "dependency" 或 "resource"
	TaskID   string // 造成推迟的依赖前置任务（Kind=dependency）
	Resource string // 造成推迟的资源（Kind=resource）
	Until    time.Time
}

// ScheduleResult 一次完整排算的结果（不可变）。
type ScheduleResult struct {
	Tasks        map[string]*ScheduledTask
	CriticalPath []string
	ProjectStart time.Time
	ProjectEnd   time.Time
}

// Plan 计划定义（输入）。
type Plan struct {
	ID           string
	DefaultCal   string
	Calendars    map[string]*Calendar
	Tasks        map[string]*Task
	Dependencies []Dependency
	Resources    map[string]*Resource
	ProjectStart time.Time
	// Pins 钉住任务的开始时刻（任务须 Pinned=true），重算时不得重排。
	Pins map[string]time.Time
}

// NewPlan 创建空计划。
func NewPlan(id string, projectStart time.Time) *Plan {
	return &Plan{
		ID:           id,
		Calendars:    map[string]*Calendar{},
		Tasks:        map[string]*Task{},
		Resources:    map[string]*Resource{},
		ProjectStart: projectStart,
		Pins:         map[string]time.Time{},
	}
}
