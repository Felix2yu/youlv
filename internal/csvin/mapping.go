package csvin

import (
	"sort"
	"strings"
)

// 四种文件的列定义。Required 缺一列即阻断，Optional 缺了只记 warn。
//
// 列名一律用 Rond 导出的中文原文作规范名（canonical），别名表负责把
// 各种写法收敛到它上面。
var schema = map[Kind]struct {
	Required []string
	Optional []string
}{
	KindPlaces: {
		Required: []string{"名称", "纬度", "经度"},
		Optional: []string{"评分", "活跃范围半径", "国家或地区", "行政区域",
			"次级行政区域", "城市/地区", "区", "街道", "门牌号"},
	},
	KindVisits: {
		Required: []string{"活动", "地点", "抵达时刻", "离开时刻"},
		Optional: []string{"时区", "纬度", "经度", "标签", "备注"},
	},
	KindTrips: {
		Required: []string{"类型", "从", "至", "时长 (s)"},
		Optional: []string{"时区", "出发地", "目的地", "备注"},
	},
	KindWeather: {
		Required: []string{"日期", "地点", "天气状况"},
		Optional: []string{"温度 (°C)", "体感温度 (°C)", "露点 (°C)", "湿度",
			"降水类型", "降水量 (mm)", "降水概率", "紫外线指数", "紫外线等级",
			"风向", "风速 (m/s)", "能见度 (km)"},
	},
}

// 别名表。键是归一化后的写法，值是规范名。
// 覆盖英文列名、无单位写法、全角括号等常见变体。
var aliases = map[string]string{
	"lat": "纬度", "latitude": "纬度", "纬度°": "纬度",
	"lon": "经度", "lng": "经度", "long": "经度", "longitude": "经度", "经度°": "经度",
	"name": "名称", "placename": "名称", "地点名称": "名称",
	"place": "地点", "location": "地点",
	"arrived": "抵达时刻", "arrivedat": "抵达时刻", "到达时间": "抵达时刻", "抵达": "抵达时刻",
	"departed": "离开时刻", "leftat": "离开时刻", "离开时间": "离开时刻", "离开": "离开时刻",
	"duration": "时长 (s)", "时长s": "时长 (s)", "时长(秒)": "时长 (s)",
	"持续时间": "时长 (s)", "时长": "时长 (s)",
	"temp": "温度 (°C)", "温度(c)": "温度 (°C)", "气温": "温度 (°C)", "温度": "温度 (°C)",
	"体感温度(c)": "体感温度 (°C)", "体感": "体感温度 (°C)",
	"露点(c)": "露点 (°C)", "露点": "露点 (°C)",
	"天气": "天气状况", "condition": "天气状况", "weather": "天气状况",
	"降水(mm)": "降水量 (mm)", "降水量": "降水量 (mm)",
	"降水概率(%)": "降水概率",
	"uv": "紫外线指数", "紫外线": "紫外线指数",
	"风速": "风速 (m/s)", "风速(m/s)": "风速 (m/s)",
	"能见度": "能见度 (km)", "能见度(km)": "能见度 (km)",
	"半径": "活跃范围半径", "radius": "活跃范围半径",
	"国": "国家或地区", "country": "国家或地区", "国家": "国家或地区",
	"省": "行政区域", "province": "行政区域",
	"市": "城市/地区", "city": "城市/地区", "城市": "城市/地区",
	"区/县": "区", "district": "区",
	"tags": "标签", "标签s": "标签",
	"note": "备注", "备注说明": "备注", "notes": "备注",
	"type": "类型", "mode": "类型", "方式": "类型", "交通方式": "类型",
	"start": "从", "from": "从", "出发时间": "从",
	"end": "至", "to": "至", "到达": "至",
	"出发地点": "出发地", "origin": "出发地",
	"目的地点": "目的地", "destination": "目的地",
	"date": "日期", "时间": "日期",
	"activity": "活动", "category": "活动",
	"timezone": "时区", "tz": "时区",
	"街道地址": "街道", "street": "街道",
	"门牌": "门牌号", "号": "门牌号",
	"次级行政区域": "次级行政区域",
}

