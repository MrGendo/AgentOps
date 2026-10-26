// Package store 封装 PostgreSQL 访问；核心表只允许经本包与领域包写入（架构文档 §4）。
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 持有 pgx 连接池。
type Store struct {
	pool *pgxpool.Pool
}

// New 建立连接池并立即 Ping，失败即返回错误（启动期暴露配置问题）。
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// Ping 实现 httpapi.Pinger，用于健康检查。
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Pool 暴露底层池供领域包开启事务；不鼓励绕过领域包直接拼 SQL。
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close 释放连接池。
func (s *Store) Close() { s.pool.Close() }
