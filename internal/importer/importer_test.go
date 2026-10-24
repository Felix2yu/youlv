package importer

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yufei/youlv/internal/csvin"
	"github.com/yufei/youlv/internal/geo"
	"github.com/yufei/youlv/internal/testdb"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	// 测试里显式构造时区，不依赖宿主 TZ。
	return New(testdb.New(t), t.TempDir(), time.FixedZone("Asia/Shanghai", 8*3600))
}

func countOf(t *testing.T, s *Service, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("统计 %s: %v", table, err)
	}
	return n
}

// ---------- 坐标系与重名消歧（阶段 4 的核心）----------

func TestResolveHomonymPicksNearest(t *testing.T) {
	r := newPlaceResolver(0, 0)
	// 三家同名分店：这是真实导出里普遍存在的形态（几十组重名）。
	// 坐标取自仓库内的脱敏样本（testdata/rond-export）。
	branches := []struct {
		gcjLat, gcjLon float64
	}{
		{32.521260, 118.714897}, // 场所-0943：GCJ-02
		{32.509339, 118.625336}, // 场所-0813：GCJ-02
		{32.465148, 118.631096}, // 场所-0611：GCJ-02
	}
	for i, b := range branches {
		wLat, wLon := geo.GCJ02ToWGS84(b.gcjLat, b.gcjLon)
		r.Add(&candidate{
			Key: fmt.Sprintf("k%d", i), Name: "同名分店",
			Lat: wLat, Lon: wLon, LatGCJ: b.gcjLat, LonGCJ: b.gcjLon, HasCoord: true,
		})
	}
	// 取第二家的 WGS-84 坐标去问，必须命中第二家。
	wantLat, wantLon := geo.GCJ02ToWGS84(branches[1].gcjLat, branches[1].gcjLon)
	res, _ := r.ResolveVisit("同名分店", wantLat, wantLon, true)
	if res.Method != "nearest" {
		t.Fatalf("method = %s, want nearest", res.Method)
	}
	if res.DistM > 1 {
		t.Fatalf("命中距离 %.2f m，应接近 0（重名未正确消歧）", res.DistM)
	}
	if res.Frame != "wgs84" {
		t.Fatalf("frame = %s, want wgs84", res.Frame)
	}
	wantKey := r.byName["同名分店"][1].Key
	if res.PlaceKey != wantKey {
		t.Fatalf("绑定到了错误的分店")
	}
}

// TestResolveDetectsUnconvertedFrame 守住「30 条疑似未变换」的判定。
// 输入恰好等于地点表的 GCJ-02 原值时，必须识别出来并就地纠正。
func TestResolveDetectsUnconvertedFrame(t *testing.T) {
	r := newPlaceResolver(0, 0)
	const gcjLat, gcjLon = 32.499758, 118.585294
	wLat, wLon := geo.GCJ02ToWGS84(gcjLat, gcjLon)
	r.Add(&candidate{Key: "k", Name: "示例楼", Lat: wLat, Lon: wLon,
		LatGCJ: gcjLat, LonGCJ: gcjLon, HasCoord: true})

	// 期望路径：传入 WGS-84。
	res, issues := r.ResolveVisit("示例楼", wLat, wLon, true)
	if res.Frame != "wgs84" || len(issues) != 0 {
		t.Fatalf("WGS-84 输入应判为 wgs84，实际 %s，issues=%d", res.Frame, len(issues))
	}

	// 脏数据路径：传入 GCJ-02 原值。
	res, issues = r.ResolveVisit("示例楼", gcjLat, gcjLon, true)
	if res.Frame != "gcj02_mislabeled" {
		t.Fatalf("GCJ-02 输入应判为 gcj02_mislabeled，实际 %s", res.Frame)
	}
	if len(issues) != 1 || issues[0].Code != "WARN_COORD_FRAME_SUSPECT" {
		t.Fatalf("应产生一条 WARN_COORD_FRAME_SUSPECT，实际 %+v", issues)
	}
}

