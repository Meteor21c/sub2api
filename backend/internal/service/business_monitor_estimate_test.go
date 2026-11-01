package service

import (
	"math"
	"testing"
)

func TestBusinessMonitorOverviewPreservesKnownRevenueAndCost(t *testing.T) {
	known := BusinessMonitorWallet{Revenue: 111, RequestCount: 100, ExpectedCost: businessAmount(78), GrossProfit: businessAmount(33)}
	unknown := BusinessMonitorWallet{Revenue: .07, RequestCount: 10, Partial: true}
	s := businessSummarize([]BusinessMonitorWallet{known, unknown})
	// The complete-ledger result still cannot call the unknown channel free.
	if s.ExpectedCost != nil || s.GrossProfit != nil {
		t.Fatal("unknown cost became a complete total")
	}
	e := s.Estimate
	if e.Cost == nil || *e.Cost != 78 || e.GrossProfit == nil || *e.GrossProfit != 33 || e.Revenue != 111 || e.UncoveredRevenue != .07 || e.CoveredRequests != 100 || e.UncoveredRequests != 10 {
		t.Fatalf("one small unknown channel blanked the overview: %+v", e)
	}
	for _, pair := range [][2]BusinessMonitorWallet{{known, unknown}, {unknown, known}} {
		merged := pair[0]
		businessMergeWallet(&merged, pair[1])
		got := businessWalletEstimate(merged)
		if got.Cost == nil || *got.Cost != 78 || got.GrossProfit == nil || *got.GrossProfit != 33 || got.UncoveredRequests != 10 {
			t.Fatalf("period merge lost coverage: %+v", got)
		}
	}
}

func TestBusinessMonitorOverviewExtractsCoveredPartsOfMixedWallet(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 12, RequestCount: 3, ExpectedCost: businessAmount(0), SubscriptionExpectedCost: businessAmount(0)}
	businessApplyProbeCost(&w, businessAccount{RateStatus: "ok", ProbeRate: businessAmount(.15)}, BusinessMonitorCostBasis{StandardCost: 10, Revenue: 2, RequestCount: 1, UsageKind: "text"})
	businessApplyProbeCost(&w, businessAccount{RateStatus: "unsupported"}, BusinessMonitorCostBasis{StandardCost: 20, Revenue: 4, RequestCount: 1, UsageKind: "text"})
	businessApplyProbeCost(&w, businessAccount{RateStatus: "failed"}, BusinessMonitorCostBasis{StandardCost: 30, Revenue: 6, RequestCount: 1, UsageKind: "video"})
	businessFinalizeWallet(&w)
	e := businessWalletEstimate(w)
	if e.Cost == nil || *e.Cost != 1.5 || e.GrossProfit == nil || *e.GrossProfit != .5 || e.Revenue != 2 || e.UncoveredRevenue != 10 || e.UncoveredRequests != 2 {
		t.Fatalf("mixed-wallet missing costs hid known entries or inflated profit: %+v", e)
	}
}

func TestBusinessMonitorOverviewUnknownIsNotZeroAndZeroProbeIsValid(t *testing.T) {
	for _, status := range []string{"failed", "stale", "unsupported"} {
		w := BusinessMonitorWallet{Revenue: 5, RequestCount: 1, CostBasis: []BusinessMonitorCostBasis{{Revenue: 5, StandardCost: 20, ProbeRate: businessAmount(.15), RateStatus: status, RequestCount: 1}}}
		s := businessSummarize([]BusinessMonitorWallet{{}, w})
		if s.Estimate.Cost != nil || s.Estimate.GrossProfit != nil || s.Estimate.UncoveredRevenue != 5 {
			t.Fatalf("%s or idle wallet manufactured free cost: %+v", status, s.Estimate)
		}
	}
	w := BusinessMonitorWallet{Revenue: 2, RequestCount: 1, CostBasis: []BusinessMonitorCostBasis{{Revenue: 2, StandardCost: 10, ProbeRate: businessAmount(0), RateStatus: "ok", RequestCount: 1}}}
	e := businessWalletEstimate(w)
	if e.Cost == nil || *e.Cost != 0 || e.GrossProfit == nil || *e.GrossProfit != 2 {
		t.Fatalf("actual free probe was rejected: %+v", e)
	}
}

func TestBusinessMonitorOverviewSubscriptionsStaySeparate(t *testing.T) {
	w := BusinessMonitorWallet{Revenue: 2, SubscriptionUsage: 30, RequestCount: 2, CostBasis: []BusinessMonitorCostBasis{{Revenue: 2, StandardCost: 110, SubscriptionStandardCost: 100, ProbeRate: businessAmount(.15), RateStatus: "ok", RequestCount: 2}}}
	e := businessWalletEstimate(w)
	if e.Cost == nil || math.Abs(*e.Cost-1.5) > 1e-9 || e.GrossProfit == nil || math.Abs(*e.GrossProfit-.5) > 1e-9 {
		t.Fatalf("subscription usage was counted as per-request income or cost: %+v", e)
	}
}