// normKey 把表头/别名收敛成可比较的键：NFKC 之后去掉全部空白、
// 全角括号转半角、转小写。
//
// 去空白是刻意的：Rond 的列是「温度 (°C)」「时长 (s)」这种带空格的写法，
// 而别名表里既有「温度(c)」也有「温度(°c)」，抹掉空白才能让两边对齐。
func normKey(s string) string {
	s = strings.NewReplacer("（", "(", "）", ")", "，", ",").Replace(s)
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '　' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// canonical 返回该列头对应的规范名；认不出来就返回空串。
func canonical(header string) string {
	n := normKey(header)
	if c, ok := aliases[n]; ok {
		return c
	}
	// 别名表里没有时，看它本身是不是某种文件的规范名。
	for _, def := range schema {
		for _, c := range def.Required {
			if normKey(c) == n {
				return c
			}
		}
		for _, c := range def.Optional {
			if normKey(c) == n {
				return c
			}
		}
	}
	return ""
}

// ColumnIndex 是按规范名寻址的列索引。
type ColumnIndex map[string]int

// Get 取出该规范名对应的原始值；列不存在或越界时返回空串。
func (ci ColumnIndex) Get(rec []string, name string) string {
	i, ok := ci[name]
	if !ok || i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}

// Has 报告该规范列是否存在。
func (ci ColumnIndex) Has(name string) bool { _, ok := ci[name]; return ok }

// Detect 依据表头判断文件类型。靠表头而非文件名——用户从 iCloud 或邮件
// 里存下来的文件经常被改名。
type DetectResult struct {
	Kind        Kind
	Confidence  float64 // 必需列命中率
	Matched     []string
	MissingReq  []string
	MissingOpt  []string
	Extra       []string // 认识但不属于该类型的列，以及完全不认识的列
	Mapping     ColumnIndex
	Ambiguous   bool   // 与另一类型的命中率并列最高
	AlsoMatches []Kind // 并列或次高的候选
}

func Detect(header []string) (*DetectResult, error) {
	if len(header) == 0 {
		return nil, ErrNoHeader
	}
	seen := make(map[string]int, len(header))
	for i, h := range header {
		if c := canonical(h); c != "" {
			if _, dup := seen[c]; !dup {
				seen[c] = i
			}
		}
	}

	type cand struct {
		kind  Kind
		hit   float64
		missR []string
		missO []string
	}
	var cands []cand
	for _, k := range []Kind{KindPlaces, KindVisits, KindTrips, KindWeather} {
		def := schema[k]
		var missR []string
		for _, c := range def.Required {
			if _, ok := seen[c]; !ok {
				missR = append(missR, c)
			}
		}
		var missO []string
		for _, c := range def.Optional {
			if _, ok := seen[c]; !ok {
				missO = append(missO, c)
			}
		}
		hit := float64(len(def.Required)-len(missR)) / float64(len(def.Required))
		cands = append(cands, cand{k, hit, missR, missO})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].hit > cands[j].hit })

	best := cands[0]
	def := schema[best.kind]
	res := &DetectResult{
		Kind:       best.kind,
		Confidence: best.hit,
		MissingReq: best.missR,
		MissingOpt: best.missO,
		Mapping:    make(ColumnIndex, len(def.Required)+len(def.Optional)),
	}
	for _, c := range append(append([]string{}, def.Required...), def.Optional...) {
		if i, ok := seen[c]; ok {
			res.Mapping[c] = i
			res.Matched = append(res.Matched, c)
		}
	}
	for _, h := range header {
		if canonical(h) == "" {
			res.Extra = append(res.Extra, h)
		}
	}
	for _, c := range cands[1:] {
		if c.hit == best.hit && c.hit > 0 {
			res.Ambiguous = true
			res.AlsoMatches = append(res.AlsoMatches, c.kind)
		}
	}
	return res, nil
}

// MinDetectConfidence 是「可自动确定类型」的阈值。
// 低于它必须让用户在界面上手动指定文件类型。
const MinDetectConfidence = 1.0