func TestResolveOverseasNoFalsePositive(t *testing.T) {
	r := newPlaceResolver(0, 0)
	// 境外点变换恒等，dA == dB，不能被误判成脏数据。
	const lat, lon = 35.6812, 139.7671
	r.Add(&candidate{Key: "k", Name: "东京", Lat: lat, Lon: lon,
		LatGCJ: lat, LonGCJ: lon, HasCoord: true})
	res, issues := r.ResolveVisit("东京", lat, lon, true)
	if res.Frame != "wgs84" {
		t.Fatalf("境外点应判为 wgs84，实际 %s", res.Frame)
	}
	if len(issues) != 0 {
		t.Fatalf("境外点不应产生告警，实际 %+v", issues)
	}
}

func TestResolveUnresolvedAndCreated(t *testing.T) {
	r := newPlaceResolver(0, 0)
	res, issues := r.ResolveVisit("不存在的地点", 31.1, 120.1, false)
	if res.Method != "unresolved" || len(issues) != 1 {
		t.Fatalf("禁建桩时应为 unresolved + 1 条 error，实际 %s / %d", res.Method, len(issues))
	}
	res, _ = r.ResolveVisit("不存在的地点", 31.1, 120.1, true)
	if res.Method != "created" || !res.Created {
		t.Fatalf("允许建桩时应为 created，实际 %s", res.Method)
	}
	if r.Count() != 1 {
		t.Fatalf("应只建了一个地点，实际 %d", r.Count())
	}
}

func TestStubKeyStability(t *testing.T) {
	// 孤儿地点按名字建桩，同名必须收敛到同一个键，否则会重复建桩。
	a := PlaceSourceKey("无名镇", 0, 0, false)
	b := PlaceSourceKey("无名镇", 0, 0, false)
	if a != b {
		t.Fatalf("同名桩键不一致: %s vs %s", a, b)
	}
	c := PlaceSourceKey("无名镇", 31.1, 120.1, true)
	if c == a {
		t.Fatal("有坐标的地点不应与桩同键")
	}
}

// ---------- 导入管线 ----------

const samplePlaces = "名称,纬度,经度,评分,活跃范围半径,国家或地区,行政区域,次级行政区域,城市/地区,区,街道,门牌号\n" +
	"场所-0943,32.521260,118.714897,0.0,125.0,CN,省份-0001,,城市-0001,辖区-0001,街道-0001,\n" +
	"场所-0813,32.509339,118.625336,0.0,300.0,CN,省份-0001,,城市-0001,辖区-0001,街道-0002,\n"

const sampleVisits = "活动,地点,抵达时刻,离开时刻,时区,纬度,经度,标签,备注\n" +
	"家,场所-0943,2026-10-22T23:25:43+0800,2026-10-23T08:56:47+0800,Asia/Shanghai,32.523343,118.709664,,\n" +
	"工作,场所-0813,2026-10-23T09:09:19+0800,2026-10-23T12:49:27+0800,Asia/Shanghai,32.511572,118.620275,,\n" +
	// 时间倒挂：离开早于抵达。
	"工作,场所-0813,2026-10-23T14:11:49+0800,2026-10-22T14:11:49+0800,Asia/Shanghai,32.511572,118.620275,,\n"

func upload(name, body string) UploadedFile {
	return UploadedFile{Name: name, Size: int64(len(body)), Content: []byte(body)}
}

func TestPreviewDoesNotTouchBusinessTables(t *testing.T) {
	s := newSvc(t)
	rep, err := s.Preview(context.Background(), []UploadedFile{
		upload("地点记录.csv", samplePlaces),
		upload("到访记录.csv", sampleVisits),
	}, DefaultOptions())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	for _, tbl := range []string{"places", "visits", "trips", "weather"} {
		if n := countOf(t, s, tbl); n != 0 {
			t.Errorf("预检后 %s 有 %d 行，应为空（预检不能写业务表）", tbl, n)
		}
	}
	if countOf(t, s, "imports") != 1 {
		t.Error("预检应留下一条审计记录")
	}
	if rep.Totals.Ready == 0 {
		t.Error("预检报告里没有可导入的行")
	}
}

