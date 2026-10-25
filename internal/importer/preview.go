package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/Felix2yu/youlv/internal/csvin"
	"github.com/Felix2yu/youlv/internal/geo"
)

// UploadedFile 是一个已上传的源文件。multipart 解析后落到这里，
// 预检与提交都只认这个结构。
type UploadedFile struct {
	Name    string
	Size    int64
	Content []byte
}

// Service 是导入服务的入口。
type Service struct {
	db     *sql.DB
	tmpDir string
	tz     *time.Location
}

// New 构造导入服务。tmpDir 是暂存目录（预检产物落在这里）。
func New(db *sql.DB, tmpDir string, tz *time.Location) *Service {
	if tz == nil {
		tz = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return &Service{db: db, tmpDir: tmpDir, tz: tz}
}

// Preview 解析全部文件并产出预检报告。
//
// 它**只写审计表**（imports / imports_files / import_issues），一行业务数据
// 都不写。业务表要等用户确认后的 Commit 才动。
func (s *Service) Preview(ctx context.Context, files []UploadedFile, opt Options) (*Report, error) {
	started := time.Now()
	batchNo := newBatchNo()

	// 四个切片必须初始化成非 nil：Go 的 nil 切片会序列化成 JSON null，
	// 而前端（与 TS 类型声明）按数组处理 —— 直接读 .length 就会抛
	// "Cannot read properties of null (reading 'length')"。
	rep := &Report{
		BatchNo:    batchNo,
		Status:     StatusPreview,
		Options:    opt,
		Files:      []FileStat{},
		Issues:     []csvin.Issue{},
		Conflicts:  []ConflictGroup{},
		Highlights: []string{},
		Blocking:   []string{},
	}

	// --- 1. 解码与类型识别 ---
	decoded := make([]*csvin.File, 0, len(files))
	detects := make([]*csvin.DetectResult, 0, len(files))
	for _, uf := range files {
		f, decIssues, err := csvin.Decode(uf.Name, strings.NewReader(string(uf.Content)))
		if err != nil {
			rep.Blocking = append(rep.Blocking, fmt.Sprintf("%s：%v", uf.Name, err))
			continue
		}
		d, err := csvin.Detect(f.Header)
		if err != nil {
			rep.Blocking = append(rep.Blocking, fmt.Sprintf("%s：%v", uf.Name, err))
			continue
		}
		f.Kind = d.Kind
		for i := range decIssues {
			decIssues[i].Column = uf.Name
			// 解码阶段的问题同样要带上 kind：类型此刻已确定。
			decIssues[i].Kind = string(f.Kind)
		}
		rep.Issues = append(rep.Issues, decIssues...)

		st := FileStat{
			Kind: f.Kind, Label: f.Kind.Label(), File: uf.Name,
			SHA256: f.Sha256, Rows: len(f.Records),
			Detected: string(d.Kind), Confidence: d.Confidence,
			Missing: d.MissingReq, MissingOpt: d.MissingOpt,
			Encoding: f.Encoding, Extra: d.Extra,
		}
		if len(d.MissingReq) > 0 {
			rep.Blocking = append(rep.Blocking,
				fmt.Sprintf("%s 缺少必需列：%s", uf.Name, strings.Join(d.MissingReq, "、")))
		}
		if d.Confidence < csvin.MinDetectConfidence {
			rep.Blocking = append(rep.Blocking,
				fmt.Sprintf("%s 无法自动确定类型（置信度 %.0f%%），请手动指定", uf.Name, d.Confidence*100))
		}
		if f.Encoding == "gb18030" {
			rep.Highlights = append(rep.Highlights,
				fmt.Sprintf("%s 不是 UTF-8，已按 GBK 解码（建议导出时选 UTF-8）", uf.Name))
		}
		rep.Files = append(rep.Files, st)
		decoded = append(decoded, f)
		detects = append(detects, d)
	}
	if len(decoded) == 0 {
		return nil, UserErrf("没有可解析的文件：%s", strings.Join(rep.Blocking, "；"))
	}

	// --- 2. 载入库内已有的地点，供重名消歧与去重 ---
	existing, err := s.loadPlaces(ctx)
	if err != nil {
		return nil, err
	}
	resolver := newPlaceResolver(opt.SnapMeters, opt.NearestMeters)
	for _, c := range existing {
		resolver.Add(c)
	}
	existingKeys := make(map[string]bool, len(existing))
	for _, c := range existing {
		if c.IsStub {
			// 桩的 source_key 不进 existingKeys：它还没被正式导入过，
			// 本批次若带来同名地点的真实坐标，应当升级它而不是跳过。
			if c.HasCoord {
				existingKeys[PlaceSourceKey(c.Name, c.LatGCJ, c.LonGCJ, true)] = true
			}
			continue
		}
		existingKeys[c.Key] = true
		// 额外登记坐标键：visit_derived 地点的 source_key 是按到访的
		// WGS-84 算的，而地点记录的行是按登记点的 GCJ-02 算的——同一个
		// 物理地点会算出两个不同的键。不登记坐标键，「先导到访、后导地点」
		// 的分批导入在第二次导入时就会再插一条。
		if c.HasCoord {
			existingKeys[PlaceSourceKey(c.Name, c.LatGCJ, c.LonGCJ, true)] = true
		}
	}

	// --- 3. 按依赖顺序解析：地点 → 到访 → 交通 → 天气 ---
	var places []stagedPlace
	var visits []stagedVisit
	var trips []stagedTrip
	var weather []stagedWeather

	for i, f := range decoded {
		switch f.Kind {
		case csvin.KindPlaces:
			places, rep = s.parsePlaces(f, detects[i], rep, resolver, existingKeys, opt)
		}
	}
	// 地点解析完之后重名提示才准确。
	if n := resolver.HomonymGroups(); n > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("检测到 %d 组重名地点（同名不同址，如连锁分店），将按「时刻 + 坐标」消歧，不做合并", n))
	}

	// 到访必须先于交通/天气解析。
	//
	// 交通与天气只有地点名，同名多址时得靠「那一刻人在哪」来消歧，而这条
	// 证据就是到访记录。此前四个文件挤在同一个循环里按上传顺序处理，交通
	// 先上传时锚点全空，塔斯汀这类连锁店的交通会整体绑到同一条分店上。
	for i, f := range decoded {
		if f.Kind == csvin.KindVisits {
			visits, rep = s.parseVisits(f, detects[i], rep, resolver, opt)
		}
	}
	// 锚点 = 本批次刚解析的到访 + 库里已有的。增量导入（只带交通/天气）时
	// 前者为空，全靠后者。
	past, err := s.loadVisitAnchors(ctx)
	if err != nil {
		return nil, err
	}
	anchors := newTimeIndex(append(anchorsFromStaged(visits), past...))

	for i, f := range decoded {
		switch f.Kind {
		case csvin.KindTrips:
			trips, rep = s.parseTrips(f, detects[i], rep, resolver, anchors, opt)
		case csvin.KindWeather:
			existingW, err := s.loadExistingWeather(ctx)
			if err != nil {
				return nil, err
			}
			weather, rep = s.parseWeather(f, detects[i], rep, resolver, anchors, opt, existingW)
		}
	}

	// --- 4. 把解析途中动态创建的地点补进暂存 ---
	// 这些条目不来自地点记录文件，而是到访/交通/天气遇到未知名地点时补建的。
	// 漏掉它们会让到访引用一个不存在的地点键。
	have := make(map[string]bool, len(places))
	for _, p := range places {
		have[p.SourceKey] = true
	}
	var stubs int
	for _, c := range resolver.Created() {
		if have[c.Key] {
			continue
		}
		have[c.Key] = true
		p := stagedPlace{
			SourceKey: c.Key, Name: c.Name, DisplayName: c.Name,
			Origin: "visit_derived", CoordFrame: "wgs84_stored",
		}
		if c.HasCoord {
			p.Lat, p.Lon = ptr(c.Lat), ptr(c.Lon)
			p.LatGCJ, p.LonGCJ = ptr(c.LatGCJ), ptr(c.LonGCJ)
		}
		if c.IsStub {
			p.Origin, p.IsStub, p.CoordFrame = "reference_only", true, "na"
			stubs++
		}
		places = append(places, p)
	}
	if stubs > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("为 %d 个仅被引用、未登记坐标的地点建了桩（不进足迹地图，后续导入真实坐标会自动升级）", stubs))
	}

	// --- 5. 与库内已有记录比对，剔除会被跳过的行 ---
	visits, trips, rep = s.dropExistingDups(ctx, rep, visits, trips)

	// --- 5. 落暂存 ---
	dir := s.stageDir(batchNo)
	if err := writeStaged(filepath.Join(dir, stageFileName(csvin.KindPlaces)), places); err != nil {
		return nil, err
	}
	if err := writeStaged(filepath.Join(dir, stageFileName(csvin.KindVisits)), visits); err != nil {
		return nil, err
	}
	if err := writeStaged(filepath.Join(dir, stageFileName(csvin.KindTrips)), trips); err != nil {
		return nil, err
	}
	if err := writeStaged(filepath.Join(dir, stageFileName(csvin.KindWeather)), weather); err != nil {
		return nil, err
	}

	// --- 6. 汇总与审计 ---
	for i := range rep.Files {
		rep.Totals.Rows += rep.Files[i].Rows
		rep.Totals.Ready += rep.Files[i].Ready
		rep.Totals.Failed += rep.Files[i].Failed
		rep.Totals.DupSkip += rep.Files[i].DupSkip
		// 预检阶段 Inserted/Skipped 恒为 0，一并累加以免漏项。
		rep.Totals.Inserted += rep.Files[i].Inserted
		rep.Totals.Skipped += rep.Files[i].Skipped
	}
	if err := s.savePreview(ctx, batchNo, rep, decoded, detects); err != nil {
		return nil, err
	}
	rep.DurationMS = time.Since(started).Milliseconds()
	log.Printf("[import] 预检完成 %s：%d 行可导入，%d 行失败，%d 组冲突",
		batchNo, rep.Totals.Ready, rep.Totals.Failed, len(rep.Conflicts))
	return rep, nil
}

