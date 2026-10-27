package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// This test only runs against the isolated PostgreSQL service created by CI.
func TestBusinessMonitorSQLIntegration(t *testing.T) {
	dsn := os.Getenv("BUSINESS_MONITOR_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL fixture not configured")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,credentials JSONB,extra JSONB,deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,rate_multiplier NUMERIC,deleted_at TIMESTAMPTZ);
CREATE TABLE usage_logs(account_id BIGINT,actual_cost NUMERIC,billing_type INT,account_stats_cost NUMERIC,total_cost NUMERIC,account_rate_multiplier NUMERIC,created_at TIMESTAMPTZ);
INSERT INTO groups(id,name,rate_multiplier) VALUES(1,'test group',0.2);
INSERT INTO accounts(id,name,credentials,extra) VALUES(1,'test supplier','{"base_url":"https://Supplier.Example.Test/"}','{}');`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO usage_logs VALUES(1,2,0,10,10,0.1,$1)`, businessDay(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		balance := 100.0
		if call > 1 {
			balance = 110.08
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"generatedAt": time.Now(),
			"accounts":    []map[string]any{{"id": "viapi", "name": "Test", "baseUrl": "https://supplier.example.test", "balance": balance, "status": "ready"}},
		})
	}))
	defer server.Close()
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", server.URL)
	s := NewBusinessMonitorService(db)
	defer s.Stop()
	report, err := s.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Revenue != 2 || report.Summary.ExpectedCost != 1 || report.Summary.GrossProfit != 1 {
		t.Fatalf("wrong accounting: %+v", report.Summary)
	}
	if report.Summary.BalanceCost != nil || report.Summary.ObservedProfit != nil || !report.Summary.Partial {
		t.Fatal("first sample must not manufacture an all-day balance profit")
	}
	s.mu.Lock()
	s.lastCollect = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if _, err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	var amount float64
	if err := db.QueryRowContext(ctx, `SELECT amount FROM business_monitor_credits LIMIT 1`).Scan(&amount); err != nil || amount != 10 {
		t.Fatalf("integer recharge candidate amount=%v err=%v", amount, err)
	}
	before := calls.Load()
	if _, err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("repeated manual clicks must reuse the recent sample")
	}
	fail.Store(true)
	s.mu.Lock()
	s.lastCollect = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if _, err := s.Refresh(ctx); err == nil {
		t.Fatal("failed balance response was treated as success")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM business_monitor_samples`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed fetch changed saved samples: count=%d err=%v", count, err)
	}
	if err := s.observeRates(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE groups SET rate_multiplier=0.25 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.observeRates(ctx); err != nil {
		t.Fatal(err)
	}
	var oldRate, newRate float64
	if err := db.QueryRowContext(ctx, `SELECT old_rate,new_rate FROM business_monitor_rate_changes WHERE kind='site' LIMIT 1`).Scan(&oldRate, &newRate); err != nil || oldRate != 0.2 || newRate != 0.25 {
		t.Fatalf("rate history old=%v new=%v err=%v", oldRate, newRate, err)
	}
}
