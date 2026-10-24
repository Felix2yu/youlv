package importer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Felix2yu/youlv/internal/csvin"
)

// 暂存记录结构。
//
// 预检阶段把「已解析 + 已归一化 + 已校验 + 已解析出地点键」的结果序列化落盘，
// 提交阶段只读这批文件，绝不重新解析 CSV。
//
// 这样设计的理由是信任：如果提交时重新解析一遍，用户在预检报告里看到的
// 「将插入 4280 条」就可能和实际写入数不一致——这是导入功能最伤信任的 bug。

type stagedPlace struct {
	SourceKey   string   `json:"sk"`
	Name        string   `json:"n"`
	DisplayName string   `json:"dn"`
	Lat         *float64 `json:"lat"`
	Lon         *float64 `json:"lon"`
	LatGCJ      *float64 `json:"latg"`
	LonGCJ      *float64 `json:"long"`
	CoordFrame  string   `json:"cf"`
	RadiusM     *float64 `json:"rad"`
	Rating      *float64 `json:"rat"`
	Country     *string  `json:"ctry"`
	AdminArea   *string  `json:"adm"`
	SubAdmin    *string  `json:"sadm"`
	City        *string  `json:"city"`
	District    *string  `json:"dist"`
	Street      *string  `json:"st"`
	HouseNumber *string  `json:"hn"`
	Origin      string   `json:"org"`
	IsStub      bool     `json:"stub"`
	// Promote 表示这条地点要把库里同名的「仅引用」桩升级为正式地点。
	//
	// 升级时 source_key 保持桩的原值不变——visits 的 dedup_key 里嵌着
	// placeKey，改 key 会让历史到访的去重键全部失配，重导同一份文件时
	// 整批重复插入（症状是数据悄悄翻倍而不报错）。
	Promote bool `json:"promote,omitempty"`
}

type stagedVisit struct {
	PlaceKey      string   `json:"pk"`
	PlaceNameRaw  string   `json:"pn"`
	ActivityRaw   string   `json:"act"`
	ArrivedAt     int64    `json:"arr"`
	DepartedAt    *int64   `json:"dep"`
	DurationS     *int64   `json:"dur"`
	TZ            string   `json:"tz"`
	ArrivedLocal  string   `json:"al"`
	DepartedLocal string   `json:"dl"`
	LocalDate     string   `json:"ld"`
	LocalMonth    string   `json:"lm"`
	LocalYear     int      `json:"ly"`
	LocalHour     int      `json:"lh"`
	LocalDow      int      `json:"lw"`
	Lat           *float64 `json:"lat"`
	Lon           *float64 `json:"lon"`
	LatGCJ        *float64 `json:"latg"`
	LonGCJ        *float64 `json:"long"`
	LatRaw        *float64 `json:"latr"`
	LonRaw        *float64 `json:"lonr"`
	CoordFrame    string   `json:"cf"`
	Method        string   `json:"meth"`
	DistM         *float64 `json:"dist"`
	Confidence    string   `json:"conf"`
	Note          *string  `json:"note"`
	TagsRaw       *string  `json:"tr"`
	Tags          []string `json:"tags"`
	DedupKey      string   `json:"dk"`
	RowNo         int      `json:"rn"`
	Line          int      `json:"ln"`
}

type stagedTrip struct {
	Mode              string `json:"mo"`
	ModeRaw           string `json:"mor"`
	FromAt            int64  `json:"fa"`
	ToAt              int64  `json:"ta"`
	FromLocal         string `json:"fl"`
	ToLocal           string `json:"tl"`
	LocalDate         string `json:"ld"`
	LocalMonth        string `json:"lm"`
	TZ                string `json:"tz"`
	DurationS         int64  `json:"dur"`
	DurationDeclaredS *int64 `json:"dd"`
	FromPlaceKey      string `json:"fpk"`
	ToPlaceKey        string `json:"tpk"`
	FromNameRaw       string `json:"fn"`
	ToNameRaw         string `json:"tn"`
	FromOrigin        string `json:"fo"`
	ToOrigin          string `json:"to"`
	DedupKey          string `json:"dk"`
	RowNo             int    `json:"rn"`
	Line              int    `json:"ln"`
}

type stagedWeather struct {
	PlaceKey     string   `json:"pk"`
	PlaceNameRaw string   `json:"pn"`
	HourTS       int64    `json:"ht"`
	HourLocal    string   `json:"hl"`
	LocalDate    string   `json:"ld"`
	LocalMonth   string   `json:"lm"`
	Condition    *string  `json:"cond"`
	TempC        *float64 `json:"temp"`
	FeelsLikeC   *float64 `json:"feel"`
	DewPointC    *float64 `json:"dew"`
	Humidity     *float64 `json:"hum"`
	PrecipType   *string  `json:"pt"`
	PrecipMM     *float64 `json:"pmm"`
	PrecipProb   *float64 `json:"pp"`
	UVIndex      *int     `json:"uv"`
	UVLevel      *string  `json:"uvl"`
	WindDir      *string  `json:"wd"`
	WindSpeedMS  *float64 `json:"ws"`
	VisibilityKm *float64 `json:"vis"`
	RawJSON      string   `json:"raw"`
	DedupKey     string   `json:"dk"`
	RowNo        int      `json:"rn"`
	Line         int      `json:"ln"`
}

// 暂存文件名与类型的对应关系。
func stageFileName(k csvin.Kind) string { return string(k) + ".staged.jsonl" }

// stageDir 返回该批次的暂存目录。
func (s *Service) stageDir(batchNo string) string {
	return filepath.Join(s.tmpDir, batchNo)
}

// writeStaged 把一批记录写成 NDJSON。
func writeStaged[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建暂存目录失败: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建暂存文件失败: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("写入暂存记录失败: %w", err)
		}
	}
	return w.Flush()
}

// readStaged 读回一批 NDJSON 记录。
func readStaged[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 该文件类型本批次没上传
		}
		return nil, fmt.Errorf("打开暂存文件失败: %w", err)
	}
	defer f.Close()
	var out []T
	dec := json.NewDecoder(bufio.NewReader(f))
	for {
		var v T
		if err := dec.Decode(&v); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("解析暂存记录失败: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

// visitDedupKey 生成到访的去重键。
//
// 用哈希而非裸三元组，是因为 departed_at 可能为 NULL，而 SQLite 的 UNIQUE
// 对 NULL 不生效——裸三元组会被「两条都无离开时刻」的记录击穿。
func visitDedupKey(placeKey string, arrived int64, departed *int64, lat, lon float64) string {
	d := int64(-1)
	if departed != nil {
		d = *departed
	}
	return hashString(fmt.Sprintf("v|%s|%d|%d|%.6f,%.6f", placeKey, arrived, d, lat, lon))
}

func tripDedupKey(mode, fromName, toName string, fromAt, toAt int64) string {
	return hashString(fmt.Sprintf("t|%s|%s|%s|%d|%d", mode, fromName, toName, fromAt, toAt))
}

func weatherDedupKey(placeKey string, hourTS int64) string {
	return hashString(fmt.Sprintf("w|%s|%d", placeKey, hourTS))
}

// cleanupStaged 清理过期暂存目录。服务启动时调用一次。
func cleanupStaged(tmpDir string, maxAge time.Duration) int {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return 0
	}
	cut := time.Now().Add(-maxAge)
	var n int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cut) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(tmpDir, e.Name())); err == nil {
			n++
		}
	}
	return n
}
