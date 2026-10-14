package metric

import "strings"

// aliases 把同一个健康概念的多种上游写法收敛到字典里的规范名。
//
// 为什么需要这一层：同一个概念在 HAE 的导出里有两种名字，字典只认其中一种。
// 血氧在字典里叫 oxygen_saturation，导出里叫 blood_oxygen_saturation；
// 站立时长字典叫 stand_time，导出叫 apple_stand_time。不做收敛的后果不是
// 「少一个指标」，而是双向失效——真实数据躺在另一个名字下，字典里的卡片
// 永远是空的，界面上既看不到数值也看不出哪里错了（旧名会被当成未知指标
// 静默入库，只有查 samples 表才发现）。
//
// 收敛目标怎么选：
//   - 字典里已有同一概念的条目 → 用字典的那个名字，历史数据才有归属；
//   - 字典里没有 → 用去掉来源前缀的可读名（apple_stand_hour → stand_hour），
//     与已有的 apple_exercise_time → exercise_time、apple_stand_time → stand_time 一致。
//
// 只登记「同一概念的不同写法」。语义不同的指标绝不合并：apple_stand_time 是
// 站立分钟数（48~187 min/天），apple_stand_hour 是站立小时数（9~19 小时/天），
// 合并会让两个量级完全不同、单位不同的指标互相污染。
//
// 改这里必须同步改 migrations/0004_metric_aliases.sql：历史行仍躺在旧名下，
// 只改入库侧会让老数据被搁浅。两者的一致性由 store/postgres 的契约测试锁定。
var aliases = map[string]string{
	// 血氧：Apple 的 HealthKit 标识是 OxygenSaturation，HAE 早期按完整标识导出
	// blood_oxygen_saturation，字典用的是短名。
	"blood_oxygen_saturation": "oxygen_saturation",

	// 锻炼时长 / 站立时长 / 站立小时数：苹果专有指标，HAE 带 apple_ 前缀。
	"apple_exercise_time": "exercise_time",
	"apple_stand_time":    "stand_time",
	"apple_stand_hour":    "stand_hour",

	// 步行+跑步距离：HealthKit 标识 DistanceWalkingRunning 的两种拼法，
	// 字典早期同时登记了两条，界面上会出现两个同名 chip（其中一个永远无数据）。
	"distance_walking_running": "walking_running_distance",

	// 体重：body_mass 是字典名，weight_&_body_mass 与 weight_body_mass 是导出侧写法。
	"weight_&_body_mass": "body_mass",
	"weight_body_mass":   "body_mass",

	// 睡眠腕温：苹果专有指标（AppleSleepingWristTemperature）。
	"apple_sleeping_wrist_temperature": "sleeping_wrist_temperature",
}

// Canonical 返回指标名的规范写法；没有别名时原样返回。
//
// 入库、聚合、水位三类写入都必须先过这里，否则同一概念会分裂成两个 metric：
// 一个在字典里有卡片但没数据，一个在库里堆数据但没有卡片。
func Canonical(name string) string {
	n := strings.TrimSpace(name)
	if c, ok := aliases[n]; ok {
		return c
	}
	return n
}

// Aliases 返回别名表的副本（别名 → 规范名）。
//
// 供迁移与契约测试比对：SQL 里的别名对必须与这里完全一致。
// 返回副本是为了让调用方排不了序也改不坏字典。
func Aliases() map[string]string {
	out := make(map[string]string, len(aliases))
	for k, v := range aliases {
		out[k] = v
	}
	return out
}
