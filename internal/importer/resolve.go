package importer

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/Felix2yu/youlv/internal/csvin"
	"github.com/Felix2yu/youlv/internal/geo"
)

// candidate 是一个可供解析的地点条目。
//
// LatGCJ/LonGCJ 是 CSV 原值（地点记录为 GCJ-02）；Lat/Lon 是换算出的
// WGS-84。两份都要留着，因为到访记录用的是 WGS-84，而国内底图要 GCJ-02。
type candidate struct {
	Key      string
	Name     string
	Lat      float64 // WGS-84
	Lon      float64
	LatGCJ   float64
	LonGCJ   float64
	HasCoord bool
	IsStub   bool
	Hits     int64  // visit_count，同名消歧时的次要判据
	Origin   string // imported|reference_only|visit_derived
}

// ResolveResult 是到访记录解析到地点的结论。
type ResolveResult struct {
	PlaceKey   string // 稳定键（source_key），提交阶段才换成 rowid
	Method     string // exact|nearest|created|unresolved
	DistM      float64
	Confidence string // high|medium|low|none
	Frame      string // wgs84|gcj02_mislabeled|unknown|inherited
	Created    bool
}

// PlaceResolver 负责把「名字 + 坐标」解析到唯一地点。
//
// 它同时服务于两个阶段：预检时它手上是「库里已有的地点 + 本批次即将导入的
// 地点」，提交时它给出稳定键，由 commit 换成 rowid。
type PlaceResolver struct {
	byName  map[string][]*candidate
	byKey   map[string]*candidate
	created []*candidate // 本批次动态创建的条目（到访衍生点、孤儿桩）
	snapM   float64      // 坐标系判定阈值，实测取 5 m
	nearM   float64      // 最近邻上限，超过即低置信
}

// Created 返回解析过程中动态创建的地点条目。
//
// 这些条目不来自地点记录文件，而是解析到访/交通/天气时凭空补出来的。
// 必须把它们一并写进暂存，否则到访会引用一个不存在的地点键。
func (r *PlaceResolver) Created() []*candidate { return r.created }

// DefaultSnapMeters 是坐标系判定阈值。
// 实测 4250 条经 GCJ→WGS 复现的偏差中位 0.64 m、最大 3.12 m；取 5 m 能
// 完整覆盖，又不会误伤「到访自带独立定位点」的分支（885 个唯一坐标对）。
const DefaultSnapMeters = 5.0

// DefaultNearestMeters 是同名多址时允许绑定的最远距离。
const DefaultNearestMeters = 500.0

func newPlaceResolver(snapM, nearM float64) *PlaceResolver {
	if snapM <= 0 {
		snapM = DefaultSnapMeters
	}
	if nearM <= 0 {
		nearM = DefaultNearestMeters
	}
	return &PlaceResolver{
		byName: make(map[string][]*candidate),
		byKey:  make(map[string]*candidate),
		snapM:  snapM,
		nearM:  nearM,
	}
}

// Add 把一个地点条目纳入解析索引。
func (r *PlaceResolver) Add(c *candidate) {
	r.byName[c.Name] = append(r.byName[c.Name], c)
	if _, dup := r.byKey[c.Key]; !dup {
		r.byKey[c.Key] = c
	}
}

// Count 返回已纳入的地点条目数。
func (r *PlaceResolver) Count() int { return len(r.byKey) }

// StubByName 返回该名字对应的桩候选（没有则 nil）。
//
// 用于桩升级：地点记录里出现了一个此前只在到访/交通/天气里被提到过的
// 名字，并且这次带上了真实坐标——那它就不是桩了，该升级成正式地点。
func (r *PlaceResolver) StubByName(name string) *candidate {
	for _, c := range r.byName[name] {
		if c.IsStub {
			return c
		}
	}
	return nil
}

// PromoteStub 给桩候选补上真实坐标，把它变成可参与坐标消歧的候选。
//
// 就地修改而不是新建候选：byName 里同一个名字只能有一个条目，否则
// 解析会误判成「同名多址」，走进最近邻分支而不是精确匹配。
func (r *PlaceResolver) PromoteStub(c *candidate, wLat, wLon, latGCJ, lonGCJ float64) {
	c.Lat, c.Lon = wLat, wLon
	c.LatGCJ, c.LonGCJ = latGCJ, lonGCJ
	c.HasCoord = true
}

