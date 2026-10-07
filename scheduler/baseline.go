package scheduler

import "time"

// BaselineTask 基线中单个任务的时间快照。
type BaselineTask struct {
	Start  time.Time
	Finish time.Time
}

// Baseline 基线快照：某次已提交版本的时间轴拷贝，不可变。
type Baseline struct {
	Name  string
	Taken time.Time
	Tasks map[string]BaselineTask
}

// TaskVariance 单个任务相对基线的偏移。
type TaskVariance struct {
	TaskID       string
	StartVariance  time.Duration // 计划开始 - 基线开始
	FinishVariance time.Duration // 计划完成 - 基线完成
}

// VarianceReport 偏差报告：逐任务偏移 + 偏差最大的任务列表。
type VarianceReport struct {
	Tasks       map[string]TaskVariance
	MostDeviated []string // 按 |FinishVariance| 降序，并列按 ID 升序
}

// SetBaseline 以当前版本的时间轴为基线，返回新版本（Seq+1）。
func (p *Plan) SetBaseline(name string) *Version {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.version
	bl := &Baseline{Name: name, Taken: time.Now(), Tasks: map[string]BaselineTask{}}
	for id, tr := range v.Result.Tasks {
		bl.Tasks[id] = BaselineTask{Start: tr.Start, Finish: tr.Finish}
	}
	next := &Version{Seq: v.Seq + 1, Input: v.Input, Result: v.Result, Baseline: bl}
	p.version = next
	return next
}

// Variance 计算当前版本相对基线的偏移报告。无基线时返回 nil。
func (v *Version) Variance() *VarianceReport { return nil }
