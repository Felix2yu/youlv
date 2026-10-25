package importer

import (
	"testing"
)

// 真实导出里的形态：同一个连锁品牌在上海、苏州各一家，相距约 100 km。
// 交通与天气只写名字，光看名字无从判断是哪一家。
const (
	shLat, shLon = 31.238430, 121.471470 // 上海黄浦
	szLat, szLon = 31.320530, 120.624590 // 苏州姑苏
)

func twoBranches() *PlaceResolver {
	r := newPlaceResolver(0, 0)
	r.Add(&candidate{Key: "k-sh", Name: "塔斯汀中国汉堡", Lat: shLat, Lon: shLon, HasCoord: true})
	r.Add(&candidate{Key: "k-sz", Name: "塔斯汀中国汉堡", Lat: szLat, Lon: szLon, HasCoord: true})
	return r
}

// 没有时间证据时必须退化成原来的确定性规则，并且仍然告警 ——
// 这条路径保证了「无锚点」情形下的幂等性与诚实。
func TestResolveNameAtWithoutAnchorDegrades(t *testing.T) {
	r := twoBranches()
	res, issues := r.ResolveNameAt("塔斯汀中国汉堡", nil, true)
	if res.PlaceKey == "" {
		t.Fatal("无锚点时应仍能解析出一个地点")
	}
	if res.Method != "nearest" {
		t.Fatalf("method = %s, want nearest（退化路径）", res.Method)
	}
	if len(issues) != 1 || issues[0].Code != "WARN_NAME_AMBIGUOUS" {
		t.Fatalf("无锚点必须报 WARN_NAME_AMBIGUOUS，得到 %+v", issues)
	}
	if issues[0].Message == "" || issues[0].Hint == "" {
		t.Fatal("告警必须带可读的 message 与 hint")
	}
}

// 锚点本身就解析到了某条同名地点：直接用，不再报歧义。
func TestResolveNameAtUsesAnchorPlaceKey(t *testing.T) {
	r := twoBranches()
	a := &visitAnchor{
		PlaceKey: "k-sz", Name: "塔斯汀中国汉堡",
		Lat: szLat, Lon: szLon, HasCoord: true, ArrivedAt: 1_700_000_000,
	}
	res, issues := r.ResolveNameAt("塔斯汀中国汉堡", a, true)
	if res.PlaceKey != "k-sz" {
		t.Fatalf("绑定到 %s，应绑定锚点所在的苏州店", res.PlaceKey)
	}
	if res.Method != "temporal" {
		t.Fatalf("method = %s, want temporal", res.Method)
	}
	if len(issues) != 0 {
		t.Fatalf("时间已能确定归属，不该再报歧义：%+v", issues)
	}
}

// 锚点只有坐标（到访地点名与交通写的不完全一致时）：取最近的一家。
func TestResolveNameAtUsesAnchorCoord(t *testing.T) {
	r := twoBranches()
	a := &visitAnchor{Lat: szLat + 0.0005, Lon: szLon, HasCoord: true} // 约 55 m
	res, issues := r.ResolveNameAt("塔斯汀中国汉堡", a, true)
	if res.PlaceKey != "k-sz" {
		t.Fatalf("绑定到 %s，应按坐标绑定苏州店", res.PlaceKey)
	}
	if res.Method != "temporal_nearest" {
		t.Fatalf("method = %s, want temporal_nearest", res.Method)
	}
	if len(issues) != 0 {
		t.Fatalf("不该报歧义：%+v", issues)
	}
}

// 锚点离所有同名候选都远（例如到访记录坏了）：不敢断言，退回确定性规则并告警。
func TestResolveNameAtFarAnchorDegrades(t *testing.T) {
	r := twoBranches()
	a := &visitAnchor{Lat: 30.0, Lon: 110.0, HasCoord: true}
	res, issues := r.ResolveNameAt("塔斯汀中国汉堡", a, true)
	if res.Method != "nearest" {
		t.Fatalf("method = %s, want nearest（锚点太远）", res.Method)
	}
	if len(issues) != 1 || issues[0].Code != "WARN_NAME_AMBIGUOUS" {
		t.Fatalf("应退回告警，得到 %+v", issues)
	}
}

// 名字唯一时行为不变：不该因为引入了时间锚点就改变普通地点的解析结果。
func TestResolveNameAtUniqueNameUnchanged(t *testing.T) {
	r := newPlaceResolver(0, 0)
	r.Add(&candidate{Key: "k1", Name: "柳浪闻莺", Lat: 30.24, Lon: 120.15, HasCoord: true})
	a := &visitAnchor{PlaceKey: "k-sh", Lat: shLat, Lon: shLon, HasCoord: true}
	res, issues := r.ResolveNameAt("柳浪闻莺", a, true)
	if res.PlaceKey != "k1" || res.Method != "exact" || len(issues) != 0 {
		t.Fatalf("唯一名字的解析结果被改变了: %+v / %+v", res, issues)
	}
}

func TestTimeIndexWindowAndSemantics(t *testing.T) {
	const base int64 = 1_700_000_000
	idx := newTimeIndex([]visitAnchor{
		{PlaceKey: "a", Name: "柳浪闻莺", HasCoord: true, ArrivedAt: base, DepartedAt: base + 900},
		{PlaceKey: "b", Name: "杭州站", HasCoord: true, ArrivedAt: base + 5400, DepartedAt: base + 7200},
	})

	if a := idx.arriveAnchor(base + 60); a == nil || a.PlaceKey != "a" {
		t.Fatalf("到达锚点应命中 a，得到 %+v", a)
	}
	if a := idx.departAnchor(base + 900); a == nil || a.PlaceKey != "a" {
		t.Fatalf("离开锚点应命中 a，得到 %+v", a)
	}
	// 窗口边界用单点索引测，避免被相邻点干扰。
	solo := newTimeIndex([]visitAnchor{{PlaceKey: "s", ArrivedAt: base}})
	if a := solo.arriveAnchor(base + anchorWindow - 1); a == nil {
		t.Fatal("窗口内最后一个应命中")
	}
	if a := solo.arriveAnchor(base + anchorWindow + 1); a != nil {
		t.Fatalf("超出 %ds 窗口不该命中，得到 %+v", anchorWindow, a)
	}
	// 离开时刻缺失时退回按到达时刻匹配，而不是直接放弃。
	idx2 := newTimeIndex([]visitAnchor{{PlaceKey: "c", ArrivedAt: base}})
	if a := idx2.departAnchor(base + 30); a == nil || a.PlaceKey != "c" {
		t.Fatalf("离开时刻缺失应退回到达锚点，得到 %+v", a)
	}
	// 空索引不能 panic。
	if a := newTimeIndex(nil).arriveAnchor(base); a != nil {
		t.Fatalf("空索引应返回 nil，得到 %+v", a)
	}
}
