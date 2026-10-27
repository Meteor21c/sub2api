package service

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func businessTestFloat(value float64) *float64 { return &value }

func TestBusinessMonitorURLAndAliases(t *testing.T) {
	if got := businessNormalizeURL("https://user:secret@API.Example.com/v1/?private=yes#fragment"); got != "https://api.example.com/v1" {
		t.Fatalf("unsafe or unstable normalization: %q", got)
	}
	if got := businessNormalizeURL("file:///etc/passwd"); got != "" {
		t.Fatalf("non HTTP URL accepted: %q", got)
	}
	t.Setenv("BUSINESS_MONITOR_ALIASES_JSON", `{"https://api.hajimi.chat/":"https://hajimi.chat"}`)
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "1")
	s := NewBusinessMonitorService(nil)
	defer s.Stop()
	if s.walletKey("https://api.hajimi.chat/") != "https://hajimi.chat" {
		t.Fatal("wallet alias not applied")
	}
	if !s.AccessAllowed(1) || s.AccessAllowed(2) || s.AccessAllowed(0) {
		t.Fatal("owner allowlist not enforced")
	}
}

func TestBusinessMonitorDynamicTolerance(t *testing.T) {
	for _, tc := range []struct{ revenue, cost, want float64 }{{120, 100, 3}, {102, 100, .5}, {1, .9, .2}, {0, 0, .2}, {90, 100, .2}} {
		if got := businessTolerance(tc.revenue, tc.cost); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("R=%v E=%v: got %v want %v", tc.revenue, tc.cost, got, tc.want)
		}
	}
}

func TestBusinessMonitorRechargeCandidates(t *testing.T) {
	for _, tc := range []struct {
		increase float64
		viapi    bool
		gap      time.Duration
		amount   float64
		kind     string
		ok       bool
	}{
		{-.1, false, 5 * time.Minute, 0, "", false},
		{.03, true, 5 * time.Minute, .03, "checkin_candidate", true},
		{9.8, false, 5 * time.Minute, 10, "integer_recharge_candidate", true},
		{9.8, false, time.Hour, 9.8, "credit_candidate", true},
		{8.4, false, 5 * time.Minute, 8.4, "credit_candidate", true},
	} {
		amount, kind, ok := businessCreditCandidate(tc.increase, tc.viapi, tc.gap)
		if math.Abs(amount-tc.amount) > 1e-9 || kind != tc.kind || ok != tc.ok {
			t.Errorf("candidate(%v,%v,%v)=(%v,%s,%v)", tc.increase, tc.viapi, tc.gap, amount, kind, ok)
		}
	}
}

func TestBusinessMonitorShanghaiPeriod(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 30, 0, 0, time.UTC)
	start, end, err := businessPeriod("week", "2026-10-25", now)
	if err != nil || start.Format("2006-01-02") != "2026-10-19" || end.Format("2006-01-02") != "2026-10-26" {
		t.Fatalf("incorrect Monday week: %v %v %v", start, end, err)
	}
	start, end, err = businessPeriod("day", "", time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC))
	if err != nil || start.Format("2006-01-02") != "2026-10-27" || end.Sub(start) != 24*time.Hour {
		t.Fatalf("incorrect Shanghai day: %v %v %v", start, end, err)
	}
	if _, _, err = businessPeriod("year", "2026-02-31", now); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestBusinessMonitorIncompleteBalanceNeverUsesWholeDayIncome(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, ExpectedCost: 80, BalanceEnd: businessTestFloat(50), Partial: true}
	businessFinalizeWallet(&w)
	if w.BalanceCost != nil || w.ObservedProfit != nil || w.Deviation != nil {
		t.Fatal("missing baseline incorrectly produced a financial result")
	}
	if w.GrossProfit != 20 {
		t.Fatalf("local estimate lost: %v", w.GrossProfit)
	}
}

func TestBusinessMonitorInferredRechargeDoesNotConfirmLoss(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, ExpectedCost: 80, BalanceStart: businessTestFloat(100), BalanceEnd: businessTestFloat(10), Credits: 30, InferredCredits: 30, BalanceComplete: true}
	businessFinalizeWallet(&w)
	if !w.Partial || w.Status == "loss" || w.Status == "overspend" {
		t.Fatalf("unconfirmed credit produced certain anomaly: %+v", w)
	}
	if w.BalanceCost == nil || *w.BalanceCost != 120 {
		t.Fatal("candidate balance cost should remain visible as an estimate")
	}
}

