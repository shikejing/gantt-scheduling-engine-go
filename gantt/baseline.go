package gantt

import (
	"sort"
	"time"
)

// Baseline 基线快照（不可变）。
type Baseline struct {
	TakenAt time.Time
	Tasks   map[string]BaselineTask
}

// BaselineTask 基线中单个任务的时间。
type BaselineTask struct {
	Start  time.Time
	Finish time.Time
}

// NewBaseline 从排期结果创建基线。
func NewBaseline(r *ScheduleResult) *Baseline {
	b := &Baseline{TakenAt: time.Now(), Tasks: map[string]BaselineTask{}}
	for id, st := range r.Tasks {
		b.Tasks[id] = BaselineTask{Start: st.Start, Finish: st.Finish}
	}
	return b
}

// Variance 相对基线的偏移。
type Variance struct {
	TaskID    string
	StartVar  time.Duration
	FinishVar time.Duration
}

// Compare 计算当前结果相对基线的偏移，按完成偏移绝对值降序（偏差最大的在前）。
func (b *Baseline) Compare(r *ScheduleResult) []Variance {
	var out []Variance
	for id, bt := range b.Tasks {
		st, ok := r.Tasks[id]
		if !ok {
			continue
		}
		out = append(out, Variance{
			TaskID:    id,
			StartVar:  st.Start.Sub(bt.Start),
			FinishVar: st.Finish.Sub(bt.Finish),
		})
	}
	abs := func(d time.Duration) time.Duration {
		if d < 0 {
			return -d
		}
		return d
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := abs(out[i].FinishVar), abs(out[j].FinishVar)
		if ai != aj {
			return ai > aj
		}
		return out[i].TaskID < out[j].TaskID
	})
	return out
}

// Actuals 实际进度录入。
type Actuals struct {
	Started  map[string]time.Time
	Finished map[string]time.Time
}

// PinStarted 把已开始/已完成的任务钉住（写入计划，后续重算不得重排）。
// 已开始任务钉在实际开始时刻；已完成任务同样钉住（其完成时刻由日历+工期推出，
// 如需精确完成时刻请同时把工期改为实际工期）。
func PinStarted(p *Plan, a *Actuals) {
	for id, t := range a.Started {
		if task, ok := p.Tasks[id]; ok {
			task.Pinned = true
			p.Pins[id] = t
		}
	}
	for id, t := range a.Finished {
		if task, ok := p.Tasks[id]; ok {
			task.Pinned = true
			if _, started := a.Started[id]; !started {
				p.Pins[id] = t
			}
		}
	}
}
