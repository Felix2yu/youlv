package importer

import (
	"context"
	"fmt"
	"sort"
)

// 交通与天气记录只带地点名，不带坐标。同名多址时（连锁分店、同名景点），
// 光看名字无从判断该绑哪一家。但它们都带时间，而同一时刻的到访记录带
// 真实坐标 —— 「那一刻人在哪」比「名字像谁」确定得多。
//
// 实测依据（真实导出，2026-10 数据）：
//
//	04-20 18:15 柳浪闻莺(杭州) → 塔斯汀，出发时刻与该到访的 departed_at 完全相等；
//	到达 18:38 与杭州那家塔斯汀的 arrived_at 完全相等。
//	04-25 11:05 苏州市政府 → 塔斯汀，同样精确对齐苏州市政府与苏州那家店的到访。
//
// 也就是说 Rond 把同一段停留拆成了「到访 + 交通」两半，时间戳是共用的。
// 这条对齐关系就是消歧的锚点。

// visitAnchor 是一条「某一时刻人在哪儿」的证据，来自到访记录。
type visitAnchor struct {
	PlaceKey   string
	Name       string
	Lat        float64
	Lon        float64
	HasCoord   bool
	ArrivedAt  int64
	DepartedAt int64 // 0 表示这条到访没有离开时刻
}

// anchorWindow 是锚点与目标时刻允许的最大间隔（秒）。
//
// 取 1 小时：Rond 的交通与到访时刻是同一秒，容差只需覆盖导出截断和手动
// 补录；放得太宽会把「同一天在另一座城市」的到访误当成锚点，反而更糟。
const anchorWindow int64 = 3600

// timeIndex 按到达/离开两个维度给到访建了排序索引。
type timeIndex struct {
	byArrival   []visitAnchor
	byDeparture []visitAnchor
}

// newTimeIndex 构造索引。points 可为 nil —— 库里还没有到访时索引为空，
// 查询一律返回 nil，调用方自然退化成按名字消歧。
func newTimeIndex(points []visitAnchor) *timeIndex {
	arr := make([]visitAnchor, len(points))
	copy(arr, points)
	sort.Slice(arr, func(i, j int) bool { return arr[i].ArrivedAt < arr[j].ArrivedAt })

	dep := make([]visitAnchor, 0, len(points))
	for _, p := range points {
		if p.DepartedAt > 0 {
			dep = append(dep, p)
		}
	}
	sort.Slice(dep, func(i, j int) bool { return dep[i].DepartedAt < dep[j].DepartedAt })
	return &timeIndex{byArrival: arr, byDeparture: dep}
}

// nearestBy 在以 key 排序的切片里找 key 距 ts 最近且落在 anchorWindow 内的点。
func nearestBy(points []visitAnchor, ts int64, key func(visitAnchor) int64) *visitAnchor {
	if len(points) == 0 {
		return nil
	}
	lo := sort.Search(len(points), func(i int) bool { return key(points[i]) >= ts-anchorWindow })
	var best *visitAnchor
	var bestD int64
	for i := lo; i < len(points); i++ {
		d := key(points[i]) - ts
		if d > anchorWindow {
			break
		}
		if d < 0 {
			d = -d
		}
		if best == nil || d < bestD {
			best, bestD = &points[i], d
		}
	}
	return best
}

// arriveAnchor 返回「ts 时刻刚到达/正在某处」的锚点。
//
// 交通的「至」与天气的整点都用它：那正是人已经到那儿、或正待在那儿的时刻。
func (t *timeIndex) arriveAnchor(ts int64) *visitAnchor {
	if t == nil {
		return nil
	}
	return nearestBy(t.byArrival, ts, func(a visitAnchor) int64 { return a.ArrivedAt })
}

// departAnchor 返回「ts 时刻正从某处离开」的锚点。
//
// 交通的「从」用它。没有离开时刻的到访（停留不足一条记录、或导出为 NULL）
// 退回到按到达时刻匹配 —— 比没有锚点强。
func (t *timeIndex) departAnchor(ts int64) *visitAnchor {
	if t == nil {
		return nil
	}
	if a := nearestBy(t.byDeparture, ts, func(a visitAnchor) int64 { return a.DepartedAt }); a != nil {
		return a
	}
	return t.arriveAnchor(ts)
}

// anchorsFromStaged 把本批次刚解析出的到访转成锚点。
//
// 必须带上它们：全量导入时交通/天气与到访在同一批里，库里还什么都没有，
// 只用库内到访会让新库的第一次导入完全没有锚点可用。
func anchorsFromStaged(visits []stagedVisit) []visitAnchor {
	out := make([]visitAnchor, 0, len(visits))
	for _, v := range visits {
		a := visitAnchor{
			PlaceKey:  v.PlaceKey,
			Name:      v.PlaceNameRaw,
			ArrivedAt: v.ArrivedAt,
		}
		if v.DepartedAt != nil {
			a.DepartedAt = *v.DepartedAt
		}
		if v.Lat != nil && v.Lon != nil {
			a.Lat, a.Lon, a.HasCoord = *v.Lat, *v.Lon, true
		}
		out = append(out, a)
	}
	return out
}

// loadVisitAnchors 读取库内已有到访的时间与坐标。
//
// 增量导入交通/天气（不带到访文件）时，锚点只能来自这里。
func (s *Service) loadVisitAnchors(ctx context.Context) ([]visitAnchor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(p.source_key,''), COALESCE(p.name,''),
		       COALESCE(v.lat,0), COALESCE(v.lon,0),
		       v.arrived_at, COALESCE(v.departed_at,0)
		FROM visits v
		LEFT JOIN places p ON p.id = v.place_id`)
	if err != nil {
		return nil, fmt.Errorf("读取已有到访的时刻失败: %w", err)
	}
	defer rows.Close()

	var out []visitAnchor
	for rows.Next() {
		var a visitAnchor
		if err := rows.Scan(&a.PlaceKey, &a.Name, &a.Lat, &a.Lon, &a.ArrivedAt, &a.DepartedAt); err != nil {
			return nil, fmt.Errorf("扫描到访时刻失败: %w", err)
		}
		a.HasCoord = a.Lat != 0 || a.Lon != 0
		out = append(out, a)
	}
	return out, rows.Err()
}
