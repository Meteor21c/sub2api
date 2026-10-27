package service

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
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
	w := BusinessMonitorWallet{Revenue: 100, ExpectedCost: businessTestFloat(80), SubscriptionExpectedCost: businessTestFloat(0), BalanceEnd: businessTestFloat(50), Partial: true}
	businessFinalizeWallet(&w)
	if w.BalanceCost != nil || w.ObservedProfit != nil || w.Deviation != nil {
		t.Fatal("missing baseline incorrectly produced a financial result")
	}
	if w.GrossProfit == nil || *w.GrossProfit != 20 {
		t.Fatalf("local estimate lost: %v", w.GrossProfit)
	}
}

func TestBusinessMonitorInferredRechargeDoesNotConfirmLoss(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, ExpectedCost: businessTestFloat(80), SubscriptionExpectedCost: businessTestFloat(0), BalanceStart: businessTestFloat(100), BalanceEnd: businessTestFloat(10), Credits: 30, InferredCredits: 30, BalanceComplete: true}
	businessFinalizeWallet(&w)
	if !w.Partial || w.Status == "loss" || w.Status == "overspend" {
		t.Fatalf("unconfirmed credit produced certain anomaly: %+v", w)
	}
	if w.BalanceCost == nil || *w.BalanceCost != 120 {
		t.Fatal("candidate balance cost should remain visible as an estimate")
	}
}

func TestBusinessMonitorSubscriptionIsNotCashRevenue(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, SubscriptionUsage: 50, ExpectedCost: businessTestFloat(110), SubscriptionExpectedCost: businessTestFloat(30), BalanceStart: businessTestFloat(200), BalanceEnd: businessTestFloat(90)}
	businessFinalizeWallet(&w)
	if w.GrossProfit == nil || *w.GrossProfit != 20 || w.ObservedProfit != nil || !w.Partial {
		t.Fatalf("subscription recognized as cash or false loss: %+v", w)
	}
}

func TestBusinessMonitorDeviationAndNegativeMargin(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 100, ExpectedCost: businessTestFloat(80), SubscriptionExpectedCost: businessTestFloat(0), BalanceStart: businessTestFloat(200), BalanceEnd: businessTestFloat(117)}
	businessFinalizeWallet(&w)
	if w.Status != "overspend" || w.Deviation == nil || *w.Deviation != 3 {
		t.Fatalf("overspend missed: %+v", w)
	}
	loss := BusinessMonitorWallet{Revenue: .5, ExpectedCost: businessTestFloat(1), SubscriptionExpectedCost: businessTestFloat(0)}
	businessFinalizeWallet(&loss)
	if loss.Status != "loss" {
		t.Fatal("negative local margin incorrectly hidden by tolerance")
	}
}

func TestBusinessMonitorAggregateUnknownWalletIsNotZero(t *testing.T) {
	wallets := []BusinessMonitorWallet{{Revenue: 10, ExpectedCost: businessTestFloat(5), GrossProfit: businessTestFloat(5), BalanceCost: businessTestFloat(5), ObservedProfit: businessTestFloat(5)}, {Revenue: 2, ExpectedCost: businessTestFloat(1), GrossProfit: businessTestFloat(1), Partial: true}}
	summary := businessSummarize(wallets)
	if summary.BalanceCost == nil || *summary.BalanceCost != 5 || summary.ObservedProfit == nil || *summary.ObservedProfit != 5 || !summary.Partial {
		t.Fatalf("covered subtotal or incomplete coverage lost: %+v", summary)
	}
	if summary.Revenue != 12 || summary.ExpectedCost == nil || *summary.ExpectedCost != 6 {
		t.Fatal("known local totals not preserved")
	}
}

func TestBusinessMonitorHistoricalCreditCorrectionPreservesSavedRevenue(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := NewBusinessMonitorService(db)
	defer s.Stop()
	date := "2026-01-01"
	key := "https://api.example.com"
	w := BusinessMonitorWallet{Key: key, Revenue: 123, ExpectedCost: businessTestFloat(100), SubscriptionExpectedCost: businessTestFloat(0), GrossProfit: businessTestFloat(23), CostVersion: businessCostVersion, BalanceStart: businessTestFloat(200), BalanceEnd: businessTestFloat(105), BalanceComplete: true, InferredCredits: 10, Credits: 10, Partial: true, AccountIDs: []int64{1}, Messages: []string{}}
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
	to := BusinessMonitorWallet{Revenue: 20, ExpectedCost: businessTestFloat(15), GrossProfit: businessTestFloat(5), Partial: true}
	from := BusinessMonitorWallet{Revenue: 30, ExpectedCost: businessTestFloat(20), GrossProfit: businessTestFloat(10), BalanceCost: businessTestFloat(21), ObservedProfit: businessTestFloat(9), Deviation: businessTestFloat(1), BalanceComplete: true}
	businessMergeWallet(&to, from)
	if !to.Partial || to.BalanceCost == nil || *to.BalanceCost != 21 || to.ObservedProfit == nil || *to.ObservedProfit != 9 {
		t.Fatalf("covered day accounting lost or replaced by full-period income: %+v", to)
	}
	if to.Revenue != 50 || to.ExpectedCost == nil || *to.ExpectedCost != 35 {
		t.Fatal("local period totals lost")
	}
}