// loadPlaces 载入库内已有地点，用于重名消歧与去重判定。
func (s *Service) loadPlaces(ctx context.Context) ([]*candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_key, name,
		COALESCE(lat,0), COALESCE(lon,0), COALESCE(lat_gcj,0), COALESCE(lon_gcj,0),
		is_stub, visit_count, origin FROM places`)
	if err != nil {
		return nil, fmt.Errorf("读取已有地点失败: %w", err)
	}
	defer rows.Close()
	var out []*candidate
	for rows.Next() {
		var c candidate
		var stub int
		var lat, lon, latg, long float64
		if err := rows.Scan(&c.Key, &c.Name, &lat, &lon, &latg, &long, &stub, &c.Hits, &c.Origin); err != nil {
			return nil, fmt.Errorf("扫描地点失败: %w", err)
		}
		c.Lat, c.Lon, c.LatGCJ, c.LonGCJ = lat, lon, latg, long
		c.HasCoord = lat != 0 || lon != 0
		c.IsStub = stub == 1
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Service) stat(kind csvin.Kind, rep *Report) *FileStat {
	for i := range rep.Files {
		if rep.Files[i].Kind == kind {
			return &rep.Files[i]
		}
	}
	return nil
}

func (s *Service) addIssue(rep *Report, kind csvin.Kind, i csvin.Issue) *Report {
	// 把 kind 写进 issue：它决定了这条问题来自哪份文件，落库时要进
	// import_issues.kind，之前在这里被丢掉，写进去的都是空串。
	i.Kind = string(kind)
	rep.Issues = append(rep.Issues, i)
	return rep
}

// ---------- 地点 ----------

func (s *Service) parsePlaces(f *csvin.File, d *csvin.DetectResult, rep *Report,
	resolver *PlaceResolver, existingKeys map[string]bool, opt Options) ([]stagedPlace, *Report) {
	ci := d.Mapping
	st := s.stat(csvin.KindPlaces, rep)
	var out []stagedPlace
	seen := map[string]bool{}
	var promoted int

	// 预统计每个名字在本文件里出现几次。
	//
	// 同名多行意味着这个地点有多个分支（连锁分店），桩该升级成哪一个
	// 无法判定——这种情况下宁可少升级，也不要把坐标写到错的分支上。
	nameCount := map[string]int{}
	for _, rec := range f.Records {
		nameCount[strings.TrimSpace(ci.Get(rec, "名称"))]++
	}

	for i, rec := range f.Records {
		rowNo := i + 1
		line := f.LineOf[i]

		name := strings.TrimSpace(ci.Get(rec, "名称"))
		if name == "" {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "名称", Field: "name", Severity: csvin.SevError,
				Code: "ERR_NAME_MISSING", Message: "地点名称为空", Hint: "补一个名称，或删掉这一行",
			})
			continue
		}
		lat, okLat := csvin.ParseFloat(ci.Get(rec, "纬度"))
		lon, okLon := csvin.ParseFloat(ci.Get(rec, "经度"))
		if !okLat || !okLon {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "纬度/经度", Field: "lat/lon",
				Value:    ci.Get(rec, "纬度") + "," + ci.Get(rec, "经度"),
				Severity: csvin.SevError, Code: "ERR_COORD_PARSE",
				Message: "纬度或经度无法解析", Hint: "应为十进制度，例如 31.299758",
			})
			continue
		}
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "纬度/经度", Field: "lat/lon",
				Value:    fmt.Sprintf("%.6f,%.6f", lat, lon),
				Severity: csvin.SevError, Code: "ERR_COORD_RANGE",
				Message: "坐标超出合法范围", Hint: "纬度应在 -90~90，经度在 -180~180",
			})
			continue
		}

		lat = geo.Quantize(lat, 6)
		lon = geo.Quantize(lon, 6)
		zero := lat == 0 && lon == 0
		if zero {
			st.Warned++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "纬度/经度", Field: "lat/lon",
				Value: "0,0", Severity: csvin.SevWarn, Code: "WARN_COORD_ZERO",
				Message: "坐标为 (0,0)，视为无坐标", Hint: "该地点不会出现在足迹地图上",
			})
		}

		hasCoord := !zero
		key := PlaceSourceKey(name, lat, lon, hasCoord)

		// 桩升级判定：库里有个同名桩（此前只在到访/交通/天气里被提到过），
		// 这次地点记录带来了它的真实坐标——那它就该升级成正式地点，
		// 而不是旁边再插一条同名的新地点。
		//
		// 两个前置条件：
		//   ① 本行有真实坐标（没坐标的桩还是桩）；
		//   ② 该名字在本文件里只出现一次（同名多行 = 多个分支，
		//      桩该升级成哪一个无从判定）。
		// 地点合并判定：库里已经有一个「同一个点」的记录时，不该再插一条。
		//
		// 两种情形：
		//   ① 同名桩（此前只在到访/交通/天气里被提到过，没有自己的坐标）——
		//      这次地点记录带来了真实坐标，它就该升级成正式地点；
		//   ② 同名且坐标几乎重合的既有地点（多半是先前由到访坐标派生的
		//      visit_derived 行）——分批导入时，同一地点会因为「key 用的
		//      坐标系不同」被存成两行，这里把它并回既有行。
		//
		// 两个前置条件：
		//   a. 本行有真实坐标（没坐标的桩还是桩）；
		//   b. 该名字在本文件里只出现一次（同名多行 = 多个分支，
		//      该并到哪一个无从判定）。
		var merge *candidate
		if hasCoord {
			if st := resolver.StubByName(name); st != nil && nameCount[name] == 1 {
				// 桩没有坐标，无法用距离判定归属：同名在文件里出现多次时
				// 它该并到哪一行无从判断，放弃合并。
				merge = st
			} else {
				// 按距离判定，而不是「同名是否唯一出现」：实测 36 组同名地点里
				// 有 12 组的两个坐标相距不到 5 m——那是同一个地点的两次登记，
				// 按「同名即放弃」会把它们拆成两个地点。而真正的分店相距
				// 几十米以上（中位 94 m），5 m 阈值不会误合。
				wLat, wLon := geo.GCJ02ToWGS84(lat, lon)
				merge = resolver.MatchNearby(name, wLat, wLon, resolver.snapM)
			}
		}
		if merge != nil {
			// 沿用既有行的 source_key：到访的 dedup_key 里嵌着 placeKey，
			// 换键会让历史到访的去重失配，重导时整批重复插入。
			key = merge.Key
			if merge.Origin == "imported" && merge.HasCoord {
				// 这条记录已经被地点记录处理过了（坐标与地址都补全过），
				// 再导一遍不产生任何变化，按重复跳过。
				st.DupSkip++
				rep = s.addIssue(rep, f.Kind, csvin.Issue{
					Line: line, RowNo: rowNo, Column: "名称", Field: "source_key", Value: name,
					Severity: csvin.SevInfo, Code: "INFO_DUP_IDENTICAL",
					Message: fmt.Sprintf("地点「%s」已导入过，将跳过", name), Hint: "无需处理",
				})
				continue
			}
		}

		// 同批次内的完全重复行：靠 source_key 自然合并。
		if seen[key] {
			st.DupSkip++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "名称", Field: "source_key", Value: name,
				Severity: csvin.SevInfo, Code: "INFO_DUP_IDENTICAL",
				Message: fmt.Sprintf("地点「%s」在本批次内重复，已合并", name), Hint: "无需处理",
			})
			continue
		}
		if merge == nil && existingKeys[key] {
			st.DupSkip++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "名称", Field: "source_key", Value: name,
				Severity: csvin.SevInfo, Code: "INFO_DUP_IDENTICAL",
				Message: fmt.Sprintf("地点「%s」已导入过，将跳过", name), Hint: "无需处理",
			})
			continue
		}
		seen[key] = true

		// 坐标系换算：CSV 是 GCJ-02，权威存储是 WGS-84。
		wLat, wLon := geo.GCJ02ToWGS84(lat, lon)
		wLat, wLon = geo.Quantize(wLat, 6), geo.Quantize(wLon, 6)

		p := stagedPlace{
			SourceKey: key, Name: name, DisplayName: name,
			Lat: ptr(wLat), Lon: ptr(wLon), LatGCJ: ptr(lat), LonGCJ: ptr(lon),
			CoordFrame: "wgs84_stored", Origin: "imported",
		}
		if merge != nil {
			// 就地补全候选：同一批次里随后的到访/交通/天气就能按真实
			// 坐标去解析，而不是退化成「同名无坐标」的唯一命中。
			resolver.PromoteStub(merge, wLat, wLon, lat, lon)
			p.Promote = true
			p.DisplayName = name
			promoted++
		}
		if !hasCoord {
			p.Lat, p.Lon, p.LatGCJ, p.LonGCJ = nil, nil, nil, nil
			p.CoordFrame = "na"
			p.IsStub = true
			p.Origin = "reference_only"
		}

		if v, ok := csvin.ParseFloat(ci.Get(rec, "活跃范围半径")); ok {
			if v < 0 {
				st.Failed++
				rep = s.addIssue(rep, f.Kind, csvin.Issue{
					Line: line, RowNo: rowNo, Column: "活跃范围半径", Field: "radius_m",
					Value: fmt.Sprintf("%.1f", v), Severity: csvin.SevError, Code: "ERR_RADIUS_NEGATIVE",
					Message: "活跃范围半径为负数", Hint: "应为 0 或正数",
				})
				continue
			}
			// 实测 89 条为 0.0：0 表示「未设置」，存 NULL 而不是 0。
			if v > 0 {
				p.RadiusM = ptr(geo.Quantize(v, 1))
			} else if !opt.RadiusZeroNull {
				p.RadiusM = ptr(float64(0))
			}
		}
		if v, ok := csvin.ParseFloat(ci.Get(rec, "评分")); ok {
			// 实测全部 893 条都是 0.0，存 0 会让「未评分」与「评 0 分」混淆。
			if v > 0 || !opt.RatingZeroNull {
				p.Rating = ptr(geo.Quantize(v, 2))
			}
		}
		p.Country = optStr(ci.Get(rec, "国家或地区"))
		p.AdminArea = optStr(ci.Get(rec, "行政区域"))
		p.SubAdmin = optStr(ci.Get(rec, "次级行政区域"))
		p.City = optStr(ci.Get(rec, "城市/地区"))
		p.District = optStr(ci.Get(rec, "区"))
		p.Street = optStr(ci.Get(rec, "街道"))
		p.HouseNumber = optStr(ci.Get(rec, "门牌号"))

		if merge == nil {
			// 合并分支不新增候选：候选已经就地补全了坐标，
			// 再插一个同键候选会让解析误判成「同名多址」。
			resolver.Add(&candidate{
				Key: key, Name: name, Lat: wLat, Lon: wLon, LatGCJ: lat, LonGCJ: lon,
				HasCoord: hasCoord,
			})
		}
		out = append(out, p)
		st.Ready++
	}
	if promoted > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("有 %d 个地点将并入库中已有的同名记录（此前只有到访坐标或仅有名字），坐标与地址将被补全", promoted))
	}
	return out, rep
}

// ---------- 到访 ----------

func (s *Service) parseVisits(f *csvin.File, d *csvin.DetectResult, rep *Report,
	resolver *PlaceResolver, opt Options) ([]stagedVisit, *Report) {
	ci := d.Mapping
	st := s.stat(csvin.KindVisits, rep)
	var out []stagedVisit
	seen := map[string]bool{}
	frameFixed := 0

	for i, rec := range f.Records {
		rowNo := i + 1
		line := f.LineOf[i]

		activity := strings.TrimSpace(ci.Get(rec, "活动"))
		placeName := strings.TrimSpace(ci.Get(rec, "地点"))
		arr, err := csvin.ParseTime(ci.Get(rec, "抵达时刻"), s.tz)
		if err != nil {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "抵达时刻", Field: "arrived_at",
				Value: ci.Get(rec, "抵达时刻"), Severity: csvin.SevError, Code: "ERR_TIME_PARSE",
				Message: fmt.Sprintf("抵达时刻无法解析：%v", err),
				Hint:    "期望形如 2026-10-23T14:11:49+0800",
			})
			continue
		}
		dep, errDep := csvin.ParseTime(ci.Get(rec, "离开时刻"), s.tz)

		var depPtr *int64
		var depLocal string
		var durPtr *int64
		switch {
		case errDep == nil:
			if dep.Unix < arr.Unix {
				// 实测 1 条：离开早于抵达约 23.9 小时。
				switch opt.DepartedBeforeArrived {
				case "reject":
					st.Failed++
					rep = s.addIssue(rep, f.Kind, csvin.Issue{
						Line: line, RowNo: rowNo, Column: "离开时刻", Field: "departed_at",
						Value: ci.Get(rec, "离开时刻"), Severity: csvin.SevError, Code: "ERR_TIME_INVERTED",
						Message: fmt.Sprintf("离开时刻早于抵达时刻 %s", csvin.DurationFmt(arr.Unix-dep.Unix)),
						Hint:    "请修正 CSV，或改用「置空离开时刻」策略",
					})
					continue
				case "swap":
					arr, dep = dep, arr
					st.Warned++
					rep = s.addIssue(rep, f.Kind, csvin.Issue{
						Line: line, RowNo: rowNo, Column: "抵达时刻", Field: "arrived_at",
						Severity: csvin.SevWarn, Code: "WARN_TIME_SWAPPED",
						Message: "离开时刻早于抵达时刻，已按交换处理", Hint: "确认这条数据的原始顺序",
					})
				default: // null
					st.Warned++
					rep = s.addIssue(rep, f.Kind, csvin.Issue{
						Line: line, RowNo: rowNo, Column: "离开时刻", Field: "departed_at",
						Value: ci.Get(rec, "离开时刻"), Severity: csvin.SevWarn, Code: "ERR_TIME_INVERTED",
						Message: fmt.Sprintf("离开时刻早于抵达时刻 %s，已置空离开时刻（停留时长留空）",
							csvin.DurationFmt(arr.Unix-dep.Unix)),
						Hint: "原始行未丢失，修正后可重新导入覆盖",
					})
				}
			}
			if errDep == nil || opt.DepartedBeforeArrived == "swap" {
				depPtr = ptr(dep.Unix)
				depLocal = dep.Local
				d := dep.Unix - arr.Unix
				durPtr = ptr(d)
				if d < 60 {
					st.Warned++
					rep = s.addIssue(rep, f.Kind, csvin.Issue{
						Line: line, RowNo: rowNo, Column: "离开时刻", Field: "duration_s",
						Value: fmt.Sprintf("%ds", d), Severity: csvin.SevWarn, Code: "WARN_DURATION_SHORT",
						Message: fmt.Sprintf("停留仅 %s，可能是路过误记", csvin.DurationFmt(d)),
						Hint:    "确认后可在到访详情里忽略该条",
					})
				} else if d > 86400 {
					st.Warned++
					rep = s.addIssue(rep, f.Kind, csvin.Issue{
						Line: line, RowNo: rowNo, Column: "离开时刻", Field: "duration_s",
						Value: csvin.DurationFmt(d), Severity: csvin.SevWarn, Code: "WARN_DURATION_LONG",
						Message: fmt.Sprintf("停留 %s，超过 24 小时", csvin.DurationFmt(d)),
						Hint:    "若属正常（如在家过夜）可忽略",
					})
				}
			}
		case ci.Get(rec, "离开时刻") != "":
			st.Warned++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "离开时刻", Field: "departed_at",
				Value: ci.Get(rec, "离开时刻"), Severity: csvin.SevWarn, Code: "WARN_TIME_PARSE",
				Message: "离开时刻无法解析，已置空", Hint: "期望形如 2026-10-23T14:11:49+0800",
			})
		}

		lat, okLat := csvin.ParseFloat(ci.Get(rec, "纬度"))
		lon, okLon := csvin.ParseFloat(ci.Get(rec, "经度"))
		// 到访的坐标以前完全不校验：解析失败静默置 (0,0)、越界值（如 999）
		// 原样入库。前者让用户看不出坐标丢了，后者直接污染地图与距离计算。
		switch {
		case !okLat || !okLon:
			if ci.Get(rec, "纬度") != "" || ci.Get(rec, "经度") != "" {
				st.Warned++
				rep = s.addIssue(rep, f.Kind, csvin.Issue{
					Line: line, RowNo: rowNo, Column: "纬度/经度", Field: "lat/lon",
					Value:    ci.Get(rec, "纬度") + "," + ci.Get(rec, "经度"),
					Severity: csvin.SevWarn, Code: "ERR_COORD_PARSE",
					Message: "纬度或经度无法解析，按无坐标处理",
					Hint:    "应为十进制度，例如 31.299758；这条到访不会出现在地图上",
				})
			}
			lat, lon = 0, 0
		case lat < -90 || lat > 90 || lon < -180 || lon > 180:
			st.Warned++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "纬度/经度", Field: "lat/lon",
				Value:    fmt.Sprintf("%.6f,%.6f", lat, lon),
				Severity: csvin.SevWarn, Code: "WARN_COORD_RANGE",
				Message: "坐标超出合法范围，按无坐标处理",
				Hint:    "纬度应在 -90~90，经度在 -180~180",
			})
			lat, lon = 0, 0
		}
		lat, lon = geo.Quantize(lat, 6), geo.Quantize(lon, 6)

		res, resIssues := resolver.ResolveVisit(placeName, lat, lon, opt.CreateStubs)
		for _, ri := range resIssues {
			ri.Line, ri.RowNo = line, rowNo
			st.Warned++
			rep = s.addIssue(rep, f.Kind, ri)
		}
		if res.Method == "unresolved" {
			st.Failed++
			continue
		}
		if res.Frame == "gcj02_mislabeled" {
			frameFixed++
			// 判定为未换算：入库前就地纠正，原值留在 lat_raw/lon_raw。
			lat, lon = geo.GCJ02ToWGS84(lat, lon)
			lat, lon = geo.Quantize(lat, 6), geo.Quantize(lon, 6)
		}

		dk := visitDedupKey(res.PlaceKey, arr.Unix, depPtr, lat, lon)
		if seen[dk] {
			st.DupSkip++
			continue
		}
		seen[dk] = true

		gLat, gLon := geo.WGS84ToGCJ02(lat, lon)
		v := stagedVisit{
			PlaceKey: res.PlaceKey, PlaceNameRaw: placeName, ActivityRaw: activity,
			ArrivedAt: arr.Unix, DepartedAt: depPtr, DurationS: durPtr,
			TZ: opt.Timezone, ArrivedLocal: arr.Local, DepartedLocal: depLocal,
			LocalDate: arr.Date, LocalMonth: arr.Month,
			LocalYear: arr.Year, LocalHour: arr.Hour, LocalDow: arr.Dow,
			Lat: ptr(lat), Lon: ptr(lon),
			LatGCJ: ptr(geo.Quantize(gLat, 6)), LonGCJ: ptr(geo.Quantize(gLon, 6)),
			LatRaw: ptr(lat), LonRaw: ptr(lon),
			CoordFrame: res.Frame, Method: res.Method, DistM: ptr(res.DistM),
			Confidence: res.Confidence,
			Note:       optStr(ci.Get(rec, "备注")),
			TagsRaw:    optStr(ci.Get(rec, "标签")),
			Tags:       csvin.SplitTags(ci.Get(rec, "标签")),
			DedupKey:   dk, RowNo: rowNo, Line: line,
		}
		if lat == 0 && lon == 0 {
			v.Lat, v.Lon, v.LatGCJ, v.LonGCJ = nil, nil, nil, nil
		}
		out = append(out, v)
		st.Ready++
	}

	if frameFixed > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("检测到 %d 条到访坐标疑似未做坐标系换算（与地点表的 GCJ-02 原值重合），已自动纠正；原始坐标保留可回溯", frameFixed))
	}
	return out, rep
}

// ---------- 交通 ----------

func (s *Service) parseTrips(f *csvin.File, d *csvin.DetectResult, rep *Report,
	resolver *PlaceResolver, anchors *timeIndex, opt Options) ([]stagedTrip, *Report) {
	ci := d.Mapping
	st := s.stat(csvin.KindTrips, rep)
	var out []stagedTrip
	seen := map[string]bool{}
	var stubs int

	for i, rec := range f.Records {
		rowNo := i + 1
		line := f.LineOf[i]

		mode := strings.TrimSpace(ci.Get(rec, "类型"))
		from, errF := csvin.ParseTime(ci.Get(rec, "从"), s.tz)
		if errF != nil {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "从", Field: "from_at",
				Value: ci.Get(rec, "从"), Severity: csvin.SevError, Code: "ERR_TIME_PARSE",
				Message: fmt.Sprintf("出发时刻无法解析：%v", errF), Hint: "期望形如 2026-10-23T17:16:26+0800",
			})
			continue
		}
		to, errT := csvin.ParseTime(ci.Get(rec, "至"), s.tz)
		if errT != nil {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "至", Field: "to_at",
				Value: ci.Get(rec, "至"), Severity: csvin.SevError, Code: "ERR_TIME_PARSE",
				Message: fmt.Sprintf("到达时刻无法解析：%v", errT), Hint: "期望形如 2026-10-23T17:31:31+0800",
			})
			continue
		}
		if to.Unix < from.Unix {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "至", Field: "to_at",
				Value: ci.Get(rec, "至"), Severity: csvin.SevError, Code: "ERR_TIME_INVERTED",
				Message: "到达时刻早于出发时刻", Hint: "请修正 CSV",
			})
			continue
		}
		dur := to.Unix - from.Unix

		// 声明时长与起止时间差的一致性判定。
		//
		// 为什么用 1 秒容差而不是严格相等：Rond 导出时把「时长 (s)」按整秒
		// 截断，起止时刻也按整秒存储，两边各自截断一次就会差 1 秒。实测
		// 5143 行里有 2386 行差 1 秒、0 行差 2 秒以上——严格相等会把
		// 2386 条纯噪声报成告警，占全部 issue 的 88%，真正需要人看的
		// 问题会被淹没。容差取 1 秒：差 2 秒以上才算声明与实际对不上。
		const durationTolerance = 1
		var declared *int64
		if v, ok := csvin.ParseInt(ci.Get(rec, "时长 (s)")); ok {
			declared = ptr(int64(v))
			if d := int64(v) - dur; d > durationTolerance || d < -durationTolerance {
				st.Warned++
				rep = s.addIssue(rep, f.Kind, csvin.Issue{
					Line: line, RowNo: rowNo, Column: "时长 (s)", Field: "duration_declared_s",
					Value:    fmt.Sprintf("声明 %ds，实际 %ds", v, dur),
					Severity: csvin.SevWarn, Code: "WARN_DURATION_MISMATCH",
					Message: "声明时长与起止时间差不一致，以起止时间为准", Hint: "导出工具的舍入差异，通常无害",
				})
			}
		}

		fromName := strings.TrimSpace(ci.Get(rec, "出发地"))
		toName := strings.TrimSpace(ci.Get(rec, "目的地"))
		var fr, tr ResolveResult
		var fIssues, tIssues []csvin.Issue
		if fromName != "" {
			// 「从」用出发时刻的离开锚点：Rond 的 trips.from_at 与那一段停留的
			// departed_at 是同一秒（见 timeline.go 的实测说明）。
			fr, fIssues = resolver.ResolveNameAt(fromName, anchors.departAnchor(from.Unix), opt.CreateStubs)
		} else {
			fr = ResolveResult{Method: "empty"}
		}
		if toName != "" {
			tr, tIssues = resolver.ResolveNameAt(toName, anchors.arriveAnchor(to.Unix), opt.CreateStubs)
		} else {
			tr = ResolveResult{Method: "empty"}
		}
		for _, ri := range append(fIssues, tIssues...) {
			ri.Line, ri.RowNo = line, rowNo
			st.Warned++
			rep = s.addIssue(rep, f.Kind, ri)
		}
		if fr.Created || tr.Created {
			stubs++
		}

		dk := tripDedupKey(mode, fromName, toName, from.Unix, to.Unix)
		if seen[dk] {
			st.DupSkip++
			continue
		}
		seen[dk] = true

		out = append(out, stagedTrip{
			Mode: mode, ModeRaw: mode,
			FromAt: from.Unix, ToAt: to.Unix,
			FromLocal: from.Local, ToLocal: to.Local,
			LocalDate: from.Date, LocalMonth: from.Month, TZ: opt.Timezone,
			DurationS: dur, DurationDeclaredS: declared,
			FromPlaceKey: fr.PlaceKey, ToPlaceKey: tr.PlaceKey,
			FromNameRaw: fromName, ToNameRaw: toName,
			FromOrigin: originOf(fr), ToOrigin: originOf(tr),
			DedupKey: dk, RowNo: rowNo, Line: line,
		})
		st.Ready++
	}
	if stubs > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("交通记录中有 %d 处地点的名称不在地点库里，已建「仅引用」桩地点（无坐标，不进足迹地图）", stubs))
	}
	return out, rep
}

// ---------- 天气 ----------

func (s *Service) parseWeather(f *csvin.File, d *csvin.DetectResult, rep *Report,
	resolver *PlaceResolver, anchors *timeIndex, opt Options, existing map[string]stagedWeather) ([]stagedWeather, *Report) {
	ci := d.Mapping
	st := s.stat(csvin.KindWeather, rep)
	var out []stagedWeather
	byKey := map[string]int{} // dedupKey -> out 下标
	var identical, conflict int
	var stubs int

	for i, rec := range f.Records {
		rowNo := i + 1
		line := f.LineOf[i]

		tv, err := csvin.ParseTime(ci.Get(rec, "日期"), s.tz)
		if err != nil {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "日期", Field: "hour_ts",
				Value: ci.Get(rec, "日期"), Severity: csvin.SevError, Code: "ERR_TIME_PARSE",
				Message: fmt.Sprintf("日期无法解析：%v", err), Hint: "期望形如 2026-09-27T17:00:00+0800",
			})
			continue
		}
		hourTS := csvin.FloorHour(tv.Unix)

		name := strings.TrimSpace(ci.Get(rec, "地点"))
		// 天气按整点记，用同一小时内的到访当锚点。
		res, resIssues := resolver.ResolveNameAt(name, anchors.arriveAnchor(hourTS), opt.CreateStubs)
		for _, ri := range resIssues {
			ri.Line, ri.RowNo = line, rowNo
			st.Warned++
			rep = s.addIssue(rep, f.Kind, ri)
		}
		if res.Created {
			stubs++
		}

		w := stagedWeather{
			PlaceKey: res.PlaceKey, PlaceNameRaw: name,
			HourTS: hourTS, HourLocal: time.Unix(hourTS, 0).In(s.tz).Format("2006-01-02T15:04:05-07:00"),
			LocalDate: tv.Date, LocalMonth: tv.Month,
			Condition:  optStr(ci.Get(rec, "天气状况")),
			PrecipType: optStr(ci.Get(rec, "降水类型")),
			UVLevel:    optStr(ci.Get(rec, "紫外线等级")),
			WindDir:    optStr(ci.Get(rec, "风向")),
			RowNo:      rowNo, Line: line,
		}
		// 温度等是 float32 导出噪声，统一抹平后再比对，否则冲突裁决会
		// 出现「22.34 vs 22.34000015258789」这种无意义差异。
		w.TempC = qf(ci.Get(rec, "温度 (°C)"), 4)
		w.FeelsLikeC = qf(ci.Get(rec, "体感温度 (°C)"), 4)
		w.DewPointC = qf(ci.Get(rec, "露点 (°C)"), 4)
		w.Humidity = qf(ci.Get(rec, "湿度"), 6)
		w.PrecipMM = qf(ci.Get(rec, "降水量 (mm)"), 2)
		w.PrecipProb = qf(ci.Get(rec, "降水概率"), 4)
		w.WindSpeedMS = qf(ci.Get(rec, "风速 (m/s)"), 2)
		w.VisibilityKm = qf(ci.Get(rec, "能见度 (km)"), 2)
		if v, ok := csvin.ParseInt(ci.Get(rec, "紫外线指数")); ok {
			w.UVIndex = ptr(v)
		}

		if w.TempC != nil && (*w.TempC < -60 || *w.TempC > 60) {
			st.Failed++
			rep = s.addIssue(rep, f.Kind, csvin.Issue{
				Line: line, RowNo: rowNo, Column: "温度 (°C)", Field: "temp_c",
				Value: ci.Get(rec, "温度 (°C)"), Severity: csvin.SevError, Code: "ERR_TEMP_RANGE",
				Message: "温度超出合理范围（-60~60°C）", Hint: "检查导出是否损坏",
			})
			continue
		}

		dk := weatherDedupKey(res.PlaceKey, hourTS)
		w.DedupKey = dk

		// 三级重复判定：① 库内已有 → 比对内容；② 本批次内重复 → 比对内容；
		// ③ 全新 → 直接入列。内容一致自动跳过，内容不同进裁决列表。
		old, hasOld := existing[dk]
		if !hasOld {
			if j, dup := byKey[dk]; dup {
				old, hasOld = out[j], true
			}
		}
		if hasOld {
			diffs := diffWeather(old, w)
			if len(diffs) == 0 {
				identical++
				st.DupSkip++
				continue
			}
			conflict++
			st.DupSkip++
			rep.Conflicts = append(rep.Conflicts, ConflictGroup{
				Key: dk, Kind: csvin.KindWeather, PlaceKey: res.PlaceKey, PlaceName: name,
				Hour: w.HourLocal, Diffs: diffs,
			})
			continue
		}
		byKey[dk] = len(out)
		out = append(out, w)
		st.Ready++
	}

	if identical > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("天气数据有 %d 组「同地点同时刻」的重复行，内容完全一致，将自动跳过", identical))
	}
	if conflict > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("天气数据有 %d 组「同地点同时刻」的内容冲突，需要你逐组裁决（默认沿用已导入的值）", conflict))
	}
	if stubs > 0 {
		rep.Highlights = append(rep.Highlights,
			fmt.Sprintf("天气数据中有 %d 处地点名不在地点库里，已建「仅引用」桩地点", stubs))
	}
	return out, rep
}

// ---------- 与库内记录比对 ----------

func (s *Service) dropExistingDups(ctx context.Context, rep *Report,
	visits []stagedVisit, trips []stagedTrip) ([]stagedVisit, []stagedTrip, *Report) {

	if len(visits) > 0 {
		hit, err := s.existingKeys(ctx, "visits", "dedup_key", visitKeys(visits))
		if err == nil && len(hit) > 0 {
			kept := make([]stagedVisit, 0, len(visits))
			for _, v := range visits {
				if hit[v.DedupKey] {
					continue
				}
				kept = append(kept, v)
			}
			n := len(visits) - len(kept)
			if st := s.stat(csvin.KindVisits, rep); st != nil {
				st.DupSkip += n
				st.Ready = len(kept)
			}
			visits = kept
			rep.Highlights = append(rep.Highlights,
				fmt.Sprintf("到访记录中有 %d 条已经导入过（按 地点+抵达+离开+坐标 判定），将跳过", n))
		}
	}
	if len(trips) > 0 {
		hit, err := s.existingKeys(ctx, "trips", "dedup_key", tripKeys(trips))
		if err == nil && len(hit) > 0 {
			kept := make([]stagedTrip, 0, len(trips))
			for _, t := range trips {
				if hit[t.DedupKey] {
					continue
				}
				kept = append(kept, t)
			}
			n := len(trips) - len(kept)
			if st := s.stat(csvin.KindTrips, rep); st != nil {
				st.DupSkip += n
				st.Ready = len(kept)
			}
			trips = kept
			rep.Highlights = append(rep.Highlights,
				fmt.Sprintf("交通记录中有 %d 条已经导入过（按 出发地+目的地+出发+抵达+交通方式 判定），将跳过", n))
		}
	}
	// 地点与天气的重复在各自的 parse 阶段就已按内容逐组判过并 continue，
	// 不进这里：天气需要区分「内容一致自动跳过」和「内容冲突待裁决」，
	// 混进统一的 dup 计数会让裁决组的来源说不清。
	//
	// Totals.DupSkip 也不在这里累加——它由 Preview 末尾的汇总循环统一
	// 从各 FileStat 累加，这里再加一遍会翻番。
	return visits, trips, rep
}

// existingKeys 分批查出这批键里哪些已经存在于库内。
//
// 返回命中的键集合而不是计数：调用方要按键剔除暂存记录，光知道个数没用。
func (s *Service) existingKeys(ctx context.Context, table, col string, keys []string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(keys) == 0 {
		return out, nil
	}
	const batch = 400
	for i := 0; i < len(keys); i += batch {
		end := i + batch
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[i:end]
		q := fmt.Sprintf(`SELECT %s FROM %s WHERE %s IN (%s)`,
			col, table, col, placeholders(len(chunk)))
		args := make([]any, len(chunk))
		for j, k := range chunk {
			args[j] = k
		}
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			out[k] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func placeholders(n int) string {
	b := strings.Builder{}
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	return b.String()
}

// ---------- 审计落库 ----------

// savePreview 把预检结果写进审计表。
//
// 文件名/大小/校验和一律取自 decoded（csvin.File 自带），**不再按下标回查
// 上传列表**：解码失败的文件不会进 decoded，一旦按下标配对，审计行就会与
// 真实文件错位（曾出现「空文件.csv」带着地点文件的 sha256 与行数）。
func (s *Service) savePreview(ctx context.Context, batchNo string, rep *Report,
	decoded []*csvin.File, detects []*csvin.DetectResult) error {
	optJSON, _ := json.Marshal(rep.Options)
	statsJSON, _ := json.Marshal(rep.Totals)
	reportJSON, _ := json.Marshal(rep)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO imports(batch_no, status, options_json, stats_json, report_json,
			rows_total, created_at) VALUES(?,?,?,?,?,?,unixepoch())`,
		batchNo, StatusPreview, string(optJSON), string(statsJSON), string(reportJSON), rep.Totals.Rows); err != nil {
		return err
	}
	var importID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM imports WHERE batch_no=?`, batchNo).Scan(&importID); err != nil {
		return err
	}

	for i, f := range decoded {
		if i >= len(detects) {
			break
		}
		hdr, _ := json.Marshal(f.Header)
		mapJSON, _ := json.Marshal(detects[i].Mapping)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO imports_files(import_id, kind, file_name, file_size, sha256,
				header_json, mapping_json, rows_total, staged_path)
			 VALUES(?,?,?,?,?,?,?,?,?)`,
			importID, string(f.Kind), f.Name, f.Size, f.Sha256,
			string(hdr), string(mapJSON), len(f.Records),
			filepath.Join(s.stageDir(batchNo), stageFileName(f.Kind))); err != nil {
			return err
		}
	}
	for _, iss := range rep.Issues {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO import_issues(import_id, kind, line, row_no, col, field, value,
				severity, code, message, hint) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			importID, iss.Kind, iss.Line, iss.RowNo, iss.Column, iss.Field,
			truncate(iss.Value, 500), iss.Severity, iss.Code, iss.Message, iss.Hint); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- 小工具 ----------

func ptr[T any](v T) *T { return &v }

// optStr 把空串转成 nil，避免把「没填」和「填了空串」混为一谈。
func optStr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func qf(s string, digits int) *float64 {
	v, ok := csvin.ParseFloat(s)
	if !ok {
		return nil
	}
	return ptr(geo.Quantize(v, digits))
}

func originOf(r ResolveResult) string {
	switch r.Method {
	case "empty":
		return "empty"
	case "created":
		return "stub"
	}
	return "resolved"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// newBatchNo 生成批次号：时间前缀 + 随机后缀，可读且唯一。
func newBatchNo() string {
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hashString(fmt.Sprint(time.Now().UnixNano()))[:6])
}
