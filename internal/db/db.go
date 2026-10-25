// Package db 封装 SQLite 的打开与迁移。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // 纯 Go 驱动，CGO_ENABLED=0 下可用
)

// Open 打开（必要时创建）数据库。
//
// PRAGMA 全部写在 DSN 里：这样每一个新建连接都会带上，不依赖调用方记得设置。
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 写入串行化；少量连接支撑并发读，写冲突由 busy_timeout 兜底。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	return db, nil
}

// OpenReadOnly 只读打开，供 backup 子命令使用。
// 与 Open 的区别：不建目录、不跑迁移、文件不存在直接报错。
//
// 只读用 URI 的 mode=ro，**不能**用 PRAGMA query_only —— 后者是连接级的
// 「禁止任何写」，会把 VACUUM INTO 也一起禁掉（它需要写目标快照，内部还会
// 做临时写入），表现为 "attempt to write a readonly database"，备份直接失败。
// mode=ro 是文件级只读：源库照样不可写，但 VACUUM INTO 能正常产出快照。
func OpenReadOnly(path string) (*sql.DB, error) {
	// sql.Open 是惰性的，Ping 会把不存在的库当成「新建」处理。备份契约要求
	// 库不存在时报错退出而不是产出空备份，所以这里必须先显式 Stat。
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("数据文件不可用: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("只读打开数据库失败（文件是否存在？）: %w", err)
	}
	return db, nil
}

// Migrate 依次应用未执行的迁移，返回当前 schema 版本。
func Migrate(ctx context.Context, db *sql.DB) (int, error) {
	if err := runMigrations(ctx, db); err != nil {
		return 0, err
	}
	v, err := currentVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	return v, nil
}