func TestBusinessMonitorProbeCostNeverDefaultsToOne(t *testing.T) {
	for _, status := range []string{"ok", "stale", "failed", "unsupported", "unknown", "invalid"} {
		t.Run(status, func(t *testing.T) {
			w := BusinessMonitorWallet{Revenue: 2, ExpectedCost: businessTestFloat(0), SubscriptionExpectedCost: businessTestFloat(0)}
			a := businessAccount{ID: 1, Name: "viapi", RateStatus: status, ProbeRate: businessTestFloat(0.15)}
			businessApplyProbeCost(&w, a, BusinessMonitorCostBasis{StandardCost: 10})
			businessFinalizeWallet(&w)
			if status == "ok" {
				if w.ExpectedCost == nil || *w.ExpectedCost != 1.5 || w.GrossProfit == nil || *w.GrossProfit != 0.5 {
					t.Fatalf("did not use 0.15 probe: %+v", w)
				}
			} else if w.ExpectedCost != nil || w.GrossProfit != nil || w.Deviation != nil || w.Status == "loss" || !w.Partial {
				t.Fatalf("unavailable probe manufactured cost/profit: %+v", w)
			}
		})
	}
}

func TestBusinessMonitorMissingProbeDoesNotCreateAggregateProfit(t *testing.T) {
	known := BusinessMonitorWallet{Revenue: 2, ExpectedCost: businessTestFloat(1.5), GrossProfit: businessTestFloat(0.5)}
	unknown := BusinessMonitorWallet{Revenue: 3, Partial: true}
	summary := businessSummarize([]BusinessMonitorWallet{known, unknown})
	if summary.Revenue != 5 || summary.ExpectedCost != nil || summary.GrossProfit != nil || !summary.Partial {
		t.Fatalf("unknown cost became free usage: %+v", summary)
	}
	businessMergeWallet(&known, unknown)
	if known.Revenue != 5 || known.ExpectedCost != nil || known.GrossProfit != nil {
		t.Fatalf("period ignored an unknown day: %+v", known)
	}
}

func TestBusinessMonitorLegacyRepairPreservesIncome(t *testing.T) {
	legacy := BusinessMonitorWallet{Revenue: 2, RequestCount: 1, ExpectedCost: businessTestFloat(10), SubscriptionExpectedCost: businessTestFloat(0), GrossProfit: businessTestFloat(-8), Messages: []string{"viapi 本日记账平均倍率 1.0000", "本站配置估算毛利为负"}}
	recalculated := BusinessMonitorWallet{Revenue: 2, RequestCount: 1, ExpectedCost: businessTestFloat(1.5), SubscriptionExpectedCost: businessTestFloat(0), RateStatus: "ok"}
	saved := legacy
	businessRepairCost(&saved, &recalculated)
	if saved.Revenue != 2 || saved.ExpectedCost == nil || *saved.ExpectedCost != 1.5 || saved.GrossProfit == nil || *saved.GrossProfit != 0.5 || saved.CostVersion != businessCostVersion {
		t.Fatalf("incorrect legacy correction: %+v", saved)
	}
	for _, message := range saved.Messages {
		if strings.Contains(message, "本日记账平均倍率") || strings.Contains(message, "本站配置估算毛利为负") {
			t.Fatalf("stale warning retained: %s", message)
		}
	}
	for _, missing := range []*BusinessMonitorWallet{nil, {}, {Revenue: 1, RequestCount: 1}} {
		saved = legacy
		businessRepairCost(&saved, missing)
		if saved.Revenue != 2 || saved.ExpectedCost != nil || saved.GrossProfit != nil || !saved.Partial {
			t.Fatalf("expired logs changed saved revenue or invented cost: %+v", saved)
		}
	}
	saved = legacy
	businessInvalidateLegacyCost(&saved)
	if saved.Revenue != 2 || saved.ExpectedCost != nil || saved.GrossProfit != nil || saved.Status == "loss" {
		t.Fatalf("legacy report exposed incorrect cost during migration: %+v", saved)
	}
}