func TestPreviewFlagsInvertedTime(t *testing.T) {
	s := newSvc(t)
	rep, err := s.Preview(context.Background(), []UploadedFile{
		upload("地点记录.csv", samplePlaces),
		upload("到访记录.csv", sampleVisits),
	}, DefaultOptions())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	var found bool
	for _, i := range rep.Issues {
		if i.Code == "ERR_TIME_INVERTED" {
			found = true
			if i.Severity != csvin.SevWarn {
				t.Errorf("默认策略下时间倒挂应是 warn（置空离开时刻），实际 %s", i.Severity)
			}
		}
	}
	if !found {
		t.Fatal("未检出时间倒挂")
	}
}

func TestCommitThenIdempotent(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	files := []UploadedFile{
		upload("地点记录.csv", samplePlaces),
		upload("到访记录.csv", sampleVisits),
	}
	opt := DefaultOptions()

	rep1, err := s.Preview(ctx, files, opt)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep1.BatchNo, Decisions{}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	places1, visits1 := countOf(t, s, "places"), countOf(t, s, "visits")
	if places1 != 2 || visits1 != 3 {
		t.Fatalf("首次导入：地点 %d（want 2）、到访 %d（want 3）", places1, visits1)
	}

	// 立刻重复导入同一批文件：必须一行都不新增。
	rep2, err := s.Preview(ctx, files, opt)
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	r2, err := s.Commit(ctx, rep2.BatchNo, Decisions{})
	if err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}
	if countOf(t, s, "places") != places1 || countOf(t, s, "visits") != visits1 {
		t.Fatalf("重复导入后行数变了：地点 %d、到访 %d", countOf(t, s, "places"), countOf(t, s, "visits"))
	}
	for _, f := range r2.Files {
		if f.Kind == csvin.KindVisits && f.Inserted != 0 {
			t.Errorf("第二次导入到访应全部跳过，实际新增 %d", f.Inserted)
		}
	}
}

// 重复导入时，预检报告本身必须显示「没有新东西要写」。
//
// 这里回归的是两类数字失真，都只在报告层、不反映到行数上，所以
// 「导入后行数不变」这类断言抓不到：
//  1. 到访/交通只统计了库内重复却不把记录从暂存里剔除，Ready 仍显示满额；
//  2. Totals 被累加两次，DupSkip 翻倍到比 Rows 还大。
func TestSecondPreviewReportsZeroReady(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	files := []UploadedFile{
		upload("地点记录.csv", samplePlaces),
		upload("到访记录.csv", sampleVisits),
	}
	opt := DefaultOptions()

	rep1, err := s.Preview(ctx, files, opt)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	r1, err := s.Commit(ctx, rep1.BatchNo, Decisions{})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if r1.Totals.Inserted != rep1.Totals.Ready {
		t.Errorf("首次提交 Totals.Inserted = %d，应等于可导入行数 %d（文件级有值但总计没汇总）",
			r1.Totals.Inserted, rep1.Totals.Ready)
	}

	rep2, err := s.Preview(ctx, files, opt)
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	if rep2.Totals.Ready != 0 {
		t.Errorf("第二次预检 Totals.Ready = %d，应为 0", rep2.Totals.Ready)
	}
	for _, f := range rep2.Files {
		if f.Ready != 0 {
			t.Errorf("%s 第二次预检 Ready = %d，应为 0（库内已存在，须从暂存剔除）", f.Kind, f.Ready)
		}
		if f.DupSkip != f.Rows {
			t.Errorf("%s DupSkip = %d，Rows = %d，全部重复时应相等", f.Kind, f.DupSkip, f.Rows)
		}
	}
	// Totals.DupSkip 必须等于各文件之和，不能超过 Rows。
	if rep2.Totals.DupSkip != rep2.Totals.Rows {
		t.Errorf("Totals.DupSkip = %d，Rows = %d（重复计数被累加了多次）",
			rep2.Totals.DupSkip, rep2.Totals.Rows)
	}

	r2, err := s.Commit(ctx, rep2.BatchNo, Decisions{})
	if err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}
	if r2.Totals.Inserted != 0 {
		t.Errorf("第二次提交 Totals.Inserted = %d，应为 0", r2.Totals.Inserted)
	}
}

