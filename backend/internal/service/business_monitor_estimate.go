package service

import "math"

// The overview sums the covered portion independently of the complete-ledger
// fields. Missing costs exclude their matching revenue, never the whole report.
// Subscription usage remains separate from per-request wallet consumption.
type BusinessMonitorEstimate struct {
	Revenue           float64  `json:"revenue"`
	Cost              *float64 `json:"cost"`
	GrossProfit       *float64 `json:"gross_profit"`
	CoveredRequests   int64    `json:"covered_requests"`
	UncoveredRevenue  float64  `json:"uncovered_revenue"`
	UncoveredRequests int64    `json:"uncovered_requests"`
}

func businessWalletEstimate(w BusinessMonitorWallet) BusinessMonitorEstimate {
	if w.Estimate != nil {
		return *w.Estimate
	}
	e := BusinessMonitorEstimate{UncoveredRevenue: w.Revenue, UncoveredRequests: w.RequestCount}
	// Complete saved estimates remain usable even after their raw usage expires.
	if w.ExpectedCost != nil && w.GrossProfit != nil && !businessOnlyExcludedMedia(&w) {
		e.Revenue = w.Revenue - w.ExcludedMediaRevenue
		e.Cost = businessAmount(e.Revenue - *w.GrossProfit)
		e.CoveredRequests = w.RequestCount - w.ExcludedMediaRequests
	} else {
		cost := 0.0
		for _, b := range w.CostBasis {
			if b.Excluded || b.RateStatus != "ok" || b.ProbeRate == nil || *b.ProbeRate < 0 || math.IsNaN(*b.ProbeRate) || math.IsInf(*b.ProbeRate, 0) {
				continue
			}
			part := (b.StandardCost - b.SubscriptionStandardCost) * *b.ProbeRate
			if math.IsNaN(part) || math.IsInf(part, 0) || part < -0.000001 {
				continue
			}
			cost += math.Max(0, part)
			e.Revenue += b.Revenue
			e.CoveredRequests += b.RequestCount
			e.Cost = businessAmount(cost)
		}
		if w.RequestCount == 0 && w.Revenue == 0 && w.SubscriptionUsage == 0 {
			e.Cost = businessAmount(0)
		}
	}
	e.UncoveredRevenue = w.Revenue - e.Revenue
	if math.Abs(e.UncoveredRevenue) < 0.000001 {
		e.UncoveredRevenue = 0
	}
	e.UncoveredRequests = w.RequestCount - e.CoveredRequests
	if e.Cost != nil {
		e.GrossProfit = businessAmount(e.Revenue - *e.Cost)
	}
	return e
}

func businessMergeEstimate(to *BusinessMonitorEstimate, from BusinessMonitorEstimate) {
	if to.CoveredRequests == 0 && to.Revenue == 0 && to.Cost != nil && *to.Cost == 0 {
		to.Cost, to.GrossProfit = nil, nil
	}
	to.Revenue += from.Revenue
	to.CoveredRequests += from.CoveredRequests
	to.UncoveredRevenue += from.UncoveredRevenue
	to.UncoveredRequests += from.UncoveredRequests
	// No usage in an idle wallet cannot establish cost coverage by itself.
	if from.Cost != nil && (from.CoveredRequests > 0 || from.Revenue != 0 || *from.Cost != 0) {
		cost := *from.Cost
		if to.Cost != nil {
			cost += *to.Cost
		}
		to.Cost = businessAmount(cost)
	}
	if to.Cost != nil {
		to.GrossProfit = businessAmount(to.Revenue - *to.Cost)
	}
}
