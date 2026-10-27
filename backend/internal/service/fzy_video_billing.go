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

var ErrFZYVideoBillingUnavailable = errors.New("FZY video scenario billing is unavailable")
var ErrFZYVideoInsufficientBalance = errors.New("insufficient balance for video reservation")

type FZYVideoBillingJob struct {
	ID              string
	UserID          int64
	APIKeyID        int64
	AccountID       int64
	TaskID          string
	Price           FZYVideoPriceSnapshot
	HoldAmount      float64
	ActualAmount    float64
	OutputTokens    int
	Status          string
	DurationSeconds int
	CreatedAt       time.Time
}

func (j *FZYVideoBillingJob) PricingPreview() map[string]any {
	if j == nil {
		return nil
	}
	discount, _ := decimal.NewFromString(j.Price.DiscountRate)
	markup, _ := decimal.NewFromString(j.Price.MarkupRate)
	mode := "token"
	if j.Price.IsPerSecond() {
		mode = "second"
	}
	row := map[string]any{
		"source": fzyPricingSource, "fetched_at": time.Now().UTC().Format(time.RFC3339),
		"billing_mode": mode, "currency": j.Price.Currency, "model": j.Price.Model,
		"scene_code": j.Price.SceneCode, "scene_name": j.Price.SceneName,
		"provider_discount_rate": j.Price.DiscountRate, "markup_rate": j.Price.MarkupRate,
		"sale_rate_to_official": discount.Mul(markup).String(), "precharge_amount": j.HoldAmount,
		"balance_conversion": "1 CNY = 1 USD balance unit", "duration": j.DurationSeconds,
	}
	if j.Price.IsPerSecond() {
		official, _ := decimal.NewFromString(j.Price.OfficialPerSecond)
		row["official_per_second"], row["upstream_per_second"], row["sale_per_second"] = j.Price.OfficialPerSecond, official.Mul(discount).String(), j.Price.SalePerSecond
		row["final_charge"] = "sale_per_second * provider_duration_seconds; reservation refunded or topped up"
	} else {
		official, _ := decimal.NewFromString(j.Price.OfficialPerMillion)
		row["official_per_million"], row["upstream_per_million"], row["sale_per_million"] = j.Price.OfficialPerMillion, official.Mul(discount).String(), j.Price.SalePerMillion
		row["final_charge"] = "sale_per_million * provider_output_tokens / 1000000; reservation refunded or topped up"
	}
	return row
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
	return IsFZYVideoBridgeAccount(account) && strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "doubao-seedance-")
}
func IsFZYPricedVideoAccount(account *Account, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return IsFZYTokenVideoAccount(account, model) || IsFZYVideoBridgeAccount(account) && (model == "kling-v3" || model == "kling-v3-omni")
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
	if !IsFZYPricedVideoAccount(account, model) || apiKey == nil || apiKey.Group == nil || user == nil || apiKey.Group.IsSubscriptionType() || (s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return nil, ErrFZYVideoBillingUnavailable
	}
	duration, err := fzyVideoReservationDuration(model, body)
	if err != nil {
		return nil, err
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
	var hold float64
	if price.IsPerSecond() {
		hold, err = price.CostForUsage(0, duration)
		if err != nil {
			return nil, err
		}
	} else {
		// Token output is unknown until completion. Retain the existing refundable
		// estimate; final price always uses the provider snapshot without group rates.
		unitPrice := s.billingService.getVideoUnitPrice(model, resolution, videoPriceConfigFromAPIKey(apiKey))
		groupMultiplier := s.ResolveUserGroupRateMultiplier(ctx, user.ID, apiKey.Group.ID, apiKey.Group.RateMultiplier)
		multiplier := resolveVideoRateMultiplier(apiKey, groupMultiplier)
		if unitPrice <= 0 || multiplier < 0 {
			return nil, errors.New("video reservation price is unavailable")
		}
		hold = decimal.NewFromFloat(unitPrice).Mul(decimal.NewFromInt(int64(duration))).Mul(decimal.NewFromFloat(multiplier)).Round(UsageBillingMonetaryScale).InexactFloat64()
	}
	if hold <= 0 {
		return nil, errors.New("video reservation amount is zero")
	}
	return &FZYVideoBillingJob{
		ID: uuid.NewString(), UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
		Price: price, HoldAmount: hold, DurationSeconds: duration, Status: "reserved", CreatedAt: time.Now().UTC(),
	}, nil
}

// A model-selected duration is already accepted by the bridge for Seedance.
// Reserve its documented maximum so the precharge covers that existing mode.
func fzyVideoReservationDuration(model string, body []byte) (int, error) {
	minDuration, maxDuration := 4, 15
	switch model {
	case "kling-v3", "kling-v3-omni":
		minDuration = 3
	case "doubao-seedance-2.5":
		maxDuration = 30
	case "doubao-seedance-1.5-pro":
		maxDuration = 12
	}
	duration := int(gjson.GetBytes(body, "duration").Int())
	if duration == 0 {
		duration = 5 // matches the bridge default
	}
	if duration == -1 && strings.HasPrefix(model, "doubao-seedance-") {
		return maxDuration, nil
	}
	if duration < minDuration || duration > maxDuration {
		return 0, errors.New("video duration is outside the provider-supported range")
	}
	return duration, nil
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

// Keep the token helper for existing callers; all new settlement uses units
// appropriate to the frozen billing mode rather than fabricating output tokens.
func calculateFZYVideoTokenCost(job *FZYVideoBillingJob, outputTokens int) (*CostBreakdown, error) {
	return calculateFZYVideoCost(job, outputTokens, 0)
}
func fzyVideoDuration(job *FZYVideoBillingJob, duration int) int {
	if duration > 0 {
		return duration
	}
	if job != nil {
		return job.DurationSeconds
	}
	return 0
}
func calculateFZYVideoCost(job *FZYVideoBillingJob, outputTokens, duration int) (*CostBreakdown, error) {
	if job == nil {
		return nil, ErrFZYVideoBillingUnavailable
	}
	duration = fzyVideoDuration(job, duration)
	official, sale, err := job.Price.priceUnits(outputTokens, duration)
	if err != nil {
		return nil, err
	}
	mode := string(BillingModeToken)
	if job.Price.IsPerSecond() {
		mode = string(BillingModeVideo)
	}
	total := official.Round(UsageBillingMonetaryScale).InexactFloat64()
	actual := sale.Round(UsageBillingMonetaryScale).InexactFloat64()
	return &CostBreakdown{OutputCost: total, TotalCost: total, ActualCost: actual, BillingMode: mode}, nil
}

func (s *OpenAIGatewayService) applyFZYVideoUsageBilling(ctx context.Context, requestID string, usageLog *UsageLog, params *postUsageBillingParams, job *FZYVideoBillingJob, outputTokens, durationSeconds int) error {
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
	providerCost, err := job.Price.ProviderCostForUsage(outputTokens, fzyVideoDuration(job, durationSeconds))
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
