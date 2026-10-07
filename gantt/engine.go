package gantt

import (
	"fmt"
	"sync"
	"time"
)

// Edit 描述一次对计划的编辑。
// 每个实体（任务/依赖/资源/日历）按 Seq 最后写入者胜出：
// 乱序到达或重复提交时最终状态收敛。
type Edit struct {
	Seq    uint64 // 全局单调递增序号
	Op     EditOp
	Task   *Task
	Dep    *Dependency
	Res    *Resource
	Cal    *Calendar
	TaskID string    // 删除/钉住任务用
	Pin    time.Time // OpPinTask：钉住的开始时刻
}

// EditOp 编辑操作类型。
type EditOp int

const (
	OpUpsertTask EditOp = iota
	OpDeleteTask
	OpAddDep
	OpRemoveDep
	OpUpsertResource
	OpUpsertCalendar
	OpPinTask
	OpUnpinTask
)

// Version 一个已提交的不可变计划版本。
// 读取方不得修改 Plan/Result 内部字段。
type Version struct {
	Seq    uint64
	Plan   *Plan
	Result *ScheduleResult
}

// Engine 支持并发读写的排期引擎。
// 读取方任何时刻看到的都是某个已提交的完整版本（MVCC）；
// 写操作先作用于副本，排算成功才发布，失败不留半成品。
type Engine struct {
	mu      sync.RWMutex
	plan    *Plan
	result  *ScheduleResult
	version uint64
	lastSeq map[string]uint64 // 实体键 -> 已应用的最大 Seq
}

// NewEngine 创建引擎并完成首次全量排算。
func NewEngine(p *Plan) (*Engine, error) {
	res, err := Schedule(p)
	if err != nil {
		return nil, err
	}
	return &Engine{
		plan:    p,
		result:  res,
		version: 0,
		lastSeq: map[string]uint64{},
	}, nil
}

// entityKey 返回编辑作用的实体键。
func entityKey(e Edit) string {
	switch e.Op {
	case OpUpsertTask:
		return "task:" + e.Task.ID
	case OpDeleteTask, OpPinTask, OpUnpinTask:
		return "task:" + e.TaskID
	case OpAddDep, OpRemoveDep:
		return fmt.Sprintf("dep:%s>%s>%d", e.Dep.From, e.Dep.To, e.Dep.Type)
	case OpUpsertResource:
		return "res:" + e.Res.ID
	case OpUpsertCalendar:
		return "cal:" + e.Cal.ID
	}
	return "?"
}

// Apply 应用一批编辑并重算；任一编辑导致排算失败时整体不生效。
// 重复或乱序的编辑按实体 Seq 去重，最终状态收敛。
func (e *Engine) Apply(edits ...Edit) (*Version, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	np := copyPlan(e.plan)
	changed := false
	pending := map[string]uint64{}
	for _, ed := range edits {
		key := entityKey(ed)
		last := e.lastSeq[key]
		if p, ok := pending[key]; ok && p > last {
			last = p
		}
		if ed.Seq <= last {
			continue // 重复或过期编辑
		}
		if err := applyEdit(np, ed); err != nil {
			return nil, err
		}
		pending[key] = ed.Seq
		changed = true
	}
	if !changed {
		return &Version{Seq: e.version, Plan: e.plan, Result: e.result}, nil
	}
	res, err := Schedule(np)
	if err != nil {
		return nil, err // 引擎状态未变，不留半成品
	}
	for k, v := range pending {
		e.lastSeq[k] = v
	}
	e.plan = np
	e.result = res
	e.version++
	return &Version{Seq: e.version, Plan: np, Result: res}, nil
}

// applyEdit 把单个编辑作用到计划副本。
func applyEdit(p *Plan, e Edit) error {
	switch e.Op {
	case OpUpsertTask:
		t := *e.Task
		p.Tasks[t.ID] = &t
	case OpDeleteTask:
		delete(p.Tasks, e.TaskID)
		delete(p.Pins, e.TaskID)
		var deps []Dependency
		for _, d := range p.Dependencies {
			if d.From != e.TaskID && d.To != e.TaskID {
				deps = append(deps, d)
			}
		}
		p.Dependencies = deps
	case OpAddDep:
		for _, d := range p.Dependencies {
			if d.From == e.Dep.From && d.To == e.Dep.To && d.Type == e.Dep.Type {
				return nil // 已存在，幂等
			}
		}
		p.Dependencies = append(p.Dependencies, *e.Dep)
	case OpRemoveDep:
		var deps []Dependency
		for _, d := range p.Dependencies {
			if !(d.From == e.Dep.From && d.To == e.Dep.To && d.Type == e.Dep.Type) {
				deps = append(deps, d)
			}
		}
		p.Dependencies = deps
	case OpUpsertResource:
		r := *e.Res
		p.Resources[r.ID] = &r
	case OpUpsertCalendar:
		c := *e.Cal
		p.Calendars[c.ID] = &c
	case OpPinTask:
		t, ok := p.Tasks[e.TaskID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTask, e.TaskID)
		}
		t.Pinned = true
		p.Pins[e.TaskID] = e.Pin
	case OpUnpinTask:
		t, ok := p.Tasks[e.TaskID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTask, e.TaskID)
		}
		t.Pinned = false
		delete(p.Pins, e.TaskID)
	}
	return nil
}

// Snapshot 读取当前已提交版本（并发安全，结果不可变）。
func (e *Engine) Snapshot() *Version {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return &Version{Seq: e.version, Plan: e.plan, Result: e.result}
}

// Recompute 强制全量重算（用于校验增量与全量一致）；结果不变则不升版本。
func (e *Engine) Recompute() (*Version, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	res, err := Schedule(e.plan)
	if err != nil {
		return nil, err
	}
	e.result = res
	return &Version{Seq: e.version, Plan: e.plan, Result: res}, nil
}

// copyPlan 深拷贝计划，保证已发布版本不可变。
func copyPlan(p *Plan) *Plan {
	np := &Plan{
		ID:           p.ID,
		DefaultCal:   p.DefaultCal,
		ProjectStart: p.ProjectStart,
		Calendars:    map[string]*Calendar{},
		Tasks:        map[string]*Task{},
		Resources:    map[string]*Resource{},
		Pins:         map[string]time.Time{},
	}
	for k, c := range p.Calendars {
		nc := *c
		nc.WorkDays = c.WorkDays
		nc.Holidays = map[string]bool{}
		for h, v := range c.Holidays {
			nc.Holidays[h] = v
		}
		nc.Intervals = append([]WorkInterval{}, c.Intervals...)
		np.Calendars[k] = &nc
	}
	for k, t := range p.Tasks {
		nt := *t
		nt.Resources = append([]ResourceAssignment{}, t.Resources...)
		np.Tasks[k] = &nt
	}
	for k, r := range p.Resources {
		nr := *r
		np.Resources[k] = &nr
	}
	np.Dependencies = append([]Dependency{}, p.Dependencies...)
	for k, v := range p.Pins {
		np.Pins[k] = v
	}
	return np
}
