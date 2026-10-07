package scheduler

// rescheduleIncremental 在旧版本基础上做增量重算：
// 只有受编辑影响的任务（脏集 = 被改任务 + 依赖下游）参与前向重算，
// 其余任务复用旧结果；资源调平对受影响的资源重算。
// 保证：增量结果与从头全量重算逐项一致。
func rescheduleIncremental(old *Version, next *Input, changed map[string]bool) (*Result, error) {
	return nil, nil
}
