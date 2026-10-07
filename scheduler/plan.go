package scheduler

import (
	"sync"
	"time"
)

// EditType 编辑类型。
type EditType int

const (
	EditUpsertTask EditType = iota // 新增或整体替换任务
	EditRemoveTask
	EditUpsertResource
	EditRemoveResource
	EditUpsertCalendar
	EditSetProjectStart
	EditReportProgress // 录入实际进度（Status/PercentComplete，自动钉住）
	EditPinTask        // 显式钉住/解除钉住
)

// Edit 一次对计划的编辑。ID 为幂等键：重复提交同一 ID 的编辑不会重复生效。
type Edit struct {
	ID   string
	Type EditType

	Task     *Task     // UpsertTask / ReportProgress(只需 ID+Status+PercentComplete)
	Resource *Resource // UpsertResource
	Calendar *Calendar // UpsertCalendar

	TaskID       string // RemoveTask / PinTask / ReportProgress
	Pinned       bool   // PinTask
	PinnedStart  *time.Time
	ProjectStart *time.Time // SetProjectStart
}

// Version 计划的一个已提交版本，不可变。读取方任何时刻看到的都是完整版本。
type Version struct {
	Seq      uint64
	Input    *Input
	Result   *Result
	Baseline *Baseline
}

// Plan 支持并发读写的计划。写操作串行化，读操作无锁读取已提交版本。
type Plan struct {
	mu      sync.RWMutex
	version *Version
	applied map[string]bool // 已应用的编辑 ID（幂等去重）
}

// NewPlan 以给定输入创建计划并做首次全量排期。
func NewPlan(in *Input) (*Plan, error) {
	res, err := Schedule(in)
	if err != nil {
		return nil, err
	}
	return &Plan{
		version: &Version{Seq: 1, Input: in, Result: res},
		applied: map[string]bool{},
	}, nil
}

