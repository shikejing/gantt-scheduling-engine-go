package scheduler

import (
	"sort"
	"time"
)

// ViewQuery 甘特视图查询：按时间窗口与资源过滤，分页取用。
type ViewQuery struct {
	WindowStart time.Time // 只含与 [WindowStart, WindowEnd) 相交的任务；零值不限
	WindowEnd   time.Time
	ResourceID  string // 只含占用该资源的任务；空不限
	Offset      int    // 分页偏移（在过滤排序后的序列上）
	Limit       int    // 每页大小；<=0 表示不限
}

// ViewRow 甘特视图中的一行（一个任务条）。
type ViewRow struct {
	TaskID   string
	Name     string
	Start    time.Time
	Finish   time.Time
	Critical bool
	Delayed  bool
	Pinned   bool
	Resources []string
	PercentComplete float64
}

// ViewPage 一页视图结果。Total 为过滤后的总行数，输出行序确定且可复现。
type ViewPage struct {
	Rows  []ViewRow
	Total int
}

// View 从版本结果生成甘特视图页。
// 行序固定为 (Start, TaskID) 升序，因此同一版本同一查询输出完全一致、可复现。
func (v *Version) View(q ViewQuery) ViewPage {
	var rows []ViewRow
	for id, tr := range v.Result.Tasks {
		t := v.Input.Tasks[id]
		if !q.WindowStart.IsZero() && !tr.Finish.After(q.WindowStart) {
			continue
		}
		if !q.WindowEnd.IsZero() && !tr.Start.Before(q.WindowEnd) {
			continue
		}
		if q.ResourceID != "" {
			uses := false
			for _, ra := range t.Resources {
				if ra.ResourceID == q.ResourceID {
					uses = true
					break
				}
			}
			if !uses {
				continue
			}
		}
		row := ViewRow{
			TaskID:          id,
			Name:            t.Name,
			Start:           tr.Start,
			Finish:          tr.Finish,
			Critical:        tr.Critical,
			Delayed:         tr.Delayed,
			Pinned:          t.Pinned,
			PercentComplete: t.PercentComplete,
		}
		for _, ra := range t.Resources {
			row.Resources = append(row.Resources, ra.ResourceID)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Start.Equal(rows[j].Start) {
			return rows[i].Start.Before(rows[j].Start)
		}
		return rows[i].TaskID < rows[j].TaskID
	})
	total := len(rows)
	if q.Offset > 0 {
		if q.Offset >= len(rows) {
			rows = nil
		} else {
			rows = rows[q.Offset:]
		}
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return ViewPage{Rows: rows, Total: total}
}