func TestBusinessMonitorSubscriptionIsNotCashRevenue(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, SubscriptionUsage: 50, ExpectedCost: 110, SubscriptionExpectedCost: 30, BalanceStart: businessTestFloat(200), BalanceEnd: businessTestFloat(90)}
	businessFinalizeWallet(&w)
	if w.GrossProfit != 20 || w.ObservedProfit != nil || !w.Partial {
		t.Fatalf("subscription recognized as cash or false loss: %+v", w)
	}
}

func TestBusinessMonitorDeviationAndNegativeMargin(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, ExpectedCost: 80, BalanceStart: businessTestFloat(200), BalanceEnd: businessTestFloat(117)}
	businessFinalizeWallet(&w)
	if w.Status != "overspend" || w.Deviation == nil || *w.Deviation != 3 {
		t.Fatalf("overspend missed: %+v", w)
	}
	loss := BusinessMonitorWallet{Revenue: .5, ExpectedCost: 1}
	businessFinalizeWallet(&loss)
	if loss.Status != "loss" {
		t.Fatal("negative local margin incorrectly hidden by tolerance")
	}
}

func TestBusinessMonitorAggregateUnknownWalletIsNotZero(t *testing.T) {
	wallets := []BusinessMonitorWallet{{Revenue: 10, ExpectedCost: 5, GrossProfit: 5, BalanceCost: businessTestFloat(5), ObservedProfit: businessTestFloat(5)}, {Revenue: 2, ExpectedCost: 1, GrossProfit: 1, Partial: true}}
	summary := businessSummarize(wallets)
	if summary.BalanceCost == nil || *summary.BalanceCost != 5 || summary.ObservedProfit == nil || *summary.ObservedProfit != 5 || !summary.Partial {
		t.Fatalf("covered subtotal or incomplete coverage lost: %+v", summary)
	}
	if summary.Revenue != 12 || summary.ExpectedCost != 6 {
		t.Fatal("known local totals not preserved")
	}
}

func TestBusinessMonitorHistoricalCreditCorrectionPreservesSavedRevenue(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewBusinessMonitorService(db)
	defer s.Stop()
	date := "2026-01-01"
	key := "https://api.example.com"
	w := BusinessMonitorWallet{Key: key, Revenue: 123, ExpectedCost: 100, GrossProfit: 23, BalanceStart: businessTestFloat(200), BalanceEnd: businessTestFloat(105), BalanceComplete: true, InferredCredits: 10, Credits: 10, Partial: true, AccountIDs: []int64{1}, Messages: []string{}}
	payload, _ := json.Marshal(w)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM business_monitor_samples WHERE wallet_key=$1 UNION ALL SELECT 1 FROM business_monitor_daily WHERE wallet_key=$1)`)).WithArgs(key).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("INSERT INTO business_monitor_credit_overrides").WithArgs(key, date, 10.0, "").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT payload FROM business_monitor_daily").WithArgs(date, key).WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(payload))
	mock.ExpectExec("UPDATE business_monitor_daily SET payload").WithArgs(date, key, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	if err = s.SetCreditOverride(context.Background(), key, date, businessTestFloat(10), ""); err != nil {
		t.Fatal(err)
	}
	// In particular, no usage_logs query is allowed: these records may already
	// have expired while the durable daily summary is still valid.
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBusinessMonitorPeriodPreservesCoveredDayTotals(t *testing.T) {
	to := BusinessMonitorWallet{Revenue: 20, ExpectedCost: 15, GrossProfit: 5, Partial: true}
	from := BusinessMonitorWallet{Revenue: 30, ExpectedCost: 20, GrossProfit: 10, BalanceCost: businessTestFloat(21), ObservedProfit: businessTestFloat(9), Deviation: businessTestFloat(1), BalanceComplete: true}
	businessMergeWallet(&to, from)
	if !to.Partial || to.BalanceCost == nil || *to.BalanceCost != 21 || to.ObservedProfit == nil || *to.ObservedProfit != 9 {
		t.Fatalf("covered day accounting lost or replaced by full-period income: %+v", to)
	}
	if to.Revenue != 50 || to.ExpectedCost != 35 {
		t.Fatal("local period totals lost")
	}
}
