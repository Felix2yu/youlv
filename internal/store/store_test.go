package store

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/youlv/internal/testdb"
)

// 读侧查询的回归测试。
//
// 这些用例守的是「查出来的东西对不对」，而 importer 的测试守的是「写进去的
// 东西对不对」。缺了这一半，口径变化（比如改 JOIN 类型）就只能靠肉眼看界面。

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(testdb.New(t), time.FixedZone("Asia/Shanghai", 8*3600))
}

// addPlace 插一个地点，返回其 id。
func addPlace(t *testing.T, s *Store, name, city string, lat, lon float64) int64 {
	t.Helper()
	res, err := s.DB.ExecContext(context.Background(), `
		INSERT INTO places(name, name_normalized, display_name, source_key,
			city, lat, lon, lat_gcj, lon_gcj, origin, is_stub)
		VALUES(?,?,?,?,?,?,?,?,?,'imported',0)`,
		name, name, name, "k-"+name, city, lat, lon, lat, lon)
	if err != nil {
		t.Fatalf("插入地点: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addVisit(t *testing.T, s *Store, placeID int64, name string, at int64, date string, lat, lon float64) {
	t.Helper()
	// 每一列都显式给值：SQLite 允许值比列少（剩下的取默认值），
	// 写漏了不会报错，只会悄悄把 local_date 之类留成空串、然后查不到。
	if _, err := s.DB.ExecContext(context.Background(), `
		INSERT INTO visits(place_id, place_name_raw, activity_raw,
			arrived_at, departed_at, duration_s, arrived_local, local_date,
			local_month, local_year, local_hour, local_dow, lat, lon,
			lat_gcj, lon_gcj, dedup_key)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		placeID, name, "", at, at+3600, 3600, "2026-10-23 12:00", date,
		date[:7], 2026, 12, 5, lat, lon, lat, lon, "dk-"+name+date+":"+time.Unix(at, 0).String()); err != nil {
		t.Fatalf("插入到访: %v", err)
	}
}

func TestTimelineDayOnlyReturnsThatDay(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	p := addPlace(t, s, "示例楼", "城市-0001", 31.2, 121.4)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Unix()
	addVisit(t, s, p, "示例楼", base, "2026-10-23", 31.2, 121.4)
	addVisit(t, s, p, "示例楼", base+86400, "2026-10-24", 31.2, 121.4)

	list, err := s.TimelineDay(ctx, "2026-10-23", "wgs84")
	if err != nil {
		t.Fatalf("TimelineDay: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("取到 %d 条，应为 1（只要 2026-10-23 的）", len(list))
	}
	if list[0].LocalDate != "2026-10-23" {
		t.Errorf("local_date = %s", list[0].LocalDate)
	}
}

// frame=gcj02 时主坐标列里装的是 GCJ-02，所以权威的 WGS-84 副本必须同时给出，
// 否则客户端拿着火星坐标当 WGS-84 用（静默偏移数百米）。
func TestTimelineReturnsBothCoordFrames(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	p := addPlace(t, s, "示例楼", "城市-0001", 31.2, 121.4)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Unix()
	addVisit(t, s, p, "示例楼", base, "2026-10-23", 31.2, 121.4)

	list, err := s.TimelineDay(ctx, "2026-10-23", "gcj02")
	if err != nil {
		t.Fatalf("TimelineDay: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("取到 %d 条，应为 1", len(list))
	}
	v := list[0]
	if v.Lat == nil || v.LatGCJ == nil {
		t.Fatalf("两套坐标都要有：lat=%v lat_gcj=%v", v.Lat, v.LatGCJ)
	}
	if *v.Lat != 31.2 || *v.LatGCJ != 31.2 {
		t.Errorf("lat=%.6f lat_gcj=%.6f，都应为 31.2", *v.Lat, *v.LatGCJ)
	}
}

// 未解析的到访（place_id=0）必须还在城市护照里，只是归到「未标注」。
func TestStatsCitiesKeepsUnresolvedVisits(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	p := addPlace(t, s, "示例楼", "城市-0001", 31.2, 121.4)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Unix()
	addVisit(t, s, p, "示例楼", base, "2026-10-23", 31.2, 121.4)
	// place_id=0：地点库里没有它，但到访是真的发生了。
	addVisit(t, s, 0, "没解析上的地方", base+3600, "2026-10-23", 0, 0)

	cities, err := s.StatsCities(ctx)
	if err != nil {
		t.Fatalf("StatsCities: %v", err)
	}
	var total, unresolved int
	for _, c := range cities {
		total += c.Visits
		if c.City == "未标注" {
			unresolved = c.Visits
		}
	}
	if total != 2 {
		t.Errorf("城市护照共 %d 次到访，应为 2（INNER JOIN 会漏掉未解析的那条）", total)
	}
	if unresolved != 1 {
		t.Errorf("「未标注」有 %d 次，应为 1", unresolved)
	}
}

func TestMapPointsFiltersByBBox(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sh := addPlace(t, s, "上海的点", "上海", 31.2, 121.4)
	bj := addPlace(t, s, "北京的点", "北京", 39.9, 116.4)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Unix()
	addVisit(t, s, sh, "上海的点", base, "2026-10-23", 31.2, 121.4)
	addVisit(t, s, bj, "北京的点", base, "2026-10-23", 39.9, 116.4)

	pts, err := s.MapPoints(ctx, BBox{West: 120, South: 30, East: 122, North: 32})
	if err != nil {
		t.Fatalf("MapPoints: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("bbox 内取到 %d 个点，应为 1（只有上海那个）", len(pts))
	}
	if pts[0].Name != "上海的点" {
		t.Errorf("取到 %s", pts[0].Name)
	}
}
