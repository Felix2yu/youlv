// Package testdb 仅供测试使用：建临时文件库并跑到最新 schema。
//
// 用文件而非 :memory: 是刻意的——WAL、外键、部分唯一索引在内存库上的行为
// 与文件库并不完全一致，而导入管线的回滚逻辑恰恰依赖这些特性。
package testdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/yufei/youlv/internal/db"
)

// New 返回已迁移到最新版本的临时库，测试结束时自动关闭。
func New(t *testing.T) *sql.DB {
	t.Helper()
	// 局部变量不能叫 db —— 那会遮蔽同名包，db.Migrate 就找不到了。
	conn, err := db.Open(filepath.Join(t.TempDir(), "youlv.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("迁移测试库: %v", err)
	}
	return conn
}