// Snapshot 返回当前已提交版本。返回指针不可变，可并发安全读取。
func (p *Plan) Snapshot() *Version {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

// ApplyEdit 原子地应用一次编辑并增量重算：
// 校验或计算失败时计划保持原状，不留下算了一半的状态；
// 重复提交同一编辑 ID 直接返回当前版本（幂等）。
func (p *Plan) ApplyEdit(e Edit) (*Version, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.ID != "" && p.applied[e.ID] {
		return p.version, nil
	}
	next, err := applyEdit(p.version, e)
	if err != nil {
		return nil, err
	}
	if e.ID != "" {
		p.applied[e.ID] = true
	}
	p.version = next
	return next, nil
}

// applyEdit 在旧版本基础上产生新版本（含增量重算）。不修改旧版本：
// 任何失败都让整个编辑不生效，计划保持上一个已提交版本。
func applyEdit(v *Version, e Edit) (*Version, error) {
	in := v.Input
	changed := map[string]bool{}
	next := &Input{
		Tasks:             in.Tasks,
		Resources:         in.Resources,
		Calendars:         in.Calendars,
		DefaultCalendarID: in.DefaultCalendarID,
		ProjectStart:      in.ProjectStart,
	}

	switch e.Type {
	case EditUpsertTask:
		if e.Task == nil || e.Task.ID == "" {
			return nil, &ValidationError{Detail: "UpsertTask requires Task with non-empty ID"}
		}
		if old, ok := in.Tasks[e.Task.ID]; ok && taskEqual(old, e.Task) {
			return v, nil // 内容相同的重复提交：不产生新版本
		}
		next.Tasks = cloneTasks(in.Tasks)
		next.Tasks[e.Task.ID] = e.Task.Clone()
		changed[e.Task.ID] = true

	case EditRemoveTask:
		if _, ok := in.Tasks[e.TaskID]; !ok {
			return v, nil // 删除不存在的任务：幂等空操作
		}
		next.Tasks = cloneTasks(in.Tasks)
		delete(next.Tasks, e.TaskID)
		changed[e.TaskID] = true
		// 清理引用该任务的依赖边，被清理的任务也计入脏集。
		for id, t := range next.Tasks {
			var kept []Dependency
			removedEdge := false
			for _, d := range t.Predecessors {
				if d.PredecessorID == e.TaskID {
					removedEdge = true
					continue
				}
				kept = append(kept, d)
			}
			if removedEdge {
				nt := t.Clone()
				nt.Predecessors = kept
				next.Tasks[id] = nt
				changed[id] = true
			}
		}

	case EditUpsertResource:
		if e.Resource == nil || e.Resource.ID == "" {
			return nil, &ValidationError{Detail: "UpsertResource requires Resource with non-empty ID"}
		}
		if old, ok := in.Resources[e.Resource.ID]; ok && *old == *e.Resource {
			return v, nil
		}
		next.Resources = cloneResources(in.Resources)
		rc := *e.Resource
		next.Resources[rc.ID] = &rc
		markResourceUsers(in, rc.ID, changed)

	case EditRemoveResource:
		if _, ok := in.Resources[e.TaskID]; !ok {
			return v, nil
		}
		for id, t := range in.Tasks {
			for _, ra := range t.Resources {
				if ra.ResourceID == e.TaskID {
					return nil, &ValidationError{Detail: "resource " + e.TaskID + " still used by task " + id}
				}
			}
		}
		next.Resources = cloneResources(in.Resources)
		delete(next.Resources, e.TaskID)

	case EditUpsertCalendar:
		if e.Calendar == nil || e.Calendar.ID == "" {
			return nil, &ValidationError{Detail: "UpsertCalendar requires Calendar with non-empty ID"}
		}
		next.Calendars = cloneCalendars(in.Calendars)
		next.Calendars[e.Calendar.ID] = e.Calendar.Clone()
		markCalendarUsers(in, e.Calendar.ID, changed)

	case EditSetProjectStart:
		if e.ProjectStart == nil || e.ProjectStart.IsZero() {
			return nil, &ValidationError{Detail: "SetProjectStart requires non-zero time"}
		}
		if e.ProjectStart.Equal(in.ProjectStart) {
			return v, nil
		}
		next.ProjectStart = *e.ProjectStart
		for id := range in.Tasks {
			changed[id] = true
		}

	case EditReportProgress:
		t, ok := in.Tasks[e.TaskID]
		if !ok {
			return nil, &ValidationError{Detail: "ReportProgress: unknown task " + e.TaskID}
		}
		if e.Task == nil {
			return nil, &ValidationError{Detail: "ReportProgress requires Task payload"}
		}
		nt := t.Clone()
		nt.Status = e.Task.Status
		nt.PercentComplete = e.Task.PercentComplete
		if nt.Status != StatusNotStarted && !nt.Pinned {
			// 已开始/已完成：钉住，后续重算不得重排。
			nt.Pinned = true
			if e.PinnedStart != nil {
				nt.PinnedStart = *e.PinnedStart
			} else if oldR, ok := v.Result.Tasks[e.TaskID]; ok {
				nt.PinnedStart = oldR.Start
			} else {
				nt.PinnedStart = in.ProjectStart
			}
		}
		if taskEqual(t, nt) {
			return v, nil
		}
		next.Tasks = cloneTasks(in.Tasks)
		next.Tasks[nt.ID] = nt
		changed[nt.ID] = true

	case EditPinTask:
		t, ok := in.Tasks[e.TaskID]
		if !ok {
			return nil, &ValidationError{Detail: "PinTask: unknown task " + e.TaskID}
		}
		nt := t.Clone()
		nt.Pinned = e.Pinned
		if e.Pinned {
			if e.PinnedStart != nil {
				nt.PinnedStart = *e.PinnedStart
			} else if nt.PinnedStart.IsZero() {
				if oldR, ok := v.Result.Tasks[e.TaskID]; ok {
					nt.PinnedStart = oldR.Start
				} else {
					nt.PinnedStart = in.ProjectStart
				}
			}
		}
		if taskEqual(t, nt) {
			return v, nil
		}
		next.Tasks = cloneTasks(in.Tasks)
		next.Tasks[nt.ID] = nt
		changed[nt.ID] = true

	default:
		return nil, &ValidationError{Detail: "unknown edit type"}
	}

	res, err := rescheduleIncremental(v, next, changed)
	if err != nil {
		return nil, err
	}
	return &Version{Seq: v.Seq + 1, Input: next, Result: res, Baseline: v.Baseline}, nil
}

// taskEqual 判断两个任务定义是否完全一致（用于幂等去重）。
func taskEqual(a, b *Task) bool {
	if a.ID != b.ID || a.Name != b.Name || a.Duration != b.Duration ||
		a.CalendarID != b.CalendarID || a.Constraint != b.Constraint ||
		a.Priority != b.Priority || a.Pinned != b.Pinned ||
		!a.PinnedStart.Equal(b.PinnedStart) || a.Status != b.Status ||
		a.PercentComplete != b.PercentComplete {
		return false
	}
	if len(a.Predecessors) != len(b.Predecessors) || len(a.Resources) != len(b.Resources) {
		return false
	}
	for i := range a.Predecessors {
		if a.Predecessors[i] != b.Predecessors[i] {
			return false
		}
	}
	for i := range a.Resources {
		if a.Resources[i] != b.Resources[i] {
			return false
		}
	}
	return true
}

func cloneTasks(m map[string]*Task) map[string]*Task {
	out := make(map[string]*Task, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneResources(m map[string]*Resource) map[string]*Resource {
	out := make(map[string]*Resource, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneCalendars(m map[string]*Calendar) map[string]*Calendar {
	out := make(map[string]*Calendar, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// markResourceUsers 把占用某资源的全部任务计入脏集。
func markResourceUsers(in *Input, resourceID string, changed map[string]bool) {
	for id, t := range in.Tasks {
		for _, ra := range t.Resources {
			if ra.ResourceID == resourceID {
				changed[id] = true
			}
		}
	}
}

// markCalendarUsers 把使用某日历的全部任务（含经资源间接使用者）计入脏集。
func markCalendarUsers(in *Input, calendarID string, changed map[string]bool) {
	for id, t := range in.Tasks {
		if t.CalendarID == calendarID {
			changed[id] = true
			continue
		}
		for _, ra := range t.Resources {
			if r, ok := in.Resources[ra.ResourceID]; ok && r.CalendarID == calendarID {
				changed[id] = true
			}
		}
	}
	if in.DefaultCalendarID == calendarID {
		// 默认日历变更影响所有未显式指定日历的任务。
		for id, t := range in.Tasks {
			if t.CalendarID == "" {
				changed[id] = true
			}
		}
	}
}
