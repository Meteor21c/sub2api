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
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,credentials JSONB,extra JSONB,deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,rate_multiplier NUMERIC,deleted_at TIMESTAMPTZ);
CREATE TABLE usage_logs(account_id BIGINT,actual_cost NUMERIC,billing_type INT,account_stats_cost NUMERIC,total_cost NUMERIC,account_rate_multiplier NUMERIC,created_at TIMESTAMPTZ);
ALTER TABLE usage_logs ADD COLUMN image_count INT DEFAULT 0, ADD COLUMN video_count INT DEFAULT 0, ADD COLUMN image_output_tokens INT DEFAULT 0, ADD COLUMN billing_mode TEXT, ADD COLUMN inbound_endpoint TEXT;
INSERT INTO groups(id,name,rate_multiplier) VALUES(1,'test group',0.2);
INSERT INTO accounts(id,name,credentials,extra) VALUES(1,'test supplier','{"base_url":"https://Supplier.Example.Test/"}','{}');`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO usage_logs(account_id,actual_cost,billing_type,account_stats_cost,total_cost,account_rate_multiplier,created_at) VALUES(1,2,0,10,10,1,$1)`, businessDay(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_build_object('upstream_billing_probe',jsonb_build_object('status','ok','fresh_until',$1::text,'data',jsonb_build_object('billing_scope','token','resolved_rate_multiplier',0.15,'peak_rate_enabled',false))) WHERE id=1`, time.Now().Add(time.Hour).Format(time.RFC3339Nano))
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
	if report.Summary.Revenue != 2 || report.Summary.ExpectedCost == nil || *report.Summary.ExpectedCost != 1.5 || report.Summary.GrossProfit == nil || *report.Summary.GrossProfit != 0.5 {
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
	for _, status := range []string{"stale", "failed", "unsupported", "unknown"} {
		if _, err := db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{upstream_billing_probe,status}',to_jsonb($1::text)) WHERE id=1`, status); err != nil {
			t.Fatal(err)
		}
		report, err := s.Report(ctx, "day", "")
		if err != nil {
			t.Fatal(err)
		}
		if report.Summary.Revenue != 2 || report.Summary.ExpectedCost != nil || report.Summary.GrossProfit != nil {
			t.Fatalf("status %s incorrectly fell back to logged multiplier 1: %+v", status, report.Summary)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{upstream_billing_probe,status}','"ok"'::jsonb) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	yesterday := businessDay(time.Now()).AddDate(0, 0, -1)
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_logs(account_id,actual_cost,billing_type,account_stats_cost,total_cost,account_rate_multiplier,created_at) VALUES(1,2,0,10,10,1,$1)`, yesterday); err != nil {
		t.Fatal(err)
	}
	legacy := BusinessMonitorWallet{Key: "https://supplier.example.test", Revenue: 2, RequestCount: 1, ExpectedCost: businessTestFloat(10), GrossProfit: businessTestFloat(-8)}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO business_monitor_daily(day,wallet_key,payload) VALUES($1,$2,$3)`, yesterday.Format("2006-01-02"), legacy.Key, string(payload)); err != nil {
		t.Fatal(err)
	}
	if repaired, err := s.repairLegacyCostDay(ctx); err != nil || !repaired {
		t.Fatalf("legacy repair: %v %v", repaired, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT payload FROM business_monitor_daily WHERE day=$1 AND wallet_key=$2`, yesterday.Format("2006-01-02"), legacy.Key).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Revenue != 2 || legacy.ExpectedCost == nil || *legacy.ExpectedCost != 1.5 || legacy.GrossProfit == nil || *legacy.GrossProfit != 0.5 || legacy.CostVersion != businessCostVersion {
		t.Fatalf("saved daily repair changed income or used local multiplier: %+v", legacy)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts(id,name,credentials,extra) VALUES(2,'media supplier','{"base_url":"https://supplier.example.test"}','{"upstream_billing_probe":{"status":"unsupported"}}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_logs(account_id,actual_cost,billing_type,total_cost,created_at,image_count,video_count,billing_mode,inbound_endpoint) VALUES
(2,4,0,20,$1,1,0,'image','/v1/images/generations'),
(2,6,0,30,$1,0,1,'token','/v1/videos/generations')`, businessDay(time.Now())); err != nil {
		t.Fatal(err)
	}
	report, err = s.Report(ctx, "day", "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Revenue != 12 || report.Summary.ExcludedMediaRevenue != 10 || report.Summary.ExcludedMediaRequests != 2 || report.Summary.ExpectedCost == nil || *report.Summary.ExpectedCost != 1.5 || report.Summary.GrossProfit == nil || *report.Summary.GrossProfit != .5 {
		t.Fatalf("unknown image/video contaminated known text profit: %+v", report.Summary)
	}
	if len(report.Wallets) != 1 || len(report.Wallets[0].AccountIDs) != 2 || report.Wallets[0].Deviation != nil || report.Wallets[0].ObservedProfit != nil {
		t.Fatalf("shared media wallet double counted channels or compared unlike balances: %+v", report.Wallets)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_logs(account_id,actual_cost,billing_type,total_cost,created_at,image_count,billing_mode) VALUES(1,2,0,10,$1,1,'image')`, businessDay(time.Now())); err != nil {
		t.Fatal(err)
	}
	report, err = s.Report(ctx, "day", "")
	if err != nil || report.Summary.ExpectedCost == nil || *report.Summary.ExpectedCost != 3 || report.Summary.GrossProfit == nil || *report.Summary.GrossProfit != 1 || report.Summary.ExcludedMediaRequests != 2 {
		t.Fatalf("valid-probe media was wrongly excluded: report=%+v err=%v", report, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_logs(account_id,actual_cost,billing_type,total_cost,created_at,billing_mode) VALUES(2,1,0,10,$1,'token')`, businessDay(time.Now())); err != nil {
		t.Fatal(err)
	}
	report, err = s.Report(ctx, "day", "")
	if err != nil || report.Summary.ExpectedCost != nil || report.Summary.GrossProfit != nil || report.Summary.ExcludedMediaRequests != 2 {
		t.Fatalf("unknown text usage must not be silently excluded: report=%+v err=%v", report, err)
	}
}
