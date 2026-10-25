package importer

import "github.com/Felix2yu/youlv/internal/csvin"

// Options 是导入选项。整批四份文件共用一套，并快照进 imports.options_json，
// 使回滚后能按同样选项重导。
type Options struct {
	Timezone string `json:"timezone"`

	// DupStrategy 是业务记录（地点/到访/交通）遇到重复时的策略。
	DupStrategy string `json:"dup_strategy"` // skip|overwrite|reject
	// WeatherDupStrategy 单独一项：天气冲突 57 组需要单独裁决。
	WeatherDupStrategy string `json:"weather_dup_strategy"`

	// DepartedBeforeArrived 处理「离开早于抵达」：null|reject|swap
	DepartedBeforeArrived string `json:"departed_before_arrived"`

	CreateStubs    bool `json:"create_stubs"`
	RequirePlaces  bool `json:"require_places"`
	RatingZeroNull bool `json:"rating_zero_null"`
	RadiusZeroNull bool `json:"radius_zero_null"`

	SnapMeters    float64 `json:"snap_meters"`
	NearestMeters float64 `json:"nearest_meters"`
}

// DefaultOptions 返回默认选项。
//
// 默认值全部取「最不容易丢数据」的一档：重复跳过（幂等）、时间倒挂置空
// 离开时刻而不是整行丢弃、孤儿建桩。
func DefaultOptions() Options {
	return Options{
		Timezone:              "Asia/Shanghai",
		DupStrategy:           "skip",
		WeatherDupStrategy:    "skip",
		DepartedBeforeArrived: "null",
		CreateStubs:           true,
		RequirePlaces:         true,
		RatingZeroNull:        true,
		RadiusZeroNull:        true,
		SnapMeters:            DefaultSnapMeters,
		NearestMeters:         DefaultNearestMeters,
	}
}

// FileStat 是单个文件的统计。
type FileStat struct {
	Kind       csvin.Kind `json:"kind"`
	Label      string     `json:"label"`
	File       string     `json:"file"`
	SHA256     string     `json:"sha256"`
	Rows       int        `json:"rows"`
	Ready      int        `json:"ready"`    // 通过校验、等待写入
	Failed     int        `json:"failed"`   // error 级，不会写入
	Warned     int        `json:"warned"`   // 带 warn 但仍会写入
	DupSkip    int        `json:"dup_skip"` // 与库内已有记录重复
	Inserted   int        `json:"inserted"` // 执行后回填
	Skipped    int        `json:"skipped"`
	Promoted   int        `json:"promoted"` // 执行后回填：仅引用桩升级为正式地点的条数
	Detected   string     `json:"detected"` // 自动识别的类型
	Confidence float64    `json:"confidence"`
	Missing    []string   `json:"missing,omitempty"`
	// MissingOpt 是「认识但没提供」的列。它不阻断导入，却会让对应数据静默
	// 变空（缺经纬度 → 坐标 (0,0)；缺温度 → 天气没有温度），必须回显给用户。
	// 从前它只在 /detect 端点可见，预检报告里看不到。
	MissingOpt []string `json:"missing_opt,omitempty"`
	// Encoding 是这份文件实际按哪种编码解码的（utf-8 / gb18030 / unknown）。
	Encoding string   `json:"encoding,omitempty"`
	Extra    []string `json:"extra,omitempty"`
}

// FieldDiff 是冲突裁决里的一个字段差异。
type FieldDiff struct {
	Field string `json:"field"`
	Label string `json:"label"`
	Old   any    `json:"old"`
	New   any    `json:"new"`
}

// ConflictGroup 是一组需要用户裁决的冲突。
// 天气的 57 组「内容冲突」走这里；57 组「内容完全一致」不进这里，自动跳过。
type ConflictGroup struct {
	Key       string      `json:"key"` // "<placeKey>|<hourTS>"
	Kind      csvin.Kind  `json:"kind"`
	PlaceKey  string      `json:"place_key"`
	PlaceName string      `json:"place_name"`
	Hour      string      `json:"hour"`
	Diffs     []FieldDiff `json:"diffs"`
	OldRaw    string      `json:"old_raw"`
	NewRaw    string      `json:"new_raw"`
}

// Report 是预检或执行的结果。
type Report struct {
	BatchNo    string          `json:"batch_no"`
	Status     string          `json:"status"`
	Options    Options         `json:"options"`
	Files      []FileStat      `json:"files"`
	Issues     []csvin.Issue   `json:"issues"`
	Conflicts  []ConflictGroup `json:"conflicts"`
	Highlights []string        `json:"highlights"`
	Blocking   []string        `json:"blocking"`
	Totals     struct {
		Rows     int `json:"rows"`
		Ready    int `json:"ready"`
		Failed   int `json:"failed"`
		DupSkip  int `json:"dup_skip"`
		Inserted int `json:"inserted"`
		Skipped  int `json:"skipped"`
		// Promoted 是「仅引用桩升级为正式地点」的条数。
		// 它不是新增行，而是对已有行的就地升级，所以不计入 Inserted。
		Promoted int `json:"promoted"`
	} `json:"totals"`
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// normalizeSlices 把 nil 切片换成空切片。
//
// Go 的 nil 切片会序列化成 JSON null，而 Report 的四个列表字段在前端
// （与 TS 类型声明）都是数组。旧批次存下的 report_json 里就是 null，
// 反序列化回来仍是 nil —— 前端直接读 .length 会抛
// "Cannot read properties of null (reading 'length')"。
//
// 新批次由 Preview 构造时就已经初始化过，这里同时兜住历史数据。
func (r *Report) normalizeSlices() {
	if r.Files == nil {
		r.Files = []FileStat{}
	}
	if r.Issues == nil {
		r.Issues = []csvin.Issue{}
	}
	if r.Conflicts == nil {
		r.Conflicts = []ConflictGroup{}
	}
	if r.Highlights == nil {
		r.Highlights = []string{}
	}
	if r.Blocking == nil {
		r.Blocking = []string{}
	}
}

// Decisions 是用户在裁决步骤做出的选择。
type Decisions struct {
	GlobalStrategy  string `json:"global_strategy"`
	WeatherStrategy string `json:"weather_strategy"`
	// WeatherOverrides 逐组覆盖：conflictKey -> "old"|"new"
	WeatherOverrides map[string]string `json:"weather_overrides"`
	// Force 为真时忽略预检的阻断项强行提交。UI 不暴露，只给明确知道后果的调用方。
	Force bool `json:"force,omitempty"`
}

// 状态常量。
const (
	StatusPreview    = "preview"
	StatusRunning    = "running"
	StatusSuccess    = "success"
	StatusFailed     = "failed"
	StatusRolledBack = "rolled_back"
)
