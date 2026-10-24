package importer

import "github.com/yufei/youlv/internal/csvin"

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
	Kind     csvin.Kind `json:"kind"`
	Label    string     `json:"label"`
	File     string     `json:"file"`
	SHA256   string     `json:"sha256"`
	Rows     int        `json:"rows"`
	Ready    int        `json:"ready"`    // 通过校验、等待写入
	Failed   int        `json:"failed"`   // error 级，不会写入
	Warned   int        `json:"warned"`   // 带 warn 但仍会写入
	DupSkip  int        `json:"dup_skip"` // 与库内已有记录重复
	Inserted int        `json:"inserted"` // 执行后回填
	Skipped  int        `json:"skipped"`
	Detected string     `json:"detected"` // 自动识别的类型
	Confidence float64  `json:"confidence"`
	Missing  []string   `json:"missing,omitempty"`
	Extra    []string   `json:"extra,omitempty"`
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
	BatchNo    string     `json:"batch_no"`
	Status     string     `json:"status"`
	Options    Options    `json:"options"`
	Files      []FileStat `json:"files"`
	Issues     []csvin.Issue `json:"issues"`
	Conflicts  []ConflictGroup `json:"conflicts"`
	Highlights []string   `json:"highlights"`
	Blocking   []string   `json:"blocking"`
	Totals     struct {
		Rows     int `json:"rows"`
		Ready    int `json:"ready"`
		Failed   int `json:"failed"`
		DupSkip  int `json:"dup_skip"`
		Inserted int `json:"inserted"`
		Skipped  int `json:"skipped"`
	} `json:"totals"`
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// Decisions 是用户在裁决步骤做出的选择。
type Decisions struct {
	GlobalStrategy  string            `json:"global_strategy"`
	WeatherStrategy string            `json:"weather_strategy"`
	// WeatherOverrides 逐组覆盖：conflictKey -> "old"|"new"
	WeatherOverrides map[string]string `json:"weather_overrides"`
}

// 状态常量。
const (
	StatusPreview    = "preview"
	StatusRunning    = "running"
	StatusSuccess    = "success"
	StatusFailed     = "failed"
	StatusRolledBack = "rolled_back"
)
