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

// applyEdit 在旧版本基础上产生新版本（含增量重算）。不修改旧版本。
func applyEdit(v *Version, e Edit) (*Version, error) {
	return nil, &ValidationError{Detail: "not implemented"}
}
