// cmd/migrate 是数据库迁移执行器（golang-migrate 驱动，SQL 文件位于 platform/migrations）。
// 用法：go run ./cmd/migrate -up | -down N | -status
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	dsn := flag.String("dsn", envOr("AGENTOPS_DATABASE_URL", "postgres://localhost:5432/agentops_dev?sslmode=disable"), "数据库连接串")
	dir := flag.String("dir", os.Getenv("AGENTOPS_MIGRATIONS_DIR"), "迁移目录（默认取 AGENTOPS_MIGRATIONS_DIR，未设时为 ./migrations）")
	if *dir == "" {
		*dir = "migrations"
	}
	up := flag.Bool("up", false, "升级到最新")
	down := flag.Int("down", 0, "回滚 N 个迁移")
	status := flag.Bool("status", false, "查看迁移状态")
	flag.Parse()

	// golang-migrate 的 pgx/v5 驱动要求 pgx5:// 协议头，这里做一次透明转换。
	dbURL := strings.Replace(strings.Replace(*dsn, "postgresql://", "pgx5://", 1), "postgres://", "pgx5://", 1)

	m, err := migrate.New("file://"+*dir, dbURL)
	if err != nil {
		log.Fatalf("初始化迁移失败: %v", err)
	}
	defer m.Close()

	switch {
	case *up:
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			log.Fatalf("迁移升级失败: %v", err)
		}
		fmt.Println("迁移已升级到最新")
	case *down > 0:
		if err := m.Steps(-*down); err != nil {
			log.Fatalf("迁移回滚失败: %v", err)
		}
		fmt.Printf("已回滚 %d 个迁移\n", *down)
	case *status:
		version, dirty, err := m.Version()
		if err != nil {
			log.Fatalf("读取迁移状态失败: %v", err)
		}
		fmt.Printf("当前版本: %d dirty=%v\n", version, dirty)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
