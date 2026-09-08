package service

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParseRechargeFeeTiersNormalizesAndSupportsLegacyMap(t *testing.T) {
	t.Parallel()

	t.Run("array is sorted by threshold", func(t *testing.T) {
		t.Parallel()
		tiers := parseRechargeFeeTiers(`[{"min_amount":100,"fee_rate":-1},{"min_amount":10,"fee_rate":3},{"min_amount":50,"fee_rate":1}]`)
		if len(tiers) != 3 {
			t.Fatalf("got %d tiers, want 3", len(tiers))
		}
		wantAmounts := []float64{10, 50, 100}
		wantRates := []float64{3, 1, -1}
		for i := range tiers {
			if tiers[i].MinAmount != wantAmounts[i] || tiers[i].FeeRate != wantRates[i] {
				t.Fatalf("tier[%d] = %#v, want amount=%v rate=%v", i, tiers[i], wantAmounts[i], wantRates[i])
			}
		}
	})

	t.Run("legacy threshold map is accepted", func(t *testing.T) {
		t.Parallel()
		tiers := parseRechargeFeeTiers(`{"10":1.03,"50":1.01,"100":0.99,"500":0.9}`)
		if len(tiers) != 4 {
			t.Fatalf("got %d tiers, want 4", len(tiers))
		}
		if tiers[0].MinAmount != 10 || tiers[0].FeeRate != 3 || tiers[3].MinAmount != 500 || tiers[3].FeeRate != -10 {
			t.Fatalf("unexpected normalized legacy tiers: %#v", tiers)
		}
	})

	t.Run("invalid JSON or values fall back to flat behavior", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{
			`{"not-a-number":3}`,
			`[{"min_amount":0,"fee_rate":3}]`,
			`[{"min_amount":10,"fee_rate":-100}]`,
			`[{"min_amount":10,"fee_rate":3},{"min_amount":10,"fee_rate":1}]`,
		} {
			if tiers := parseRechargeFeeTiers(raw); tiers != nil {
				t.Fatalf("parseRechargeFeeTiers(%q) = %#v, want nil", raw, tiers)
			}
		}
	})
}

func TestPaymentConfigFeeRateForBalanceAmount(t *testing.T) {
	t.Parallel()

	cfg := &PaymentConfig{
		RechargeFeeRate: 7,
		RechargeFeeTiers: []RechargeFeeTier{
			{MinAmount: 10, FeeRate: 3},
			{MinAmount: 50, FeeRate: 1},
			{MinAmount: 100, FeeRate: -1},
			{MinAmount: 500, FeeRate: -10},
		},
	}
	tests := []struct {
		amount float64
		want   float64
	}{
		{9.99, 7},
		{10, 3},
		{49.99, 3},
		{50, 1},
		{99.99, 1},
		{100, -1},
		{499.99, -1},
		{500, -10},
	}
	for _, tt := range tests {
		if got := cfg.FeeRateForBalanceAmount(tt.amount); got != tt.want {
			t.Errorf("FeeRateForBalanceAmount(%v) = %v, want %v", tt.amount, got, tt.want)
		}
	}

	if got := (&PaymentConfig{RechargeFeeRate: 4}).FeeRateForBalanceAmount(100); got != 4 {
		t.Fatalf("empty tiers should use flat rate, got %v", got)
	}
	if got := (*PaymentConfig)(nil).FeeRateForBalanceAmount(100); got != 0 {
		t.Fatalf("nil config should return zero, got %v", got)
	}
}

func TestParsePaymentConfigUsesEmptyArrayWhenTiersAreUnconfigured(t *testing.T) {
	t.Parallel()

	cfg := (&PaymentConfigService{}).parsePaymentConfig(map[string]string{})
	if cfg.RechargeFeeTiers == nil {
		t.Fatal("unconfigured recharge fee tiers must be an empty array, not nil")
	}
	if len(cfg.RechargeFeeTiers) != 0 {
		t.Fatalf("unconfigured recharge fee tiers = %#v, want empty", cfg.RechargeFeeTiers)
	}
}

func TestParsePaymentConfigNormalizesInvalidFlatFeeRate(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"-1", "100.001", "101", "NaN", "+Inf"} {
		cfg := (&PaymentConfigService{}).parsePaymentConfig(map[string]string{
			SettingRechargeFeeRate: raw,
		})
		if cfg.RechargeFeeRate != 0 {
			t.Fatalf("flat fee rate %q = %v, want safe fallback 0", raw, cfg.RechargeFeeRate)
		}
	}

	cfg := (&PaymentConfigService{}).parsePaymentConfig(map[string]string{
		SettingRechargeFeeRate: "3.33",
	})
	if cfg.RechargeFeeRate != 3.33 {
		t.Fatalf("valid flat fee rate = %v, want 3.33", cfg.RechargeFeeRate)
	}
}

func TestUpdatePaymentConfigValidatesAndPersistsRechargeFeeTiers(t *testing.T) {
	t.Parallel()

	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := &PaymentConfigService{settingRepo: repo}
	tiers := []RechargeFeeTier{
		{MinAmount: 100, FeeRate: -1},
		{MinAmount: 10.29, FeeRate: 3},
	}
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{RechargeFeeTiers: &tiers}); err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	var persisted []RechargeFeeTier
	if err := json.Unmarshal([]byte(repo.updates[SettingRechargeFeeTiers]), &persisted); err != nil {
		t.Fatalf("persisted tier JSON is invalid: %v", err)
	}
	if len(persisted) != 2 || persisted[0].MinAmount != 10.29 || persisted[1].MinAmount != 100 {
		t.Fatalf("persisted tiers were not normalized: %#v", persisted)
	}

	bad := []RechargeFeeTier{{MinAmount: 10, FeeRate: -100}}
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{RechargeFeeTiers: &bad}); err == nil {
		t.Fatal("expected invalid negative rate to be rejected")
	}

	empty := []RechargeFeeTier{}
	if err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{RechargeFeeTiers: &empty}); err != nil {
		t.Fatalf("clearing tiers returned error: %v", err)
	}
	if repo.updates[SettingRechargeFeeTiers] != "[]" {
		t.Fatalf("clearing tiers persisted %q, want []", repo.updates[SettingRechargeFeeTiers])
	}
}
