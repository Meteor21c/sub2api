package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageBillingLaneFZYUnavailableDoesNotFallBackToGeneralBilling(t *testing.T) {
	for _, lanes := range []*UsageBillingLaneCombiner{nil, NewUsageBillingLaneCombiner(nil)} {
		ctx := context.Background()
		err := lanes.ReserveFZYVideo(ctx, &FZYVideoBillingJob{ID: "unavailable", HoldAmount: 5})
		require.ErrorIs(t, err, ErrFZYVideoBillingUnavailable)
		result, err := lanes.SettleFZYVideo(ctx, "unavailable", 100, 10, &UsageBillingCommand{BalanceCost: 10})
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrFZYVideoBillingUnavailable)
		samples, err := lanes.ListFZYVideoCostSamples(ctx, 42, "doubao-seedance-2.0", 20)
		require.Nil(t, samples)
		require.ErrorIs(t, err, ErrFZYVideoBillingUnavailable)
	}
}
