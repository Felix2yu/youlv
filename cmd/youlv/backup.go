package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/Felix2yu/youlv/internal/config"
	"github.com/Felix2yu/youlv/internal/db"
)

// runBackup 实现 `youlv backup` 子命令。
//
// 五条契约要求（Felix2yu/.github/docs/backup.md）：
//  1. 在 main() 最前面拦截，早于任何存储初始化
//  2. 只读打开数据库，文件不存在时报错退出
//  3. VACUUM INTO 到临时目录，再把文件流式写到 stdout
//  4. 日志一律走 stderr，stdout 只承载备份数据
//  5. 命令能被容器内的 sh -c 解析
func runBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr) // 第 4 条：日志只走 stderr
	dataDir := fs.String("data", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir != "" {
		if err := os.Setenv("YOULV_DATA_DIR", *dataDir); err != nil {
			return err
		}
	}

	// Resolve 而非 Load：不建目录，库不存在就是错误。
	cfg, err := config.Resolve()
	if err != nil {
		return err
	}
	conn, err := db.OpenReadOnly(cfg.DBPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("youlv-backup-%d.db", os.Getpid()))
	defer os.Remove(tmp)

	// VACUUM INTO 产出一个已整理、无 WAL 依赖的完整快照。
	if _, err := conn.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("生成备份失败: %w", err)
	}

	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(os.Stdout, f); err != nil {
		return fmt.Errorf("写出备份失败: %w", err)
	}
	log.Printf("[backup] 已完成：%s", cfg.DBPath)
	return nil
}
