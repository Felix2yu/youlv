package csvin

import (
	"strconv"
	"strings"
	"time"
)

// 时间戳 layout 候选，按命中概率排序。
//
// 第一位最关键：Rond 导出的是 `2026-10-23T14:11:49+0800`——时区偏移
// **没有冒号**，对应 Go layout 的 `-0700` 而不是 `-07:00`。写错这一处
// 会让全部 4280 条到访记录解析失败。
var timeLayouts = []string{
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
	"2006/01/02 15:04",
	"2006-01-02",
	"2006/01/02",
}

// TimeValue 是解析结果。
type TimeValue struct {
	Unix  int64  // UTC unix 秒
	Local string // 规范化的本地时间串 2006-01-02T15:04:05+08:00
	Naive bool   // true 表示输入不含时区，已按 tz 解释
	Year  int
	Date  string // 2006-01-02
	Month string // 2006-01
	Hour  int
	Dow   int // 0 = 周日
}

// ParseTime 按候选 layout 依次尝试。全部失败才返回错误。
func ParseTime(s string, tz *time.Location) (TimeValue, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return TimeValue{}, errEmptyTime
	}
	if tz == nil {
		tz = time.UTC
	}

	for i, layout := range timeLayouts {
		t, err := time.ParseInLocation(layout, s, tz)
		if err != nil {
			continue
		}
		// 前两个 layout 带时区信息；其余按 tz 解释。
		naive := i >= 3
		return newTimeValue(t, naive), nil
	}
	return TimeValue{}, errBadTime
}

func newTimeValue(t time.Time, naive bool) TimeValue {
	return TimeValue{
		Unix:  t.Unix(),
		Local: t.Format("2006-01-02T15:04:05-07:00"),
		Naive: naive,
		Year:  t.Year(),
		Date:  t.Format("2006-01-02"),
		Month: t.Format("2006-01"),
		Hour:  t.Hour(),
		Dow:   int(t.Weekday()),
	}
}

// FloorHour 把 unix 秒向下取整到整点。天气数据是整点粒度，
// 到访时刻要落到同一个整点才能对上。
func FloorHour(unix int64) int64 { return unix - unix%3600 }

// LocalKeys 在给定时区下算出分组键。
//
// 刻意不用 SQLite 的 strftime(..., 'localtime')：那依赖宿主 TZ 环境变量，
// 换台机器结果就变。
func LocalKeys(unix int64, tz *time.Location) (date, month string, year, hour, dow int) {
	if tz == nil {
		tz = time.UTC
	}
	t := time.Unix(unix, 0).In(tz)
	return t.Format("2006-01-02"), t.Format("2006-01"), t.Year(), t.Hour(), int(t.Weekday())
}

// DurationFmt 把秒数渲染成「2 小时 13 分」这类中文串。
func DurationFmt(sec int64) string {
	if sec < 60 {
		return strconv.FormatInt(sec, 10) + " 秒"
	}
	if sec < 3600 {
		return strconv.FormatInt(sec/60, 10) + " 分钟"
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	if m == 0 {
		return strconv.FormatInt(h, 10) + " 小时"
	}
	return strconv.FormatInt(h, 10) + " 小时 " + strconv.FormatInt(m, 10) + " 分"
}

var (
	errEmptyTime = &timeErr{"时间为空"}
	errBadTime   = &timeErr{"时间格式无法识别"}
)

type timeErr struct{ msg string }

func (e *timeErr) Error() string { return e.msg }
