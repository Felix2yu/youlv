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

// runMigrations 应用尚未执行的迁移。
//
// 整个过程跑在**一条专属连接**的 BEGIN IMMEDIATE 事务里，原因有两个：
//  1. 原子性：DDL 与「记录版本号」必须是同一事务。否则进程在两者之间崩掉，
//     重启后会重放该迁移——001 靠 IF NOT EXISTS 侥幸幂等，而后续按约定新增的
//     ALTER TABLE ADD COLUMN 会以 duplicate column 直接失败，库卡在中间态。
//  2. 并发：BEGIN IMMEDIATE 立刻拿写锁。两个进程同时指向同一个库启动时，
//     后一个会在 BEGIN 处等待而不是各跑一遍迁移。
//
// 用 conn.ExecContext 手写事务而不是 db.BeginTx：database/sql 的 BeginTx
// 只会发 BEGIN（延迟事务），拿不到上面的即时写锁。
func runMigrations(ctx context.Context, db *sql.DB) error {
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("枚举迁移文件失败: %w", err)
	}
	sort.Strings(files)

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("开始迁移事务失败: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// 出错时用 Background：ctx 可能已经取消。
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := currentVersion(ctx, conn)
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
		if _, err := conn.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("执行 %s 失败: %w", f, err)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO schema_version(version) VALUES(?)`, ver); err != nil {
			return fmt.Errorf("记录 %s 版本号失败: %w", f, err)
		}
		log.Printf("[migrate] 已应用 %s（schema 版本 %d）", f, ver)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("提交迁移事务失败: %w", err)
	}
	committed = true
	return nil
}

// querier 让版本读取既能用于 *sql.DB 也能用于单条连接。
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func currentVersion(ctx context.Context, q querier) (int, error) {
	var v int
	// 首次运行时 schema_version 还不存在，这不是错误。
	if err := q.QueryRowContext(ctx,
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
