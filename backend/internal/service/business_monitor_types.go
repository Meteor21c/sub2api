package service

import "time"

// Financial amounts use the owner's configured 1:1 bookkeeping unit. They are
// operational estimates, not a substitute for a supplier settlement ledger.
type BusinessMonitorSummary struct {
	Revenue           float64  `json:"revenue"`
	SubscriptionUsage float64  `json:"subscription_usage"`
	ExpectedCost      *float64 `json:"expected_cost"`
	GrossProfit       *float64 `json:"gross_profit"`
	BalanceCost       *float64 `json:"balance_cost"`
	ObservedProfit    *float64 `json:"observed_profit"`
	Deviation         *float64 `json:"deviation"`
	AnomalyCount      int      `json:"anomaly_count"`
	Partial           bool     `json:"partial"`
}

type BusinessMonitorWallet struct {
	Key                      string                     `json:"key"`
	BaseURL                  string                     `json:"base_url"`
	Name                     string                     `json:"name"`
	AccountIDs               []int64                    `json:"account_ids"`
	Revenue                  float64                    `json:"revenue"`
	SubscriptionUsage        float64                    `json:"subscription_usage"`
	SubscriptionExpectedCost *float64                   `json:"subscription_expected_cost"`
	ExpectedCost             *float64                   `json:"expected_cost"`
	GrossProfit              *float64                   `json:"gross_profit"`
	CostVersion              int                        `json:"cost_version"`
	CostBasis                []BusinessMonitorCostBasis `json:"cost_basis"`
	BalanceStart             *float64                   `json:"balance_start"`
	BalanceEnd               *float64                   `json:"balance_end"`
	Credits                  float64                    `json:"credits"`
	InferredCredits          float64                    `json:"inferred_credits"`
	CreditOverride           *float64                   `json:"credit_override"`
	CreditNote               string                     `json:"credit_note"`
	BalanceCost              *float64                   `json:"balance_cost"`
	ObservedProfit           *float64                   `json:"observed_profit"`
	Deviation                *float64                   `json:"deviation"`
	Tolerance                *float64                   `json:"tolerance"`
	Status                   string                     `json:"status"`
	Messages                 []string                   `json:"messages"`
	LastSampleAt             *time.Time                 `json:"last_sample_at"`
	StartSampleAt            *time.Time                 `json:"start_sample_at"`
	RateStatus               string                     `json:"rate_status"`
	RequestCount             int64                      `json:"request_count"`
	Partial                  bool                       `json:"partial"`
	BalanceComplete          bool                       `json:"balance_complete"`
}

// Retain the standard usage basis and the probe used by each daily estimate.
// Account bookkeeping multipliers are deliberately not part of this contract.
type BusinessMonitorCostBasis struct {
	AccountID                int64    `json:"account_id"`
	AccountName              string   `json:"account_name"`
	StandardCost             float64  `json:"standard_cost"`
	SubscriptionStandardCost float64  `json:"subscription_standard_cost"`
	ProbeRate                *float64 `json:"probe_rate"`
	RateStatus               string   `json:"rate_status"`
}

type BusinessMonitorDaily struct {
	Date              string   `json:"date"`
	Revenue           float64  `json:"revenue"`
	SubscriptionUsage float64  `json:"subscription_usage"`
	ExpectedCost      *float64 `json:"expected_cost"`
	GrossProfit       *float64 `json:"gross_profit"`
	BalanceCost       *float64 `json:"balance_cost"`
	ObservedProfit    *float64 `json:"observed_profit"`
	Partial           bool     `json:"partial"`
}

type BusinessMonitorRateChange struct {
	ID          int64     `json:"id"`
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name"`
	Kind        string    `json:"kind"`
	OldRate     float64   `json:"old_rate"`
	NewRate     float64   `json:"new_rate"`
	ObservedAt  time.Time `json:"observed_at"`
}

type BusinessMonitorCredit struct {
	ID               int64     `json:"id"`
	WalletKey        string    `json:"wallet_key"`
	Amount           float64   `json:"amount"`
	ObservedIncrease float64   `json:"observed_increase"`
	Kind             string    `json:"kind"`
	ObservedAt       time.Time `json:"observed_at"`
	Confirmed        bool      `json:"confirmed"`
}

type BusinessMonitorReport struct {
	Period        string                      `json:"period"`
	Date          string                      `json:"date"`
	Start         time.Time                   `json:"start"`
	End           time.Time                   `json:"end"`
	AsOf          time.Time                   `json:"as_of"`
	Finalized     bool                        `json:"finalized"`
	CoverageStart *time.Time                  `json:"coverage_start"`
	Summary       BusinessMonitorSummary      `json:"summary"`
	Wallets       []BusinessMonitorWallet     `json:"wallets"`
	Daily         []BusinessMonitorDaily      `json:"daily"`
	RateChanges   []BusinessMonitorRateChange `json:"rate_changes"`
	Credits       []BusinessMonitorCredit     `json:"credits"`
}
