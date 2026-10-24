package csvin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func decodeString(t *testing.T, name, body string) *File {
	t.Helper()
	f, issues, err := Decode(name, strings.NewReader(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	_ = issues
	return f
}

func TestDecodeStripsBOM(t *testing.T) {
	// BOM 不剥掉的话第一列名会是 "\ufeff日期"，按名寻址全线失效。
	f := decodeString(t, "天气数据.csv", "\ufeff日期,地点,天气状况\n2026-09-27T17:00:00+0800,示例地点,多云\n")
	if f.Header[0] != "日期" {
		t.Fatalf("表头首列 = %q, want 日期（BOM 未剥离）", f.Header[0])
	}
	if len(f.Records) != 1 {
		t.Fatalf("数据行数 = %d, want 1", len(f.Records))
	}
}

func TestDecodeRaggedRows(t *testing.T) {
	body := "名称,纬度,经度\n甲,31.1,120.1\n乙,31.2\n丙,31.3,120.3,多余\n"
	f, issues, err := Decode("t.csv", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(f.Records) != 3 {
		t.Fatalf("行数 = %d, want 3", len(f.Records))
	}
	if got := f.Records[1]; len(got) != 3 || got[2] != "" {
		t.Errorf("短行未补齐: %#v", got)
	}
	if got := f.Records[2]; len(got) != 3 || got[2] != "120.3" {
		t.Errorf("长行未截断: %#v", got)
	}
	if len(issues) != 2 {
		t.Errorf("应产生 2 条行宽告警，实际 %d 条", len(issues))
	}
}

func TestDecodeLazyQuotes(t *testing.T) {
	body := "名称,纬度,经度\n\"甲\"乙,31.1,120.1\n"
	f, _, err := Decode("t.csv", strings.NewReader(body))
	if err != nil {
		t.Fatalf("裸引号导致整批失败: %v", err)
	}
	if f.Records[0][0] == "" {
		t.Error("裸引号行被吞掉了")
	}
}

func TestDetectAllFourKinds(t *testing.T) {
	cases := []struct {
		header string
		want   Kind
	}{
		{"名称,纬度,经度,评分,活跃范围半径,国家或地区,行政区域,次级行政区域,城市/地区,区,街道,门牌号", KindPlaces},
		{"活动,地点,抵达时刻,离开时刻,时区,纬度,经度,标签,备注", KindVisits},
		{"类型,从,至,时区,时长 (s),出发地,目的地,备注", KindTrips},
		{"日期,天气状况,温度 (°C),体感温度 (°C),露点 (°C),湿度,降水类型,降水量 (mm),降水概率,紫外线指数,紫外线等级,风向,风速 (m/s),能见度 (km),地点", KindWeather},
	}
	for _, c := range cases {
		res, err := Detect(strings.Split(c.header, ","))
		if err != nil {
			t.Fatalf("Detect(%s): %v", c.header, err)
		}
		if res.Kind != c.want {
			t.Errorf("表头 %q => %s, want %s", c.header[:20], res.Kind, c.want)
		}
		if res.Confidence != 1.0 {
			t.Errorf("%s 置信度 = %.2f, want 1.0（缺: %v）", c.want, res.Confidence, res.MissingReq)
		}
		if len(res.Extra) != 0 {
			t.Errorf("%s 出现无法识别的列: %v", c.want, res.Extra)
		}
	}
}

// TestDetectIgnoresColumnOrder 列顺序变化不能影响识别——按名寻址的意义所在。
func TestDetectIgnoresColumnOrder(t *testing.T) {
	shuffled := []string{"经度", "地点", "离开时刻", "备注", "活动", "时区", "纬度", "标签", "抵达时刻"}
	res, err := Detect(shuffled)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Kind != KindVisits || res.Confidence != 1.0 {
		t.Fatalf("打乱列序后 => %s (%.2f), want visits 1.0", res.Kind, res.Confidence)
	}
	// 按名取到的值必须来自正确下标。
	rec := []string{"120.5", "示例园林", "2026-10-23T18:00:00+0800", "注", "园林", "Asia/Shanghai", "31.3", "园林", "2026-10-23T14:00:00+0800"}
	if got := res.Mapping.Get(rec, "活动"); got != "园林" {
		t.Errorf("取「活动」得 %q, want 园林", got)
	}
	if got := res.Mapping.Get(rec, "地点"); got != "示例园林" {
		t.Errorf("取「地点」得 %q, want 示例园林", got)
	}
	if got := res.Mapping.Get(rec, "经度"); got != "120.5" {
		t.Errorf("取「经度」得 %q, want 120.5", got)
	}
}

func TestDetectRejectsUnknown(t *testing.T) {
	res, err := Detect([]string{"foo", "bar", "baz"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Confidence >= MinDetectConfidence {
		t.Errorf("无关表头置信度 %.2f，不应被自动确定", res.Confidence)
	}
}

func TestParseTimeOffsetWithoutColon(t *testing.T) {
	tz := time.FixedZone("Asia/Shanghai", 8*3600)
	// Rond 导出的主格式：时区偏移没有冒号。
	v, err := ParseTime("2026-10-23T14:11:49+0800", tz)
	if err != nil {
		t.Fatalf("ParseTime: %v", err)
	}
	want := time.Date(2026, 10, 23, 14, 11, 49, 0, tz).Unix()
	if v.Unix != want {
		t.Fatalf("Unix = %d, want %d", v.Unix, want)
	}
	if v.Date != "2026-10-23" || v.Month != "2026-10" || v.Year != 2026 || v.Hour != 14 {
		t.Errorf("分组键错误: date=%s month=%s year=%d hour=%d", v.Date, v.Month, v.Year, v.Hour)
	}
	if v.Naive {
		t.Error("带时区的输入不应被标为 naive")
	}
}

func TestParseTimeFallbacks(t *testing.T) {
	tz := time.FixedZone("Asia/Shanghai", 8*3600)
	samples := []string{
		"2026-10-23T14:11:49+0800",
		"2026-10-23T14:11:49+08:00",
		"2026-10-23T14:11:49",
		"2026-10-23 14:11:49",
		"2026/10/23 14:11",
		"2026-10-23",
	}
	for _, s := range samples {
		if _, err := ParseTime(s, tz); err != nil {
			t.Errorf("ParseTime(%q): %v", s, err)
		}
	}
	if _, err := ParseTime("昨天下午", tz); err == nil {
		t.Error("无意义时间应报错")
	}
	if _, err := ParseTime("", tz); err == nil {
		t.Error("空串应报错")
	}
}

func TestSplitTags(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"园林", 1},
		{"童瑶, 园林", 2},    // Rond 实际写法：英文逗号 + 空格
		{"博物馆，园林", 2},    // 中文逗号
		{"甲、乙、丙", 3},     // 顿号
		{"园林, 园林, 甲", 2}, // 段内去重
		{"   ", 0},       // 纯空白
	}
	for _, c := range cases {
		if got := SplitTags(c.in); len(got) != c.want {
			t.Errorf("SplitTags(%q) = %v (%d 项), want %d 项", c.in, got, len(got), c.want)
		}
	}
}

func TestNumericParsers(t *testing.T) {
	if v, ok := ParseFloat("27.280000686645508"); !ok || v != 27.280000686645508 {
		t.Errorf("ParseFloat 噪声值: %v %v", v, ok)
	}
	if _, ok := ParseFloat(""); ok {
		t.Error("空串应返回 false")
	}
	if _, ok := ParseFloat("abc"); ok {
		t.Error("非法值应返回 false")
	}
	if v, ok := ParseInt("3.0"); !ok || v != 3 {
		t.Errorf("ParseInt(\"3.0\") = %d %v, want 3", v, ok)
	}
}

func TestFloorHour(t *testing.T) {
	tz := time.FixedZone("Asia/Shanghai", 8*3600)
	v, _ := ParseTime("2026-09-27T17:34:00+0800", tz)
	got := time.Unix(FloorHour(v.Unix), 0).In(tz)
	if got.Hour() != 17 || got.Minute() != 0 {
		t.Errorf("FloorHour => %s, want 17:00", got.Format("15:04"))
	}
}

// dataDir 返回要测试的 CSV 数据目录。
//
// 优先级：环境变量 YOULV_REAL_CSV_DIR（本地开发者用它跑自己的真实导出）
// > 仓库内的脱敏 fixture（CI 走这条，见 testdata/rond-export/README.md）。
func dataDir(t *testing.T) string {
	t.Helper()
	need := []string{"places.csv", "visits.csv", "trips.csv", "weather.csv"}
	hasAll := func(dir string) bool {
		for _, n := range need {
			if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
				return false
			}
		}
		return true
	}
	if d := os.Getenv("YOULV_REAL_CSV_DIR"); d != "" && hasAll(d) {
		return d
	}
	if d := filepath.Join("..", "..", "testdata", "rond-export"); hasAll(d) {
		return d
	}
	t.Skip("没有可用的 CSV 数据集（设置 YOULV_REAL_CSV_DIR，或使用仓库内 testdata/rond-export）")
	return ""
}

// TestRealExports 对照全量 CSV 做端到端核对。
//
// 默认用仓库内的脱敏 fixture（CI 每次都跑）；设置 YOULV_REAL_CSV_DIR 可换成
// 自己的真实导出。fixture 保留了原始数据的全部结构性质，所以两者的判据一致。
func TestRealExports(t *testing.T) {
	dir := dataDir(t)
	cases := []struct {
		file string
		kind Kind
		rows int
	}{
		{"places.csv", KindPlaces, 893},
		{"visits.csv", KindVisits, 4280},
		{"trips.csv", KindTrips, 5143},
		{"weather.csv", KindWeather, 2533},
	}
	for _, c := range cases {
		f, err := os.Open(filepath.Join(dir, c.file))
		if err != nil {
			t.Fatalf("打开 %s: %v", c.file, err)
		}
		dec, issues, err := Decode(c.file, f)
		f.Close()
		if err != nil {
			t.Fatalf("Decode %s: %v", c.file, err)
		}
		res, err := Detect(dec.Header)
		if err != nil {
			t.Fatalf("Detect %s: %v", c.file, err)
		}
		if res.Kind != c.kind {
			t.Errorf("%s 识别为 %s, want %s", c.file, res.Kind, c.kind)
		}
		if res.Confidence != 1.0 {
			t.Errorf("%s 置信度 %.2f，缺必需列 %v", c.file, res.Confidence, res.MissingReq)
		}
		if len(res.Extra) != 0 {
			t.Errorf("%s 有未识别列 %v", c.file, res.Extra)
		}
		if len(dec.Records) != c.rows {
			t.Errorf("%s 数据行 %d, want %d", c.file, len(dec.Records), c.rows)
		}
		var bad int
		for _, i := range issues {
			if i.Severity == SevError {
				bad++
			}
		}
		if bad > 0 {
			t.Errorf("%s 解码阶段产生 %d 条 error", c.file, bad)
		}
		t.Logf("%s: %d 行, 类型 %s, 置信度 %.2f, 可选列缺 %v",
			c.file, len(dec.Records), res.Kind, res.Confidence, res.MissingOpt)
	}
}