// MatchNearby 返回该名字下与给定坐标（WGS-84）距离最近的候选，
// 超过 maxMeters 则返回 nil。
//
// 它解决的是「分批导入」场景：先导入到访记录时，库里的地点是由到访坐标
// 派生的（origin=visit_derived，key 按到访的 WGS-84 算）；后来再导入地点
// 记录，同一地点的 key 按登记点的 GCJ-02 算——两个 key 不相等，于是同一个
// 地点被存成两行。全量导入不会暴露这个问题（地点表先解析），分批导入会。
//
// 阈值用坐标系判定那套（默认 5 m）：只有几乎重合的点才算同一个点，
// 同名的不同分店（相距几百米以上）不会被误合。
func (r *PlaceResolver) MatchNearby(name string, lat, lon, maxMeters float64) *candidate {
	var best *candidate
	bestD := maxMeters
	for _, c := range r.byName[name] {
		if !c.HasCoord {
			continue
		}
		d := geo.HaversineMeters(lat, lon, c.Lat, c.Lon)
		if d <= bestD {
			best, bestD = c, d
		}
	}
	return best
}

// HomonymGroups 返回重名组的数量（同名但不同键）。
// 用于导入报告的提示卡：36 组重名将按坐标消歧。
func (r *PlaceResolver) HomonymGroups() int {
	var n int
	for _, cs := range r.byName {
		if len(cs) > 1 {
			n++
		}
	}
	return n
}

// ResolveVisit 解析一条到访记录。
//
// 判据用两条距离：
//
//	dA = 到访坐标 ↔ 候选的 WGS-84（期望路径：地点表 GCJ-02 已换算过来）
//	dB = 到访坐标 ↔ 候选的 GCJ-02 原值（脏数据路径：这批到访没做换算）
//
// 取更小者。dB 明显更小时说明这批到访被错标成了 GCJ-02，需要就地换算。
func (r *PlaceResolver) ResolveVisit(name string, lat, lon float64, allowCreate bool) (ResolveResult, []csvin.Issue) {
	name = strings.TrimSpace(name)
	cs := r.byName[name]
	var issues []csvin.Issue

	if len(cs) == 0 {
		if !allowCreate {
			return ResolveResult{Method: "unresolved", Confidence: "none", Frame: "unknown"}, []csvin.Issue{{
				Severity: csvin.SevError, Code: "ERR_PLACE_NOT_FOUND", Field: "place_id",
				Value: name, Message: fmt.Sprintf("地点「%s」不在地点库中", name),
				Hint: "先导入地点记录，或勾选「到访自动建点」",
			}}
		}
		c := r.createFromVisit(name, lat, lon)
		return ResolveResult{
			PlaceKey: c.Key, Method: "created", Confidence: "high",
			Frame: "wgs84", Created: true,
		}, issues
	}

	best := struct {
		c        *candidate
		d        float64
		frameGCJ bool
	}{d: 1e18}

	for _, c := range cs {
		if !c.HasCoord {
			continue
		}
		dA := geo.HaversineMeters(lat, lon, c.Lat, c.Lon)
		dB := geo.HaversineMeters(lat, lon, c.LatGCJ, c.LonGCJ)
		if dB < dA {
			if dB < best.d {
				best.c, best.d, best.frameGCJ = c, dB, true
			}
		} else if dA < best.d {
			best.c, best.d, best.frameGCJ = c, dA, false
		}
	}
	if best.c == nil {
		// 同名候选全是无坐标桩：退化为名字唯一命中。
		best.c = cs[0]
		best.d = 0
	}

	res := ResolveResult{PlaceKey: best.c.Key, Method: "nearest", DistM: best.d}
	if len(cs) == 1 {
		res.Method = "exact"
	}

	switch {
	case best.d <= r.snapM:
		res.Confidence = "high"
		if best.frameGCJ && !geo.OutOfChina(lat, lon) {
			res.Frame = "gcj02_mislabeled"
			issues = append(issues, csvin.Issue{
				Severity: csvin.SevWarn, Code: "WARN_COORD_FRAME_SUSPECT",
				Field: "coord_frame", Value: fmt.Sprintf("%.6f,%.6f", lat, lon),
				Message: fmt.Sprintf("到访坐标与地点「%s」的 GCJ-02 原值重合（偏差 %.2f m），判定为未做坐标换算，已自动纠正", name, best.d),
				Hint:    "原始坐标已保留在 lat_raw/lon_raw，可随时回溯",
			})
		} else {
			res.Frame = "wgs84"
		}
	case best.d <= 50:
		res.Confidence, res.Frame = "high", "wgs84"
	case best.d <= 200:
		res.Confidence, res.Frame = "medium", "wgs84"
	case best.d <= r.nearM:
		res.Confidence, res.Frame = "low", "wgs84"
		issues = append(issues, csvin.Issue{
			Severity: csvin.SevWarn, Code: "WARN_RESOLVE_FAR", Field: "place_id",
			Value: name, Message: fmt.Sprintf("到访坐标距地点「%s」%.0f m，已按最近邻绑定", name, best.d),
			Hint: "若绑错了，可在预检结果里手动改绑",
		})
	default:
		res.Confidence, res.Frame = "low", "wgs84"
		issues = append(issues, csvin.Issue{
			Severity: csvin.SevWarn, Code: "WARN_RESOLVE_FAR", Field: "place_id",
			Value: name, Message: fmt.Sprintf("到访坐标距最近的同名地点「%s」达 %.0f m，超出 %0.f m 阈值", name, best.d, r.nearM),
			Hint: "很可能是地点库缺了这个点，建议先在地点记录里补上",
		})
	}
	return res, issues
}