func TestBusinessMonitorUnknownMediaExcludedFromBothSides(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 12, RequestCount: 3, ExpectedCost: businessTestFloat(0), SubscriptionExpectedCost: businessTestFloat(0), BalanceStart: businessTestFloat(100), BalanceEnd: businessTestFloat(80), BalanceComplete: true}
	businessApplyProbeCost(&w, businessAccount{ID: 1, RateStatus: "ok", ProbeRate: businessTestFloat(.15)}, BusinessMonitorCostBasis{StandardCost: 10, Revenue: 2, RequestCount: 1, UsageKind: "text"})
	for _, kind := range []string{"image", "video"} {
		businessApplyProbeCost(&w, businessAccount{ID: 2, RateStatus: "unsupported"}, BusinessMonitorCostBasis{StandardCost: 25, Revenue: 5, RequestCount: 1, UsageKind: kind})
	}
	businessFinalizeWallet(&w)
	if w.Revenue != 12 || w.ExcludedMediaRevenue != 10 || w.ExcludedMediaRequests != 2 || w.ExpectedCost == nil || *w.ExpectedCost != 1.5 || w.GrossProfit == nil || *w.GrossProfit != .5 {
		t.Fatalf("excluded media revenue made known text profit too large: %+v", w)
	}
	if w.BalanceCost == nil || *w.BalanceCost != 20 || w.Deviation != nil || w.ObservedProfit != nil || w.Tolerance != nil || !w.Partial || w.Status == "overspend" {
		t.Fatalf("whole-wallet media balance was compared with text-only estimate: %+v", w)
	}
}

func TestBusinessMonitorOnlyExcludedMediaIsNotZeroCost(t *testing.T) {
	media := BusinessMonitorWallet{Revenue: 5, RequestCount: 1, ExpectedCost: businessTestFloat(0), SubscriptionExpectedCost: businessTestFloat(0)}
	businessApplyProbeCost(&media, businessAccount{RateStatus: "failed"}, BusinessMonitorCostBasis{StandardCost: 20, Revenue: 5, RequestCount: 1, UsageKind: "video"})
	businessFinalizeWallet(&media)
	if media.ExpectedCost != nil || media.GrossProfit != nil || media.Status != "excluded" {
		t.Fatalf("excluded-only wallet must not look free: %+v", media)
	}
	summary := businessSummarize([]BusinessMonitorWallet{media})
	if summary.ExpectedCost != nil || summary.GrossProfit != nil || summary.ExcludedMediaRevenue != 5 {
		t.Fatalf("excluded-only report manufactured zero cost/profit: %+v", summary)
	}
	known := BusinessMonitorWallet{Revenue: 2, RequestCount: 1, ExpectedCost: businessTestFloat(1.5), SubscriptionExpectedCost: businessTestFloat(0), GrossProfit: businessTestFloat(.5)}
	summary = businessSummarize([]BusinessMonitorWallet{media, known})
	if summary.ExpectedCost == nil || *summary.ExpectedCost != 1.5 || summary.GrossProfit == nil || *summary.GrossProfit != .5 || summary.Revenue != 7 || summary.ExcludedMediaRevenue != 5 {
		t.Fatalf("excluded-only wallet contaminated known subtotal: %+v", summary)
	}
	for _, pair := range [][2]BusinessMonitorWallet{{media, known}, {known, media}} {
		to := pair[0]
		businessMergeWallet(&to, pair[1])
		if to.ExpectedCost == nil || *to.ExpectedCost != 1.5 || to.GrossProfit == nil || *to.GrossProfit != .5 || to.ExcludedMediaRevenue != 5 || to.Status == "excluded" {
			t.Fatalf("excluded-only day contaminated known period: %+v", to)
		}
	}
	businessMergeWallet(&media, BusinessMonitorWallet{ExpectedCost: businessTestFloat(0), GrossProfit: businessTestFloat(0)})
	if media.ExpectedCost != nil || media.GrossProfit != nil {
		t.Fatal("inactive day made an all-excluded period look free")
	}
}

func TestBusinessMonitorMediaRepairKeepsOriginalConsumption(t *testing.T) {
	saved := BusinessMonitorWallet{Revenue: 12, RequestCount: 3, CostVersion: 2}
	updated := BusinessMonitorWallet{Revenue: 12, RequestCount: 3, ExcludedMediaRevenue: 10, ExcludedMediaRequests: 2, ExpectedCost: businessTestFloat(1.5), SubscriptionExpectedCost: businessTestFloat(0)}
	businessRepairCost(&saved, &updated)
	if saved.Revenue != 12 || saved.RequestCount != 3 || saved.ExcludedMediaRevenue != 10 || saved.ExcludedMediaRequests != 2 || saved.GrossProfit == nil || *saved.GrossProfit != .5 {
		t.Fatalf("media repair lost revenue or exclusion scope: %+v", saved)
	}
	knownV2 := BusinessMonitorWallet{CostVersion: 2, Revenue: 2, ExpectedCost: businessTestFloat(1.5), GrossProfit: businessTestFloat(.5)}
	businessInvalidateLegacyCost(&knownV2)
	if knownV2.ExpectedCost == nil || *knownV2.ExpectedCost != 1.5 || knownV2.CostVersion != businessCostVersion {
		t.Fatal("fully covered v2 estimate unnecessarily hidden during media migration")
	}
}
