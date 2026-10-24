package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func runMigrations(ctx context.Context, db *sql.DB) error {
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("枚举迁移文件失败: %w", err)
	}
	sort.Strings(files)

	current, err := currentVersion(ctx, db)
	if err != nil {
		return err
	}
	for _, f := range files {
		ver, err := extractVersion(f)
		if err != nil {
			return err
		}
		if ver <= current {
			continue
		}
		data, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", f, err)
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("执行 %s 失败: %w", f, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO schema_version(version) VALUES(?)`, ver); err != nil {
			return fmt.Errorf("记录 %s 版本号失败: %w", f, err)
		}
		log.Printf("[migrate] 已应用 %s（schema 版本 %d）", f, ver)
	}
	return nil
}

func currentVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	// 首次运行时 schema_version 还不存在，这不是错误。
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v); err != nil {
		if isNoSuchTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	return v, nil
}

func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// extractVersion 从 migrations/001_init.sql 之类的文件名里取出前导数字。
func extractVersion(name string) (int, error) {
	base := name
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.SplitN(base, "_", 2)[0]
	n, err := strconv.Atoi(base)
	if err != nil {
		return 0, fmt.Errorf("迁移文件名 %q 缺少数字前缀: %w", name, err)
	}
	return n, nil
}
