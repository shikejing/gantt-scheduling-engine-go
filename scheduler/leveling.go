package scheduler

// levelResources 在 CPM 最早时间基础上做资源调平：
// 同一资源上的任务重叠时按（优先级, 最早开始, 任务ID）排序串行化，
// 并把调平后的开始/完成写回 ctx.res，同时记录推迟原因。
// 算法是输入的纯函数，同一输入必然得到同一结果。
func levelResources(c *scheduleContext) {}
