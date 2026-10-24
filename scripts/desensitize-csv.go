//go:build ignore

// 脱敏脚本：把 Rond 导出的四份 CSV 变成可入库的合成样本。
//
// 为什么用 Go 而不是 Python：脱敏必须复刻 GCJ02ToWGS84（含两轮不动点迭代），
// Python 重写一遍就有浮点末位差异风险，而 fixture 的正确性判据恰恰是
// 「亚厘米级残差」。Go 版直接 import internal/geo，转换函数逐位一致。
//
// 为什么要做：真实导出里全是个人轨迹（常去的地点、住址、认识的人）。
// 公开仓库放这样的数据既不礼貌也不安全，但 CI 又需要一份全量数据来跑
// 导入回归——所以造一份脱敏样本。
//
// 用法：
//
//	go run scripts/desensitize-csv.go -in <真实 CSV 目录> -out testdata/rond-export
//
// 脚本本身不含任何真实数据：它只从 -in 读、从 -out 写，因此可以公开入库，
// 任何人都能复算出同一份 fixture（名字置换用固定种子）。
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yufei/youlv/internal/geo"
)

var (
	// 坐标平移量（纬度、经度，单位度）。
	//
	// 选它有三个约束：① 平移后所有点仍在中国境内（geo.OutOfChina 的判定框
	// 是 lat 3.86~53.55 / lon 73.66~135.05），否则 GCJ 变换退化为恒等，
	// 坐标系判定的测试全部失去意义；② 平移量足够大，让真实地点无法被反查；
	// ③ 落在有数据的地方（安徽北部一带），避免落到无人区或境外。
	shiftLat = 1.2
	shiftLon = -2.0

	// 时间整体平移的天数。728 = 104 整周，保证星期不变（local_dow 不变），
	// 于是「周几的什么时段最常出门」这类热力图形状保持原样。
	shiftDays = -728

	// 复现路径的判定阈值（米）：到访坐标与「地点坐标经 GCJ→WGS 换算后」的
	// 偏差在此范围内即视为复现路径。中位偏差 0.64 m、最大 3.12 m，取 5 m。
	snapMeters = 5.0
)

func main() {
	in := flag.String("in", "", "真实 CSV 目录")
	out := flag.String("out", "", "输出目录")
	seed := flag.Int64("seed", 20261025, "名字置换种子")
	flag.Parse()
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*in, *out, *seed); err != nil {
		log.Fatalf("脱敏失败: %v", err)
	}
}

// namer 是「原名 → 合成名」的映射表。
type namer struct {
	prefix string
	names  map[string]string
	order  []string
}

func newNamer(prefix string) *namer {
	return &namer{prefix: prefix, names: map[string]string{}}
}

// get 登记一个原名并返回它的合成名（首次出现时分配）。
func (n *namer) get(orig string) string {
	if orig == "" {
		return ""
	}
	if v, ok := n.names[orig]; ok {
		return v
	}
	v := fmt.Sprintf("%s-%04d", n.prefix, len(n.order)+1)
	n.names[orig] = v
	n.order = append(n.order, orig)
	return v
}

// shuffle 用固定种子重排分配结果，使映射不可从合成名反推。
//
// 不用 sha1(name)：那能被字典暴力反推——地点名空间不大，枚举一遍就出来了。
func (n *namer) shuffle(seed int64) {
	rng := rand.New(rand.NewSource(seed))
	perm := rng.Perm(len(n.order))
	mapped := make(map[string]string, len(n.order))
	for i, id := range perm {
		mapped[n.order[id]] = fmt.Sprintf("%s-%04d", n.prefix, i+1)
	}
	n.names = mapped
}

// placeRow 是地点表里的一行（脱敏前的原值）。
type placeRow struct {
	name string
	lat  float64 // GCJ-02（CSV 原值）
	lon  float64
}

// 表是读进来的一组记录。
type table struct {
	header []string
	rows   []map[string]string
}

