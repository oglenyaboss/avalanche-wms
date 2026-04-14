package store

import (
	"context"
	"testing"
	"time"
)

// Unit-тесты pool.go — без testcontainers. Проверяют только парсинг DSN и
// поведение ping'а на несуществующий хост (fail-fast).
// Integration coverage (реальная БД) — в onchain_events_test.go под build tag
// `integration`.

func TestNewPool_InvalidDSN(t *testing.T) {
	_, err := NewPool(context.Background(), "not-a-valid-dsn://@@")
	if err == nil {
		t.Fatal("expected error for bad DSN")
	}
}

func TestNewPool_UnreachableHost(t *testing.T) {
	// 127.0.0.1:1 — гарантированно не слушает ничего.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := NewPool(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2")
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}
