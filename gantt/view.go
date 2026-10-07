package gantt

import (
	"sort"
	"time"
)

// Bar 甘特视图中的一根条。
type Bar struct {
	TaskID   string
	Name     string
	Start    time.Time
	Finish   time.Time
	Critical bool
	Float    time.Duration
}

// ViewQuery 视图分页查询：按时间窗口与/或资源过滤。
type ViewQuery struct {
	From, To   time.Time // 时间窗口（零值不限）
	ResourceID string    // 按资源过滤（空不限）
	Offset     int
	Limit      int // <=0 表示不限
}

// View 从版本生成分页视图；输出确定且可复现（按开始时刻、任务 ID 排序）。
func View(v *Version, q ViewQuery) []Bar {
	if v == nil || v.Result == nil {
		return nil
	}
	var bars []Bar
	for id, st := range v.Result.Tasks {
		if !q.From.IsZero() && !st.Finish.After(q.From) {
			continue
		}
		if !q.To.IsZero() && !st.Start.Before(q.To) {
			continue
		}
		if q.ResourceID != "" {
			t, ok := v.Plan.Tasks[id]
			if !ok || !usesResource(t, q.ResourceID) {
				continue
			}
		}
		name := id
		if t, ok := v.Plan.Tasks[id]; ok {
			name = t.Name
		}
		bars = append(bars, Bar{
			TaskID:   id,
			Name:     name,
			Start:    st.Start,
			Finish:   st.Finish,
			Critical: st.Critical,
			Float:    st.TotalFloat,
		})
	}
	sort.Slice(bars, func(i, j int) bool {
		if !bars[i].Start.Equal(bars[j].Start) {
			return bars[i].Start.Before(bars[j].Start)
		}
		return bars[i].TaskID < bars[j].TaskID
	})
	if q.Offset > 0 {
		if q.Offset >= len(bars) {
			return nil
		}
		bars = bars[q.Offset:]
	}
	if q.Limit > 0 && q.Limit < len(bars) {
		bars = bars[:q.Limit]
	}
	return bars
}

func usesResource(t *Task, rid string) bool {
	for _, ra := range t.Resources {
		if ra.ResourceID == rid {
			return true
		}
	}
	return false
}