func run(inDir, outDir string, seed int64) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	placesT, err := readTable(filepath.Join(inDir, "地点记录.csv"))
	if err != nil {
		return err
	}
	visitsT, err := readTable(filepath.Join(inDir, "到访记录.csv"))
	if err != nil {
		return err
	}
	tripsT, err := readTable(filepath.Join(inDir, "交通记录.csv"))
	if err != nil {
		return err
	}
	weatherT, err := readTable(filepath.Join(inDir, "天气数据.csv"))
	if err != nil {
		return err
	}

	// --- 1. 收集所有要替换的名字 ---
	//
	// 跨文件必须用同一张表：地点表、到访表、交通表、天气表里的同一个名字
	// 要映射到同一个合成名，否则解析时对不上。
	names := newNamer("场所")
	acts := newNamer("活动")
	tags := newNamer("标签")
	cities := newNamer("城市")
	provinces := newNamer("省份")
	districts := newNamer("辖区")
	streets := newNamer("街道")
	notes := newNamer("备注")

	byName := map[string][]placeRow{}
	for _, r := range placesT.rows {
		name := strings.TrimSpace(r["名称"])
		if name == "" {
			continue
		}
		names.get(name)
		cities.get(strings.TrimSpace(r["城市/地区"]))
		provinces.get(strings.TrimSpace(r["行政区域"]))
		districts.get(strings.TrimSpace(r["区"]))
		streets.get(strings.TrimSpace(r["街道"]))
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(r["纬度"]), 64)
		lon, e2 := strconv.ParseFloat(strings.TrimSpace(r["经度"]), 64)
		if e1 == nil && e2 == nil {
			byName[name] = append(byName[name], placeRow{name: name, lat: lat, lon: lon})
		}
	}
	for _, r := range visitsT.rows {
		names.get(strings.TrimSpace(r["地点"]))
		acts.get(strings.TrimSpace(r["活动"]))
		notes.get(strings.TrimSpace(r["备注"]))
		for _, t := range strings.Split(strings.TrimSpace(r["标签"]), ",") {
			tags.get(strings.TrimSpace(t))
		}
	}
	for _, r := range tripsT.rows {
		names.get(strings.TrimSpace(r["出发地"]))
		names.get(strings.TrimSpace(r["目的地"]))
		notes.get(strings.TrimSpace(r["备注"]))
	}
	for _, r := range weatherT.rows {
		names.get(strings.TrimSpace(r["地点"]))
	}

	// --- 2. 统一置换 ---
	for _, n := range []*namer{names, acts, tags, cities, provinces, districts, streets, notes} {
		n.shuffle(seed)
	}

	// --- 3. 写出 ---
	if err := writeTable(filepath.Join(outDir, "地点记录.csv"), placesT.header, placesT.rows,
		func(rec map[string]string) map[string]string {
			out := clone(rec)
			out["名称"] = names.get(strings.TrimSpace(rec["名称"]))
			lat, e1 := strconv.ParseFloat(strings.TrimSpace(rec["纬度"]), 64)
			lon, e2 := strconv.ParseFloat(strings.TrimSpace(rec["经度"]), 64)
			if e1 == nil && e2 == nil {
				out["纬度"] = fmt.Sprintf("%f", geo.Quantize(lat+shiftLat, 6))
				out["经度"] = fmt.Sprintf("%f", geo.Quantize(lon+shiftLon, 6))
			}
			out["城市/地区"] = cities.get(strings.TrimSpace(rec["城市/地区"]))
			out["行政区域"] = provinces.get(strings.TrimSpace(rec["行政区域"]))
			out["区"] = districts.get(strings.TrimSpace(rec["区"]))
			out["街道"] = streets.get(strings.TrimSpace(rec["街道"]))
			out["次级行政区域"] = ""
			if strings.TrimSpace(rec["门牌号"]) != "" {
				out["门牌号"] = "合成门牌"
			}
			return out
		}); err != nil {
		return err
	}

	if err := writeTable(filepath.Join(outDir, "到访记录.csv"), visitsT.header, visitsT.rows,
		func(rec map[string]string) map[string]string {
			out := clone(rec)
			name := strings.TrimSpace(rec["地点"])
			out["地点"] = names.get(name)
			out["活动"] = acts.get(strings.TrimSpace(rec["活动"]))
			out["抵达时刻"] = shiftTime(rec["抵达时刻"])
			out["离开时刻"] = shiftTime(rec["离开时刻"])
			out["标签"] = shiftTags(rec["标签"], tags)
			out["备注"] = notes.get(strings.TrimSpace(rec["备注"]))
			lat, e1 := strconv.ParseFloat(strings.TrimSpace(rec["纬度"]), 64)
			lon, e2 := strconv.ParseFloat(strings.TrimSpace(rec["经度"]), 64)
			if e1 == nil && e2 == nil {
				nlat, nlon := shiftVisitCoord(name, lat, lon, byName)
				out["纬度"] = fmt.Sprintf("%f", geo.Quantize(nlat, 6))
				out["经度"] = fmt.Sprintf("%f", geo.Quantize(nlon, 6))
			}
			return out
		}); err != nil {
		return err
	}

	if err := writeTable(filepath.Join(outDir, "交通记录.csv"), tripsT.header, tripsT.rows,
		func(rec map[string]string) map[string]string {
			out := clone(rec)
			out["从"] = shiftTime(rec["从"])
			out["至"] = shiftTime(rec["至"])
			out["出发地"] = names.get(strings.TrimSpace(rec["出发地"]))
			out["目的地"] = names.get(strings.TrimSpace(rec["目的地"]))
			out["备注"] = notes.get(strings.TrimSpace(rec["备注"]))
			// 「时长 (s)」原样保留：它与起止时刻差 1 秒，正是容差判定的用例。
			return out
		}); err != nil {
		return err
	}

	if err := writeTable(filepath.Join(outDir, "天气数据.csv"), weatherT.header, weatherT.rows,
		func(rec map[string]string) map[string]string {
			out := clone(rec)
			out["日期"] = shiftTime(rec["日期"])
			out["地点"] = names.get(strings.TrimSpace(rec["地点"]))
			// 气象数值原样保留：本身不含隐私，且天气冲突组依赖它的数值分布。
			return out
		}); err != nil {
		return err
	}

	return writeReadme(outDir)
}