func TestRollbackRestoresCounts(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	rep, err := s.Preview(ctx, []UploadedFile{
		upload("地点记录.csv", samplePlaces),
		upload("到访记录.csv", sampleVisits),
	}, DefaultOptions())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep.BatchNo, Decisions{}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if countOf(t, s, "visits") == 0 {
		t.Fatal("导入后应有到访记录")
	}
	if _, err := s.Rollback(ctx, rep.BatchNo); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	for _, tbl := range []string{"visits", "trips", "weather"} {
		if n := countOf(t, s, tbl); n != 0 {
			t.Errorf("回滚后 %s 仍有 %d 行", tbl, n)
		}
	}
	if n := countOf(t, s, "places"); n != 0 {
		t.Errorf("回滚后 places 仍有 %d 行（本批次创建且零引用应被删除）", n)
	}
	// 审计必须保留。
	if countOf(t, s, "imports") == 0 {
		t.Error("回滚不应删除审计记录")
	}
}

func TestWeatherConflictGroups(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	base := "日期,地点,天气状况,温度 (°C)\n"
	same := base +
		"2026-09-27T17:00:00+0800,示例地点,多云,27.280000686645508\n" +
		"2026-09-27T17:00:00+0800,示例地点,多云,27.280000686645508\n" // 内容一致（仅浮点噪声也一致）
	diff := base +
		"2026-09-27T18:00:00+0800,示例地点,多云,27.280000686645508\n" +
		"2026-09-27T18:00:00+0800,示例地点,下雨,22.34000015258789\n"

	rep, err := s.Preview(ctx, []UploadedFile{
		upload("地点记录.csv", "名称,纬度,经度\n示例地点,31.2400,121.4900\n"),
		upload("天气数据.csv", same+strings.Join(strings.Split(diff, "\n")[1:], "\n")),
	}, DefaultOptions())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(rep.Conflicts) != 1 {
		t.Fatalf("应有 1 组冲突，实际 %d：%#v", len(rep.Conflicts), rep.Conflicts)
	}
	c := rep.Conflicts[0]
	if len(c.Diffs) != 2 {
		t.Fatalf("冲突应含天气状况与温度两个字段，实际 %d：%+v", len(c.Diffs), c.Diffs)
	}
	for _, d := range c.Diffs {
		if d.Field == "temp_c" {
			// 浮点噪声必须被 Quantize 抹平，否则用户看到的是无意义差异。
			if d.New != 22.34 {
				t.Errorf("温度新值 = %v, want 22.34（噪声未抹平）", d.New)
			}
			if d.Old != 27.28 {
				t.Errorf("温度旧值 = %v, want 27.28", d.Old)
			}
		}
	}
}

// dataDir 返回要测试的 CSV 数据目录。
//
// 优先级：环境变量 YOULV_REAL_CSV_DIR（本地开发者用它跑自己的真实导出）
// > 仓库内的脱敏 fixture（CI 走这条，见 testdata/rond-export/README.md）。
//
// 两者都没有时跳过——测试不该因为缺少可选数据集而失败。
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

