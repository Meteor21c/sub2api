//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFZYVideoBillingReserveSettleReleaseAndDeduplicate(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB).(*usageBillingRepository)
	user := mustCreateUser(t, client, &service.User{
		Email: fmt.Sprintf("fzy-video-%d@example.com", time.Now().UnixNano()), Balance: 1,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name: "fzy-video-" + uuid.NewString(), Platform: service.PlatformGrok,
		RateMultiplier: 1, SubscriptionType: service.SubscriptionTypeStandard,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "fzy-video-" + uuid.NewString(), Platform: service.PlatformGrok,
		Type: service.AccountTypeAPIKey,
	})
	key := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: "sk-fzy-video-" + uuid.NewString(),
		Quota: 1, RateLimit5h: 1,
	})
	price := service.FZYVideoPriceSnapshot{
		Model: "doubao-seedance-2.0-mini", SceneCode: "mini-no-video", SceneName: "无输入视频",
		Currency: "CNY", OfficialPerMillion: "23", DiscountRate: "0.3",
		MarkupRate: "1.15", SalePerMillion: "7.935",
	}
	job := &service.FZYVideoBillingJob{
		ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
		Price: price, HoldAmount: 0.2,
	}
	require.NoError(t, repo.ReserveFZYVideo(ctx, job))
	assertFZYVideoBalance(t, ctx, user.ID, 0.8, 0.2)
	require.NoError(t, repo.BindFZYVideo(ctx, job.ID, "fzy-task-"+job.ID))
	require.NoError(t, repo.BindFZYVideo(ctx, job.ID, "fzy-task-"+job.ID))
	loaded, err := repo.GetFZYVideoByTask(ctx, "fzy-task-"+job.ID, user.ID, key.ID, account.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.True(t, decimal.RequireFromString("7.935").Equal(decimal.RequireFromString(loaded.Price.SalePerMillion)))
	require.Equal(t, "pending", loaded.Status)

	const actual = 0.32211339
	cmd := &service.UsageBillingCommand{
		RequestID: service.StableGrokVideoBillingRequestID(loaded.TaskID),
		APIKeyID:  key.ID, UserID: user.ID, AccountID: account.ID,
		AccountType: service.AccountTypeAPIKey, Model: price.Model,
		OutputTokens: 40594, BalanceCost: actual,
		APIKeyQuotaCost: actual, APIKeyRateLimitCost: actual,
	}
	settled, err := repo.SettleFZYVideo(ctx, job.ID, 40594, actual, cmd)
	require.NoError(t, err)
	require.True(t, settled.Applied)
	assertFZYVideoBalance(t, ctx, user.ID, 0.67788661, 0)
	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id=$1", key.ID).Scan(&quotaUsed))
	require.InDelta(t, actual, quotaUsed, 1e-8)
	settled, err = repo.SettleFZYVideo(ctx, job.ID, 40594, actual, cmd)
	require.NoError(t, err)
	require.False(t, settled.Applied)
	assertFZYVideoBalance(t, ctx, user.ID, 0.67788661, 0)

	failed := &service.FZYVideoBillingJob{
		ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
		Price: price, HoldAmount: 0.15,
	}
	require.NoError(t, repo.ReserveFZYVideo(ctx, failed))
	assertFZYVideoBalance(t, ctx, user.ID, 0.52788661, 0.15)
	released, err := repo.ReleaseFZYVideo(ctx, failed.ID)
	require.NoError(t, err)
	require.True(t, released)
	released, err = repo.ReleaseFZYVideo(ctx, failed.ID)
	require.NoError(t, err)
	require.False(t, released)
	assertFZYVideoBalance(t, ctx, user.ID, 0.67788661, 0)

	tooLarge := &service.FZYVideoBillingJob{
		ID: uuid.NewString(), UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
		Price: price, HoldAmount: 2,
	}
	require.ErrorIs(t, repo.ReserveFZYVideo(ctx, tooLarge), service.ErrFZYVideoInsufficientBalance)
	assertFZYVideoBalance(t, ctx, user.ID, 0.67788661, 0)
}

func assertFZYVideoBalance(t *testing.T, ctx context.Context, userID int64, wantBalance, wantFrozen float64) {
	t.Helper()
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT balance,COALESCE(frozen_balance,0) FROM users WHERE id=$1", userID).Scan(&balance, &frozen))
	require.InDelta(t, wantBalance, balance, 1e-8)
	require.InDelta(t, wantFrozen, frozen, 1e-8)
}