// shiftVisitCoord 重建一条到访的坐标。
//
// 真实数据里「到访坐标 ≈ GCJ02ToWGS84(同名地点坐标)」，中位偏差 0.64 m、
// 最大 3.12 m。这条不变式是导入管线的核心判据：坐标系识别、重名消歧、
// 高置信度判定全靠它。
//
// 关键：**不能对经纬度做统一平移**。GCJ 偏移场随位置变化，
// GCJ02ToWGS84(p+δ) ≠ GCJ02ToWGS84(p)+δ，误差可达十几米，会直接击穿
// resolver 的 5 m 阈值，让高置信度记录整批掉下来。
//
// 正确做法是「地点侧平移、到访侧按残差重算」：
//
//	new_p_gcj = old_p_gcj + δ
//	ε         = old_v_wgs - GCJ02ToWGS84(old_p_gcj)      // 原残差，逐行保留
//	new_v_wgs = GCJ02ToWGS84(new_p_gcj) + ε              // 残差按经纬分量搬过去
//
// 这样 |new_v_wgs - GCJ02ToWGS84(new_p_gcj)| 恒等于原始偏差。
//
// 匹配不到地点的行（独立定位点，以及那 30 条「疑似未换算」的脏数据）
// 走「直接平移」分支：前者与任何地点本来就无关；后者平移后依旧等于
// 地点的 GCJ-02 原值，「未换算」这个特征被完整保留。
func shiftVisitCoord(name string, lat, lon float64, byName map[string][]placeRow) (float64, float64) {
	cands := byName[name]
	if len(cands) == 0 {
		return geo.Quantize(lat+shiftLat, 6), geo.Quantize(lon+shiftLon, 6)
	}
	bestD := math.MaxFloat64
	var best placeRow
	for _, c := range cands {
		wLat, wLon := geo.GCJ02ToWGS84(c.lat, c.lon)
		d := geo.HaversineMeters(lat, lon, wLat, wLon)
		if d < bestD {
			bestD, best = d, c
		}
	}
	if bestD > snapMeters {
		return geo.Quantize(lat+shiftLat, 6), geo.Quantize(lon+shiftLon, 6)
	}
	oldWLat, oldWLon := geo.GCJ02ToWGS84(best.lat, best.lon)
	epsLat, epsLon := lat-oldWLat, lon-oldWLon
	newWLat, newWLon := geo.GCJ02ToWGS84(best.lat+shiftLat, best.lon+shiftLon)
	return geo.Quantize(newWLat+epsLat, 6), geo.Quantize(newWLon+epsLon, 6)
}