// TestRealFullImport 用全量 CSV 跑完整管线。
//
// 默认用仓库内的脱敏 fixture（CI 每次都跑）；设置 YOULV_REAL_CSV_DIR 可换成
// 自己的真实导出。两者跑出来的判据应完全一致——fixture 保留了原始数据的
// 全部结构性质（坐标系不变式、重名分支、天气冲突、时长舍入差）。
func TestRealFullImport(t *testing.T) {
	dir := dataDir(t)
	s := newSvc(t)
	ctx := context.Background()

	var files []UploadedFile
	for _, n := range []string{"places.csv", "visits.csv", "trips.csv", "weather.csv"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Skipf("缺少 %s: %v", n, err)
		}
		files = append(files, UploadedFile{Name: n, Size: int64(len(b)), Content: b})
	}

	rep, err := s.Preview(ctx, files, DefaultOptions())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(rep.Blocking) > 0 {
		t.Fatalf("预检出现阻断项: %v", rep.Blocking)
	}
	for _, f := range rep.Files {
		t.Logf("%s: %d 行, 可导入 %d, 失败 %d, 重复 %d",
			f.File, f.Rows, f.Ready, f.Failed, f.DupSkip)
	}
	for _, h := range rep.Highlights {
		t.Logf("提示: %s", h)
	}
	t.Logf("冲突组: %d", len(rep.Conflicts))

	// 判据：到访 4280 条必须全部可解析，不能有 unresolved。
	var visitsReady int
	for _, f := range rep.Files {
		if f.Kind == csvin.KindVisits {
			visitsReady = f.Ready
			if f.Failed != 0 {
				t.Errorf("到访有 %d 行失败，应为 0", f.Failed)
			}
		}
	}
	if visitsReady != 4280 {
		t.Errorf("到访可导入 %d, want 4280", visitsReady)
	}

	r, err := s.Commit(ctx, rep.BatchNo, Decisions{})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	for _, f := range r.Files {
		t.Logf("提交 %s: 新增 %d, 跳过 %d, 失败 %d", f.File, f.Inserted, f.Skipped, f.Failed)
	}

	// 判据：坐标系判定正确——高置信度解析要覆盖绝大多数到访。
	var high, unresolved int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM visits WHERE resolve_confidence='high'`).Scan(&high); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM visits WHERE resolve_method='unresolved'`).Scan(&unresolved); err != nil {
		t.Fatal(err)
	}
	t.Logf("到访解析：high=%d, unresolved=%d", high, unresolved)
	if high < 4250 {
		t.Errorf("高置信度解析 %d，低于 4250（坐标系判定可能失效）", high)
	}
	if unresolved != 0 {
		t.Errorf("有 %d 条到访未解析到地点", unresolved)
	}

	// 判据：幂等。重复提交同一批，业务表一行都不能多。
	before := map[string]int{}
	for _, tbl := range []string{"places", "visits", "trips", "weather"} {
		before[tbl] = countOf(t, s, tbl)
	}
	rep2, err := s.Preview(ctx, files, DefaultOptions())
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep2.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}
	for tbl, n := range before {
		if got := countOf(t, s, tbl); got != n {
			t.Errorf("重复导入后 %s 从 %d 变成 %d", tbl, n, got)
		}
	}
	t.Logf("幂等核对通过：%v", before)

	// 判据：回滚后归零。
	if _, err := s.Rollback(ctx, rep2.BatchNo); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	t.Logf("回滚第二批后：places=%d visits=%d trips=%d weather=%d",
		countOf(t, s, "places"), countOf(t, s, "visits"),
		countOf(t, s, "trips"), countOf(t, s, "weather"))
}

// ---------- 桩地点升级（PromoteStub）----------

// promotePlacesCSV 是一条带坐标的地点记录（坐标是 GCJ-02，与 Rond 导出一致）。
const promotePlacesCSV = "名称,纬度,经度,活跃范围半径,评分,国家或地区,行政区域,次级行政区域,城市/地区,区,街道,门牌号\n" +
	"未知办公楼,31.300000,120.580000,0.0,0.0,CN,,,,城市-0001,,\n"

// promoteVisitsCSV 是一条**没有坐标**的到访：地点不在地点库里 → 建桩。
const promoteVisitsCSV = "活动,地点,抵达时刻,离开时刻,时区,纬度,经度,标签,备注\n" +
	"工作,未知办公楼,2026-10-23T09:00:00+0800,2026-10-23T10:00:00+0800,Asia/Shanghai,0,0,,\n"

func placeRow(t *testing.T, s *Service) (isStub int, lat, lon *float64, origin string) {
	t.Helper()
	// 同名可能有多个分支（连锁分店），桩优先：is_stub DESC 让桩排在最前。
	if err := s.db.QueryRow(
		`SELECT is_stub, lat, lon, origin FROM places WHERE name='未知办公楼'
		 ORDER BY is_stub DESC, id LIMIT 1`).
		Scan(&isStub, &lat, &lon, &origin); err != nil {
		t.Fatalf("读地点行失败: %v", err)
	}
	return
}

