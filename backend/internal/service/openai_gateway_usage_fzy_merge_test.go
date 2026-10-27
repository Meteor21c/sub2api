package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type openAIFZYMergeBillingRepoStub struct {
	UsageBillingRepository
	FZYVideoBillingRepository

	applyCalls     int
	settleCalls    int
	jobID          string
	outputTokens   int
	amount         float64
	command        *UsageBillingCommand
	settlementDone bool
}

func (s *openAIFZYMergeBillingRepoStub) Apply(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	s.applyCalls++
	return &UsageBillingApplyResult{Applied: true}, nil
}

func (s *openAIFZYMergeBillingRepoStub) SettleFZYVideo(_ context.Context, id string, tokens int, amount float64, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	s.settleCalls++
	s.jobID = id
	s.outputTokens = tokens
	s.amount = amount
	copyCommand := *cmd
	s.command = &copyCommand
	return &UsageBillingApplyResult{Applied: s.settlementDone}, nil
}

func TestOpenAIGatewayServiceRecordUsage_FZYFrozenPriceSurvivesV218Billing(t *testing.T) {
	const (
		outputTokens        = 40594
		officialCost        = 0.933662
		providerCost        = 0.2800986
		saleCost            = 0.32211339
		accountCost         = 0.47616762 // providerCost * account multiplier 1.7
		responseModel       = "gpt-5.4-nano"
		frozenVideoModel    = "doubao-seedance-2.0-mini"
		groupRateMultiplier = 4.0
	)
	frozenPrice := FZYVideoPriceSnapshot{
		Model:              frozenVideoModel,
		Currency:           "CNY",
		OfficialPerMillion: "23",
		DiscountRate:       "0.3",
		MarkupRate:         "1.15",
		SalePerMillion:     "7.935",
	}

	for _, tt := range []struct {
		name       string
		videoCount int
		platform   string
		model      string
		freeFast   bool
	}{
		{name: "completed_video", videoCount: 1, platform: PlatformGrok, model: frozenVideoModel},
		{name: "fzy_marker_without_video_count", videoCount: 0, platform: PlatformGrok, model: frozenVideoModel},
		// A frozen job is authoritative even if a caller supplies an OpenAI
		// alias and a group that normally rebills Fast requests at Standard.
		{name: "fzy_marker_with_free_fast", videoCount: 0, platform: PlatformOpenAI, model: "gpt-5.5", freeFast: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			keyQuota := &openAIRecordUsageAPIKeyQuotaStub{}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
			repo := &openAIFZYMergeBillingRepoStub{settlementDone: true}
			svc.usageBillingRepo = repo
			// Settlement still runs its normal post-billing finalization; no
			// Redis dependency is needed to verify the committed billing command.
			svc.billingCacheService = nil

			// Make the upstream declaration a genuinely cheaper, identified
			// token model, so removing the FZY guard makes the zero-count case fail.
			require.True(t, svc.billingService.HasIdentifiedTokenPricing(responseModel))
			responseCost, err := svc.billingService.CalculateCost(responseModel, UsageTokens{OutputTokens: outputTokens}, groupRateMultiplier)
			require.NoError(t, err)
			require.Positive(t, responseCost.TotalCost)
			require.Less(t, responseCost.TotalCost, officialCost)
			require.NotEqual(t, saleCost, responseCost.ActualCost)

			user := &User{ID: 11, Balance: 50}
			accountMultiplier := 1.7
			account := &Account{
				ID:             33,
				Platform:       tt.platform,
				Type:           AccountTypeAPIKey,
				RateMultiplier: &accountMultiplier,
				Extra:          map[string]any{"quota_limit": 100.0},
			}
			apiKey := &APIKey{
				ID:          22,
				User:        user,
				GroupID:     i64p(44),
				Quota:       100,
				RateLimit5h: 20,
				Group: &Group{
					ID:             44,
					Platform:       tt.platform,
					RateMultiplier: groupRateMultiplier,
					FreeOpenAIFast: tt.freeFast,
				},
			}
			job := &FZYVideoBillingJob{
				ID: "fzy-frozen-job", UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
				TaskID: "fzy-task", Price: frozenPrice, HoldAmount: 2, Status: "pending",
			}
			result := &OpenAIForwardResult{
				RequestID:             "fzy-task",
				Model:                 tt.model,
				UpstreamResponseModel: responseModel,
				Usage:                 OpenAIUsage{OutputTokens: outputTokens},
				VideoCount:            tt.videoCount,
				FZYVideoBill:          job,
				Duration:              time.Second,
			}
			if tt.freeFast {
				tier := "priority"
				result.ServiceTier = &tier
				require.True(t, groupBillsOpenAIFastAtStandard(apiKey, account, tier))
			}

			err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: result, APIKey: apiKey, User: user, Account: account, APIKeyService: keyQuota,
				ChannelUsageFields: ChannelUsageFields{
					OriginalModel: tt.model, ChannelMappedModel: tt.model, BillingModelSource: BillingModelSourceResponse,
				},
			})
			require.NoError(t, err)

			require.Equal(t, 1, repo.settleCalls, "FZY reservations must use their dedicated settlement transaction")
			require.Zero(t, repo.applyCalls, "general usage billing must not deduct the reservation again")
			require.Zero(t, userRepo.deductCalls, "the legacy balance deduction must not run")
			require.Zero(t, subRepo.incrementCalls)
			require.Zero(t, keyQuota.quotaCalls, "API-key quota belongs in the settlement command")
			require.Zero(t, keyQuota.rateLimitCalls)
			require.Equal(t, job.ID, repo.jobID)
			require.Equal(t, outputTokens, repo.outputTokens)
			require.InDelta(t, saleCost, repo.amount, 1e-12)
			require.Equal(t, frozenPrice, job.Price)
			require.NotNil(t, repo.command)
			require.InDelta(t, saleCost, repo.command.BalanceCost, 1e-12)
			require.InDelta(t, saleCost, repo.command.APIKeyQuotaCost, 1e-12)
			require.InDelta(t, saleCost, repo.command.APIKeyRateLimitCost, 1e-12)
			require.InDelta(t, accountCost, repo.command.AccountQuotaCost, 1e-12)
			require.InDelta(t, providerCost*accountMultiplier, repo.command.AccountQuotaCost, 1e-12)
			require.Zero(t, repo.command.SubscriptionCost)
			require.Equal(t, BillingTypeBalance, repo.command.BillingType)

			require.Equal(t, 1, usageRepo.calls)
			log := usageRepo.lastLog
			require.NotNil(t, log)
			require.InDelta(t, officialCost, log.OutputCost, 1e-12)
			require.InDelta(t, officialCost, log.TotalCost, 1e-12)
			require.InDelta(t, saleCost, log.ActualCost, 1e-12)
			require.InDelta(t, 0.345, log.RateMultiplier, 1e-12, "frozen provider discount * 1.15 markup")
			require.NotNil(t, log.AccountRateMultiplier)
			require.Equal(t, accountMultiplier, *log.AccountRateMultiplier)
			require.NotNil(t, log.BillingMode)
			require.Equal(t, string(BillingModeToken), *log.BillingMode)
			require.NotNil(t, log.UpstreamResponseModel)
			require.Equal(t, responseModel, *log.UpstreamResponseModel, "retain the response declaration for audit without repricing the frozen job")
		})
	}
}