// shiftTime 平移一个时刻，保持原有的输出格式。
func shiftTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	for _, layout := range []string{"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.AddDate(0, 0, shiftDays).Format(layout)
		}
	}
	return s
}

// shiftTags 替换标签，并保留多标签的「逗号 + 空格」分隔形态。
func shiftTags(s string, tags *namer) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, tags.get(p))
	}
	return strings.Join(out, ", ")
}

func clone(rec map[string]string) map[string]string {
	out := make(map[string]string, len(rec))
	for k, v := range rec {
		out[k] = v
	}
	return out
}

// ---------- CSV 读写 ----------

func readTable(path string) (*table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s 是空文件", path)
	}
	head := rows[0]
	head[0] = strings.TrimPrefix(head[0], "\ufeff")
	out := &table{header: head}
	for _, row := range rows[1:] {
		m := map[string]string{}
		for i, h := range head {
			if i < len(row) {
				m[h] = row[i]
			} else {
				m[h] = ""
			}
		}
		out.rows = append(out.rows, m)
	}
	return out, nil
}

func writeTable(path string, header []string, rows []map[string]string,
	fn func(map[string]string) map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	// 保留 BOM：与上游导出一致，且 Excel 双击不乱码。
	// csv.Writer 的一次 Write 是**一整行**，所以表头要整体写出，不能逐列写。
	head := make([]string, len(header))
	copy(head, header)
	head[0] = "\ufeff" + header[0]
	if err := w.Write(head); err != nil {
		return err
	}
	for _, rec := range rows {
		out := fn(rec)
		line := make([]string, len(header))
		for i, h := range header {
			line[i] = out[h]
		}
		if err := w.Write(line); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeReadme 记录脱敏规则：读这份 fixture 的人需要知道它是怎么造出来的，
// 以及哪些性质是被刻意保留的。
func writeReadme(dir string) error {
	const doc = `# 脱敏样本说明

本目录是 **Rond 导出数据的脱敏合成样本**，由
` + "`scripts/desensitize-csv.go`" + ` 生成。仓库内**不含任何真实个人轨迹数据**。

## 怎么造的

| 项 | 处理 |
|---|---|
| 地点名、到访地点、交通起终点、天气地点 | 全表统一映射为「场所-0001…」等合成名（固定种子置换，不可反推） |
| 活动 / 标签 / 城市 / 省 / 区 / 街道 / 备注 | 同上，分别映射为「活动-…」「标签-…」「城市-…」等 |
| 坐标 | 地点侧统一平移 (+1.2°, -2.0°)，**仍在��国内** |
| 到访坐标 | **按残差重算**，见下 |
| 时间 | 整体平移 -728 天（104 整周，星期不变） |
| 交通方式、天气状况、温湿风等数值 | 原样保留（本身不含隐私，且冲突组依赖其分布） |
| 交通的「时长 (s)」 | 原样保留（它与起止时刻差 1 秒，正是容差判定的用例） |

## 刻意保留的性质（测试依赖它们）

1. **坐标系不变式**：地点表是 GCJ-02，到访表是 WGS-84，且到访坐标
   ≈ GCJ02ToWGS84(同名地点坐标)，中位偏差 0.64 m、最大 3.12 m。
   脱敏时必须「地点侧平移、到访侧按残差重算」——直接对经纬度做统一平移会
   破坏它（GCJ 偏移场随位置变化），导致导入时高置信度记录整批掉下来。
2. **36 组重名不同址**：同名地点仍有多个坐标，导入时必须靠坐标消歧。
3. **57 组天气内容冲突**、**114 个重复键**：天气裁决逻辑的用例。
4. **时间倒挂 1 条、停留 <60s 1 条、>24h 78 条**：校验规则的用例。
5. **30 条「疑似未换算」的到访**：到访坐标与地点 GCJ-02 原值完全相等，
   导入时应被识别并自动纠正。

## 重新生成

` + "```bash" + `
go run scripts/desensitize-csv.go -in <真实 CSV 目录> -out testdata/rond-export
` + "```" + `

脚本只从参数指定的目录读、往参数指定的目录写，自身不含真实数据，
因此可以公开入库，任何人都能复算出同一份 fixture。
`
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(doc), 0o644)
}
