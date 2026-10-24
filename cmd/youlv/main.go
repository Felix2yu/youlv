// Command youlv 是「游履」服务：自托管的生活足迹记录应用。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Felix2yu/youlv/internal/api"
	"github.com/Felix2yu/youlv/internal/config"
	"github.com/Felix2yu/youlv/internal/db"
	"github.com/Felix2yu/youlv/internal/importer"
	"github.com/Felix2yu/youlv/internal/store"
)

func main() {
	// backup 子命令必须在最前面拦截：它要在任何存储初始化或迁移之前跑，
	// 且数据文件不存在时必须报错退出而不是产出空备份。
	// 契约见 Felix2yu/.github/docs/backup.md。
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		if err := runBackup(os.Args[2:]); err != nil {
			log.Printf("[backup] %v", err)
			os.Exit(1)
		}
		return
	}

	addr := flag.String("addr", config.DefaultAddr, "监听地址")
	dataDir := flag.String("data", "", "数据目录（默认取 YOULV_DATA_DIR 或 ./data）")
	showVersion := flag.Bool("version", false, "输出版本")
	flag.Parse()

	if *showVersion {
		fmt.Printf("youlv %s\n", version)
		return
	}
	if *dataDir != "" {
		if err := os.Setenv("YOULV_DATA_DIR", *dataDir); err != nil {
			log.Fatalf("设置数据目录失败: %v", err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	ver, err := db.Migrate(ctx, conn)
	if err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	imp := importer.New(conn, cfg.TmpDir(), cfg.TZ)
	// 清掉 7 天前的暂存目录；预检产物只用于一次提交，不必长期留存。
	if n := imp.Cleanup(7 * 24 * time.Hour); n > 0 {
		log.Printf("[import] 清理了 %d 个过期暂存目录", n)
	}

	st := store.New(conn, cfg.TZ)
	// 存量活动可能没有颜色（早期批次），启动时补一次；失败不致命，
	// 只是统计图上暂时灰着，不值得为此拦住启动。
	if n, err := st.EnsureActivityColors(ctx); err != nil {
		log.Printf("[activity] 回填活动颜色失败: %v", err)
	} else if n > 0 {
		log.Printf("[activity] 给 %d 个活动补了颜色", n)
	}

	srv := api.New(st, imp, version)

	httpSrv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      srv,
		ReadTimeout:  120 * time.Second, // 导入整批要写 1.3 万行，读超时不能太短
		WriteTimeout: 120 * time.Second,
	}
	if *addr != config.DefaultAddr {
		httpSrv.Addr = *addr
	}

	go func() {
		log.Printf("游履 %s 已启动，监听 %s（schema v%d，数据目录 %s）",
			version, httpSrv.Addr, ver, cfg.DataDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务退出: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Print("正在关闭服务…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("优雅关闭失败: %v", err)
	}
	log.Print("已停止")
}