// 桩升级：地点记录带来真实坐标后，「仅引用」桩应变成正式地点，
// 引用它的到访的空坐标也应被回填。
func TestPromoteStubUpgradesPlace(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()

	// 第一批：只有到访，地点名不在地点库 → 建桩。
	rep1, err := s.Preview(ctx, []UploadedFile{upload("到访记录.csv", promoteVisitsCSV)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第一次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep1.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第一次 Commit: %v", err)
	}
	isStub, lat, lon, origin := placeRow(t, s)
	if isStub != 1 {
		t.Fatalf("第一批之后 is_stub = %d，应为 1（应建桩）", isStub)
	}
	if lat != nil || lon != nil {
		t.Errorf("桩不应有坐标，实际 lat=%v lon=%v", lat, lon)
	}
	if origin != "reference_only" {
		t.Errorf("桩的 origin = %q，应为 reference_only", origin)
	}
	var visitLat *float64
	if err := s.db.QueryRow(`SELECT lat FROM visits`).Scan(&visitLat); err != nil {
		t.Fatal(err)
	}
	if visitLat != nil {
		t.Errorf("到访本无坐标，导入前不该有值")
	}

	// 第二批：地点记录带来同名坐标 → 升级。
	rep2, err := s.Preview(ctx, []UploadedFile{upload("地点记录.csv", promotePlacesCSV)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	var mentioned bool
	for _, h := range rep2.Highlights {
		if strings.Contains(h, "并入") {
			mentioned = true
		}
	}
	if !mentioned {
		t.Errorf("预检提示里应说明有地点将并入既有记录，实际：%v", rep2.Highlights)
	}
	if _, err := s.Commit(ctx, rep2.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}

	// 判据一：仍是**同一行**（不新增地点），但已不是桩。
	if n := countOf(t, s, "places"); n != 1 {
		t.Errorf("地点行数 = %d，应为 1（升级不是新增）", n)
	}
	isStub, lat, lon, origin = placeRow(t, s)
	if isStub != 0 {
		t.Errorf("升级后 is_stub = %d，应为 0", isStub)
	}
	if lat == nil || lon == nil {
		t.Fatalf("升级后应有坐标")
	}
	// CSV 是 GCJ-02，入库存的是换算后的 WGS-84。
	wantLat, wantLon := geo.GCJ02ToWGS84(31.3, 120.58)
	if math.Abs(*lat-wantLat) > 1e-6 || math.Abs(*lon-wantLon) > 1e-6 {
		t.Errorf("升级后的坐标 = %.6f,%.6f，应为 %.6f,%.6f", *lat, *lon, wantLat, wantLon)
	}
	if origin != "imported" {
		t.Errorf("升级后 origin = %q，应为 imported", origin)
	}

	// 判据二：引用它的到访的空坐标被回填。
	if err := s.db.QueryRow(`SELECT lat FROM visits`).Scan(&visitLat); err != nil {
		t.Fatal(err)
	}
	if visitLat == nil {
		t.Errorf("升级后到访的坐标应被回填")
	} else if math.Abs(*visitLat-wantLat) > 1e-6 {
		t.Errorf("回填的坐标 = %.6f，应为 %.6f", *visitLat, wantLat)
	}
}

// 幂等：桩升级后再导入同一份地点记录，不能新增行、不能重复升级。
func TestPromoteStubIsIdempotent(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()

	rep1, err := s.Preview(ctx, []UploadedFile{upload("到访记录.csv", promoteVisitsCSV)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第一次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep1.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第一次 Commit: %v", err)
	}
	rep2, err := s.Preview(ctx, []UploadedFile{upload("地点记录.csv", promotePlacesCSV)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep2.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}
	before := countOf(t, s, "places")

	// 第三批：同样的地点记录 → 应当被识别为「已导入过」。
	rep3, err := s.Preview(ctx, []UploadedFile{upload("地点记录.csv", promotePlacesCSV)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第三次 Preview: %v", err)
	}
	if rep3.Totals.Ready != 0 {
		t.Errorf("第三次预检 Ready = %d，应为 0（地点已存在）", rep3.Totals.Ready)
	}
	if _, err := s.Commit(ctx, rep3.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第三次 Commit: %v", err)
	}
	if after := countOf(t, s, "places"); after != before {
		t.Errorf("重复导入后地点行数从 %d 变成 %d", before, after)
	}
	isStub, _, _, _ := placeRow(t, s)
	if isStub != 0 {
		t.Errorf("重复导入后 is_stub = %d，应仍为 0", isStub)
	}
}

// 同名多行（多个分支）时放弃升级：桩该升级成哪一个无从判定，
// 宁可少升级，也不要把坐标写到错的分支上。
func TestPromoteStubSkipsAmbiguousName(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()

	rep1, err := s.Preview(ctx, []UploadedFile{upload("到访记录.csv", promoteVisitsCSV)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第一次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep1.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第一次 Commit: %v", err)
	}

	ambiguous := "名称,纬度,经度,活跃范围半径,评分,国家或地区,行政区域,次级行政区域,城市/地区,区,街道,门牌号\n" +
		"未知办公楼,31.300000,120.580000,0.0,0.0,CN,,,,城市-0001,,\n" +
		"未知办公楼,31.310000,120.590000,0.0,0.0,CN,,,,城市-0001,,\n"
	rep2, err := s.Preview(ctx, []UploadedFile{upload("地点记录.csv", ambiguous)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	for _, h := range rep2.Highlights {
		if strings.Contains(h, "并入") {
			t.Errorf("同名多行时不应提示合并：%s", h)
		}
	}
	if _, err := s.Commit(ctx, rep2.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}
	// 桩原样保留，两个同名分支各自成为独立地点。
	isStub, _, _, _ := placeRow(t, s)
	if isStub != 1 {
		t.Errorf("同名多行时桩应保持不变，is_stub = %d", isStub)
	}
	if n := countOf(t, s, "places"); n != 3 {
		t.Errorf("地点行数 = %d，应为 3（1 桩 + 2 个分支）", n)
	}
}

// 分批导入不得把同一个地点存成两行。
//
// 场景：先只导到访记录（地点由到访坐标派生，source_key 按到访的 WGS-84
// 算），后来再导地点记录（source_key 按登记点的 GCJ-02 算）。两个 key 不相等，
// 天真实现会插出两行同名地点——实测同一批数据因此从 1001 行涨到 1825 行。
//
// 修法是「按名字 + 坐标就近合并」：地点记录的行若与库里某个同名记录几乎
// 重合（5 m 内，与坐标系判定同一阈值），就并回那一行而不是新建。
func TestSplitImportDoesNotDuplicatePlaces(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()

	// 第一批：只有到访 → 地点由到访坐标派生。
	rep1, err := s.Preview(ctx, []UploadedFile{upload("到访记录.csv", sampleVisits)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第一次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep1.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第一次 Commit: %v", err)
	}
	afterFirst := countOf(t, s, "places")
	if afterFirst != 2 {
		t.Fatalf("第一批后地点数 = %d，应为 2", afterFirst)
	}

	// 第二批：地点记录 → 应并入既有行。
	rep2, err := s.Preview(ctx, []UploadedFile{upload("地点记录.csv", samplePlaces)}, DefaultOptions())
	if err != nil {
		t.Fatalf("第二次 Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep2.BatchNo, Decisions{}); err != nil {
		t.Fatalf("第二次 Commit: %v", err)
	}
	if n := countOf(t, s, "places"); n != afterFirst {
		t.Errorf("分批导入后地点数从 %d 变成 %d，同一地点被存成了两行", afterFirst, n)
	}
	// 并入后这些地点应是正式地点（有坐标、有地址），不再是到访派生的。
	var noCoord int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM places WHERE lat IS NULL OR lon IS NULL`).Scan(&noCoord); err != nil {
		t.Fatal(err)
	}
	if noCoord != 0 {
		t.Errorf("并入后仍有 %d 个地点没有坐标", noCoord)
	}
}
