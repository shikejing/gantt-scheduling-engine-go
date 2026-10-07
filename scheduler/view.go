package scheduler

import "time"

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

// View 从版本结果生成甘特视图页。同一版本同一查询输出完全一致。
func (v *Version) View(q ViewQuery) ViewPage { return ViewPage{} }
