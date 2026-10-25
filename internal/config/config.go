// Package config 读取游履的运行配置。配置一律来自环境变量，无 .env 文件。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config 是运行期配置。字段与 YOULV_* 环境变量一一对应。
type Config struct {
	Addr    string // 监听地址，默认 :8802
	DataDir string // 数据目录，默认 ./data
	DBPath  string // SQLite 路径，默认 <DataDir>/youlv.db
	TZName  string // 时区名，默认 Asia/Shanghai
	TZ      *time.Location
	Dev     bool // true 时前端回退读磁盘 web/dist
	WebDir  string
	// Token 非空时，除 /api/health 外的所有接口都要带上它。
	// 留空表示不鉴权（默认，与 1.x 行为一致）——自托管在可信内网是合理的，
	// 但一旦端口暴露到公网就是「任何人都能清空你的轨迹」，所以启动日志会明确说出来。
	Token   string
	Version string
}

const (
	DefaultAddr = ":8802"
	DefaultTZ   = "Asia/Shanghai"
)

// Resolve 只解析不建目录。backup 子命令用它：库不存在时必须报错退出，
// 而不是顺手创建一个空库产出一个空备份（契约见 .github/docs/backup.md）。
func Resolve() (*Config, error) {
	c := &Config{
		Addr:    getenv("YOULV_ADDR", DefaultAddr),
		DataDir: getenv("YOULV_DATA_DIR", "data"),
		TZName:  getenv("YOULV_TZ", DefaultTZ),
		Dev:     getenv("YOULV_DEV", "") == "1",
		WebDir:  getenv("YOULV_WEB_DIR", ""),
		Token:   getenv("YOULV_TOKEN", ""),
	}
	c.DBPath = getenv("YOULV_DB", filepath.Join(c.DataDir, "youlv.db"))

	loc, err := loadLocation(c.TZName)
	if err != nil {
		return nil, fmt.Errorf("解析时区 %q 失败: %w", c.TZName, err)
	}
	c.TZ = loc
	return c, nil
}

// Load 在服务路径使用：Resolve 之后补齐目录。
func Load() (*Config, error) {
	c, err := Resolve()
	if err != nil {
		return nil, err
	}
	for _, d := range []string{c.DataDir, filepath.Dir(c.DBPath), c.TmpDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	return c, nil
}

// TmpDir 返回导入暂存目录。预检产出的 staged 文件落在这里，
// 保证「预检之后不再重新解析 CSV」。
func (c *Config) TmpDir() string { return filepath.Join(c.DataDir, "tmp", "imports") }

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// loadLocation 解析时区，失败时退到显式构造的 +08:00 固定时区。
//
// 这里刻意不用 `import _ "time/tzdata"`：全舰队无一个仓库这样做，且内嵌
// tzdata 会给二进制增加约 450 KB。中国自 1991 年后无夏令时、游履的数据
// 始于 2024 年，FixedZone("Asia/Shanghai", 8*3600) 是精确的，同时完全不
// 依赖容器镜像里是否存在 /usr/share/zoneinfo。
func loadLocation(name string) (*time.Location, error) {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc, nil
	}
	if name == DefaultTZ || name == "" {
		return time.FixedZone("Asia/Shanghai", 8*3600), nil
	}
	return nil, fmt.Errorf("未知时区，且不是默认时区 %s", DefaultTZ)
}
