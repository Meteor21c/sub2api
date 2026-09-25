package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

var ErrFZYVideoBillingUnavailable = errors.New("FZY video token billing is unavailable")
var ErrFZYVideoInsufficientBalance = errors.New("insufficient balance for video reservation")

type FZYVideoBillingJob struct {
	ID           string
	UserID       int64
	APIKeyID     int64
	AccountID    int64
	TaskID       string
	Price        FZYVideoPriceSnapshot
	HoldAmount   float64
	ActualAmount float64
	OutputTokens int
	Status       string
	CreatedAt    time.Time
}

func (j *FZYVideoBillingJob) PricingPreview() map[string]any {
	if j == nil {
		return nil
	}
	official, _ := decimal.NewFromString(j.Price.OfficialPerMillion)
	discount, _ := decimal.NewFromString(j.Price.DiscountRate)
	markup, _ := decimal.NewFromString(j.Price.MarkupRate)
	return map[string]any{
		"billing_mode": "token", "currency": j.Price.Currency,
		"model": j.Price.Model, "scene_code": j.Price.SceneCode, "scene_name": j.Price.SceneName,
		"official_per_million":   j.Price.OfficialPerMillion,
		"upstream_per_million":   official.Mul(discount).String(),
		"sale_per_million":       j.Price.SalePerMillion,
		"provider_discount_rate": j.Price.DiscountRate,
		"markup_rate":            j.Price.MarkupRate,
		"sale_rate_to_official":  discount.Mul(markup).String(),
		"precharge_amount":       j.HoldAmount,
		"balance_conversion":     "1 CNY = 1 USD balance unit",
		"final_charge":           "sale_per_million * provider_output_tokens / 1000000; reservation refunded or topped up",
	}
}

// FZYVideoBillingRepository is intentionally separate from the general usage
// interface so existing gateway/test implementations remain unchanged.
type FZYVideoBillingRepository interface {
	ReserveFZYVideo(ctx context.Context, job *FZYVideoBillingJob) error
	BindFZYVideo(ctx context.Context, id, taskID string) error
	ReleaseFZYVideo(ctx context.Context, id string) (bool, error)
	GetFZYVideoByTask(ctx context.Context, taskID string, userID, apiKeyID, accountID int64) (*FZYVideoBillingJob, error)
	SettleFZYVideo(ctx context.Context, id string, outputTokens int, amount float64, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error)
	ListPendingFZYVideos(ctx context.Context, limit int) ([]*FZYVideoBillingJob, error)
	TouchFZYVideo(ctx context.Context, id string) error
	ListStaleFZYReservations(ctx context.Context, limit int) ([]*FZYVideoBillingJob, error)
	LoadFZYVideoAPIKey(ctx context.Context, id int64) (*APIKey, error)
}

func IsFZYTokenVideoAccount(account *Account, model string) bool {
	if !IsFZYVideoBridgeAccount(account) || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "doubao-seedance-") {
		return false
	}
	return true
}

func IsFZYVideoBridgeAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformGrok {
		return false
	}
	base := strings.TrimRight(strings.ToLower(strings.TrimSpace(account.GetCredential("base_url"))), "/")
	return strings.HasSuffix(base, "/provider/fzyinghe/v1")
}

func (s *OpenAIGatewayService) FZYVideoBillingRepository() (FZYVideoBillingRepository, error) {
	if s == nil {
		return nil, ErrFZYVideoBillingUnavailable
	}
	repo, ok := s.usageBillingRepo.(FZYVideoBillingRepository)
	if !ok || repo == nil {
		return nil, ErrFZYVideoBillingUnavailable
	}
	return repo, nil
}

// PrepareFZYVideoBilling freezes the provider tariff and current per-second
// quote. The latter is only a refundable reservation, never the final charge.
func (s *OpenAIGatewayService) PrepareFZYVideoBilling(ctx context.Context, account *Account, apiKey *APIKey, user *User, model string, body []byte) (*FZYVideoBillingJob, error) {
	if !IsFZYTokenVideoAccount(account, model) || apiKey == nil || apiKey.Group == nil || user == nil || apiKey.Group.IsSubscriptionType() || (s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return nil, ErrFZYVideoBillingUnavailable
	}
	credentialAccount, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	price, err := FetchFZYVideoPriceSnapshot(ctx, nil, "", credentialAccount.GetCredential("api_key"), model, body)
	if err != nil {
		return nil, err
	}
	resolution := strings.TrimSpace(gjson.GetBytes(body, "resolution").String())
	if resolution == "" {
		resolution = "720p"
	}
	duration := int(gjson.GetBytes(body, "duration").Int())
	if duration <= 0 {
		duration = 5
	}
	if duration > 30 {
		return nil, errors.New("video duration exceeds the supported reservation range")
	}
	unitPrice := s.billingService.getVideoUnitPrice(model, resolution, videoPriceConfigFromAPIKey(apiKey))
	groupMultiplier := s.ResolveUserGroupRateMultiplier(ctx, user.ID, apiKey.Group.ID, apiKey.Group.RateMultiplier)
	multiplier := resolveVideoRateMultiplier(apiKey, groupMultiplier)
	if unitPrice <= 0 || multiplier < 0 {
		return nil, errors.New("video reservation price is unavailable")
	}
	hold := decimal.NewFromFloat(unitPrice).Mul(decimal.NewFromInt(int64(duration))).Mul(decimal.NewFromFloat(multiplier)).Round(UsageBillingMonetaryScale).InexactFloat64()
	if hold <= 0 {
		return nil, errors.New("video reservation amount is zero")
	}
	return &FZYVideoBillingJob{
		ID: uuid.NewString(), UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
		Price: price, HoldAmount: hold, Status: "reserved", CreatedAt: time.Now().UTC(),
	}, nil
}