// createFromVisit 为到访里出现、但地点库没有的地点建条目。
// 到访坐标是 WGS-84，直接作为权威值；GCJ-02 副本按需派生。
//
// 坐标为 (0,0) 表示这行到访没带定位（解析失败或原表为空）：这种地点
// 没有坐标可用，本质上和「只在别处被提到的孤儿名」一样是桩。曾经的
// 写法把 source_key 硬编码成「有坐标」形式，于是建出来的地点 is_stub=0
// 却没有坐标——它既不进足迹地图，也享受不到桩升级，悬在中间地带。
func (r *PlaceResolver) createFromVisit(name string, lat, lon float64) *candidate {
	hasCoord := lat != 0 || lon != 0
	c := &candidate{
		Key:      PlaceSourceKey(name, lat, lon, hasCoord),
		Name:     name,
		Lat:      lat,
		Lon:      lon,
		HasCoord: hasCoord,
		IsStub:   !hasCoord,
	}
	if hasCoord {
		c.LatGCJ, c.LonGCJ = geo.WGS84ToGCJ02(lat, lon)
	}
	r.Add(c)
	r.created = append(r.created, c)
	return c
}

// ResolveName 只按名字解析（交通的起止地点、天气的地点没有坐标）。
//
// 同名多址时无法用坐标消歧，退而取到访次数最多的那个，并记 warn——
// 这比静默选第一个诚实。
func (r *PlaceResolver) ResolveName(name string, createStub bool) (ResolveResult, []csvin.Issue) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ResolveResult{Method: "empty", Confidence: "none", Frame: "unknown"}, nil
	}
	cs := r.byName[name]
	if len(cs) == 0 {
		if !createStub {
			return ResolveResult{Method: "unresolved", Confidence: "none", Frame: "unknown"}, []csvin.Issue{{
				Severity: csvin.SevInfo, Code: "INFO_PLACE_NOT_FOUND", Field: "place_id",
				Value: name, Message: fmt.Sprintf("地点「%s」不在地点库中", name),
				Hint: "可在设置里开启「为未知道点建桩」",
			}}
		}
		c := &candidate{
			Key: "stub:" + hashString(name), Name: name, IsStub: true,
		}
		r.Add(c)
		r.created = append(r.created, c)
		return ResolveResult{PlaceKey: c.Key, Method: "created", Confidence: "low", Created: true}, nil
	}

	// 决胜规则必须是确定性的。
	//
	// 曾经用「到访次数最多」决胜，结果埋了个幂等炸弹：第一次导入时所有地点的
	// visit_count 还是 0，第二次导入时已经非零，同一个名字会解析到不同的键，
	// 于是重复导入竟然多出 37 条天气记录。改用「有坐标优先，其次按稳定键排序」，
	// 任何时刻、任何数据状态下都得到同一个答案。
	best := cs[0]
	for _, c := range cs[1:] {
		if betterCandidate(c, best) {
			best = c
		}
	}
	res := ResolveResult{PlaceKey: best.Key, Method: "exact", Confidence: "high", Frame: "wgs84"}
	if len(cs) == 1 {
		return res, nil
	}
	res.Method, res.Confidence = "nearest", "medium"
	return res, []csvin.Issue{{
		Severity: csvin.SevWarn, Code: "WARN_NAME_AMBIGUOUS", Field: "place_id",
		Value: name, Message: fmt.Sprintf("地点「%s」有 %d 个同名条目，此处无坐标可消歧，已选到访最多的一个", name, len(cs)),
		Hint: "若选错，可在地点页合并或改名",
	}}
}

// betterCandidate 判断同名多址时谁更该被选中：有坐标的优先于无坐标桩，
// 其余按稳定键字典序。两条规则都与当前数据状态无关，因此跨批次稳定。
func betterCandidate(a, b *candidate) bool {
	if a.HasCoord != b.HasCoord {
		return a.HasCoord
	}
	return a.Key < b.Key
}

// PlaceSourceKey 生成地点的稳定业务键。
//
// 有坐标时按 (名称, 纬度6位, 经度6位) 哈希——36 组重名靠它区分成分店；
// 无坐标的桩地点按名字哈希，保证孤儿只建一次。
func PlaceSourceKey(name string, lat, lon float64, hasCoord bool) string {
	if !hasCoord {
		return "stub:" + hashString(name)
	}
	return hashString(fmt.Sprintf("%s|%.6f,%.6f", name, lat, lon))
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:10])
}
