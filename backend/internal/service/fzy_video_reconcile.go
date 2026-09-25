package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// The normal status endpoint settles immediately. This small background pass
// handles tasks whose browser/client stopped polling after creation. It asks
// the bridge for status only: no video download and no server-side build/load.
func (s *OpenAIGatewayService) runFZYVideoReconciler() {
	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		if err := s.reconcileFZYVideoJobs(ctx); err != nil {
			logger.L().Warn("fzy_video.reconcile_pass_failed", zap.Error(err))
		}
		cancel()
	}
}

func (s *OpenAIGatewayService) reconcileFZYVideoJobs(ctx context.Context) error {
	repo, err := s.FZYVideoBillingRepository()
	if err != nil {
		return err
	}
	jobs, err := repo.ListPendingFZYVideos(ctx, 3)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := repo.TouchFZYVideo(ctx, job.ID); err != nil {
			logger.L().Warn("fzy_video.reconcile_touch_failed", zap.String("reservation_id", job.ID), zap.Error(err))
			continue
		}
		if err := s.reconcileFZYVideoJob(ctx, repo, job); err != nil {
			logger.L().Warn("fzy_video.reconcile_job_failed", zap.String("reservation_id", job.ID), zap.Error(err))
		}
	}
	orphans, err := repo.ListStaleFZYReservations(ctx, 3)
	if err != nil {
		return err
	}
	for _, orphan := range orphans {
		logger.L().Error("fzy_video.unbound_reservation_expired", zap.String("reservation_id", orphan.ID))
		if err := s.ReleaseFZYVideoBilling(ctx, orphan.ID, orphan.UserID); err != nil {
			logger.L().Error("fzy_video.unbound_reservation_release_failed", zap.String("reservation_id", orphan.ID), zap.Error(err))
		}
	}
	return nil
}

type fzyReconcileQuotaUpdater struct{}

func (fzyReconcileQuotaUpdater) UpdateQuotaUsed(context.Context, int64, float64) error { return nil }
func (fzyReconcileQuotaUpdater) UpdateRateLimitUsage(context.Context, int64, float64) error {
	return nil
}

func (s *OpenAIGatewayService) reconcileFZYVideoJob(ctx context.Context, repo FZYVideoBillingRepository, job *FZYVideoBillingJob) error {
	if job == nil || job.TaskID == "" || job.Status != "pending" || s.accountRepo == nil || s.userRepo == nil {
		return ErrFZYVideoBillingUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, job.AccountID)
	if err != nil || !IsFZYVideoBridgeAccount(account) {
		return errors.New("FZY video provider account is unavailable")
	}
	credential, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(credential.GetCredential("api_key"))
	if token == "" {
		return errors.New("FZY video provider credential is unavailable")
	}
	target, err := buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoStatus, job.TaskID)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Meteor-Billing-Only", "1")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.New("FZY video status lookup returned a non-success response")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || !gjson.ValidBytes(body) {
		return errors.New("FZY video status response is invalid")
	}
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	if status == "failed" || status == "expired" || status == "cancelled" || status == "canceled" {
		return s.ReleaseFZYVideoBilling(ctx, job.ID, job.UserID)
	}
	if !IsGrokVideoStatusBillable(body) {
		return nil
	}
	usage := providerTokenUsageFromGrokStatus(body)
	if usage.OutputTokens <= 0 {
		return errors.New("completed FZY video did not include output tokens")
	}
	user, err := s.userRepo.GetByID(ctx, job.UserID)
	if err != nil || user == nil {
		return errors.New("FZY video billing user is unavailable")
	}
	key, err := repo.LoadFZYVideoAPIKey(ctx, job.APIKeyID)
	if err != nil || key == nil || key.UserID != job.UserID {
		return errors.New("FZY video billing API key is unavailable")
	}
	key.User = user
	result := &OpenAIForwardResult{
		RequestID: StableGrokVideoBillingRequestID(job.TaskID), ResponseID: job.TaskID,
		Model: job.Price.Model, BillingModel: job.Price.Model, UpstreamModel: job.Price.Model,
		Usage: usage, VideoCount: 1, FZYVideoBill: job,
		VideoDurationSeconds: int(gjson.GetBytes(body, "video.duration").Int()),
		Duration:             time.Since(job.CreatedAt),
	}
	return s.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: result, APIKey: key, User: user, Account: account,
		QuotaPlatform: PlatformGrok, APIKeyService: fzyReconcileQuotaUpdater{},
		RequestPayloadHash: HashUsageRequestPayload([]byte(job.TaskID)),
	})
}