func (s *OpenAIGatewayService) ReserveFZYVideoBilling(ctx context.Context, job *FZYVideoBillingJob) error {
	repo, err := s.FZYVideoBillingRepository()
	if err != nil {
		return err
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	if err := repo.ReserveFZYVideo(billingCtx, job); err != nil {
		return err
	}
	if s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(billingCtx, job.UserID)
	}
	return nil
}

func (s *OpenAIGatewayService) BindFZYVideoBilling(ctx context.Context, id, taskID string) error {
	repo, err := s.FZYVideoBillingRepository()
	if err != nil {
		return err
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if err = repo.BindFZYVideo(billingCtx, id, taskID); err == nil {
			return nil
		}
		if attempt < 2 {
			select {
			case <-billingCtx.Done():
				return err
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return err
}

func (s *OpenAIGatewayService) LoadFZYVideoBilling(ctx context.Context, taskID string, userID, apiKeyID, accountID int64) (*FZYVideoBillingJob, error) {
	repo, err := s.FZYVideoBillingRepository()
	if err != nil {
		return nil, err
	}
	return repo.GetFZYVideoByTask(ctx, taskID, userID, apiKeyID, accountID)
}

func (s *OpenAIGatewayService) ReleaseFZYVideoBilling(ctx context.Context, id string, userID int64) error {
	repo, err := s.FZYVideoBillingRepository()
	if err != nil {
		return err
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	released, err := repo.ReleaseFZYVideo(billingCtx, id)
	if err == nil && released && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(billingCtx, userID)
	}
	return err
}

func calculateFZYVideoTokenCost(job *FZYVideoBillingJob, outputTokens int) (*CostBreakdown, error) {
	if job == nil || outputTokens <= 0 {
		return nil, errors.New("FZY video completion has no output token usage")
	}
	actual, err := job.Price.CostForOutputTokens(outputTokens)
	if err != nil {
		return nil, err
	}
	officialUnit, err := decimal.NewFromString(job.Price.OfficialPerMillion)
	if err != nil || !officialUnit.IsPositive() {
		return nil, errors.New("FZY video official price is invalid")
	}
	official := officialUnit.Mul(decimal.NewFromInt(int64(outputTokens))).Div(decimal.NewFromInt(1000000)).Round(UsageBillingMonetaryScale).InexactFloat64()
	return &CostBreakdown{OutputCost: official, TotalCost: official, ActualCost: actual, BillingMode: string(BillingModeToken)}, nil
}

func (s *OpenAIGatewayService) applyFZYVideoUsageBilling(ctx context.Context, requestID string, usageLog *UsageLog, params *postUsageBillingParams, job *FZYVideoBillingJob, outputTokens int) error {
	repo, err := s.FZYVideoBillingRepository()
	if err != nil {
		return err
	}
	if params == nil || params.IsSubscriptionBill || job == nil || job.Status != "pending" {
		return ErrFZYVideoBillingUnavailable
	}
	cmd := buildUsageBillingCommand(requestID, usageLog, params)
	if cmd == nil || cmd.RequestID == "" {
		return ErrFZYVideoBillingUnavailable
	}
	providerCost, err := job.Price.ProviderCostForOutputTokens(outputTokens)
	if err != nil {
		return err
	}
	if cmd.AccountQuotaCost > 0 {
		cmd.AccountQuotaCost = providerCost * params.AccountRateMultiplier
	}
	cmd.RequestFingerprint = ""
	cmd.Normalize()
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	result, err := repo.SettleFZYVideo(billingCtx, job.ID, outputTokens, params.Cost.ActualCost, cmd)
	if err != nil {
		return err
	}
	if result == nil || !result.Applied {
		return nil
	}
	params.PrechargedBalance = job.HoldAmount
	finalizePostUsageBilling(billingCtx, params, s.billingDeps(), result)
	return nil
}