func TestOpenAIRecordUsageFZYScenarioPreservesModeAndVideoUnits(t *testing.T) {
	for _, tc := range []struct {
		model, resolution                string
		duration, actualDuration, tokens int
		mode                             string
	}{
		{"kling-v3-omni", "4K", 15, 15, 0, string(BillingModeVideo)},
		{"doubao-seedance-2.0", "4K", 15, 0, 40594, string(BillingModeToken)},
		{"doubao-seedance-2.5", "1080p", 30, 30, 40594, string(BillingModeToken)},
	} {
		t.Run(tc.model, func(t *testing.T) {
			price, err := ParseFZYVideoPriceSnapshot(fzyCurrentVideoPricing(t), tc.model, []byte(`{"resolution":"`+tc.resolution+`"}`))
			require.NoError(t, err)
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			repo := &openAIFZYMergeBillingRepoStub{settlementDone: true}
			svc.usageBillingRepo = repo
			svc.billingCacheService = nil
			user := &User{ID: 11, Balance: 100}
			accountRate := 1.7
			account := &Account{ID: 33, Platform: PlatformGrok, Type: AccountTypeAPIKey, RateMultiplier: &accountRate, Extra: map[string]any{"quota_limit": 100.0}}
			key := &APIKey{ID: 22, User: user, GroupID: i64p(44), Quota: 100, RateLimit5h: 100, Group: &Group{ID: 44, Platform: PlatformGrok, RateMultiplier: 4}}
			job := &FZYVideoBillingJob{ID: "frozen-scenario", UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, TaskID: "scenario-task", Price: price, DurationSeconds: tc.duration, HoldAmount: 50, Status: "pending"}
			result := &OpenAIForwardResult{RequestID: "scenario-task", ResponseID: "scenario-task", Model: tc.model, BillingModel: tc.model, Usage: OpenAIUsage{OutputTokens: tc.tokens}, VideoCount: 1, VideoDurationSeconds: tc.actualDuration, VideoResolution: "480p", FZYVideoBill: job, Duration: time.Second}
			err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: user, Account: account, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}})
			require.NoError(t, err)
			sale, err := price.CostForUsage(tc.tokens, tc.duration)
			require.NoError(t, err)
			provider, err := price.ProviderCostForUsage(tc.tokens, tc.duration)
			require.NoError(t, err)
			require.Equal(t, 1, repo.settleCalls)
			require.Equal(t, tc.tokens, repo.outputTokens)
			require.Zero(t, repo.applyCalls)
			require.InDelta(t, sale, repo.amount, 1e-10, "group multiplier must not mark up the frozen sale price again")
			require.Equal(t, decimal.NewFromFloat(provider).Mul(decimal.NewFromFloat(accountRate)).Round(UsageBillingMonetaryScale).InexactFloat64(), repo.command.AccountQuotaCost)
			require.InDelta(t, sale, repo.command.BalanceCost, 1e-10)
			require.Equal(t, tc.mode, *usageRepo.lastLog.BillingMode)
			require.Equal(t, tc.resolution, *usageRepo.lastLog.VideoResolution)
			require.Equal(t, tc.duration, *usageRepo.lastLog.VideoDurationSeconds)
		})
	}
}
