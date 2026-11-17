package service

import "context"

var (
	_ FZYVideoBillingRepository     = (*UsageBillingLaneCombiner)(nil)
	_ fzyVideoCostHistoryRepository = (*UsageBillingLaneCombiner)(nil)
)

// Video holds and settlements already run in their own idempotent transactions.
// Delegate them directly, like batch-image holds, while ordinary usage retains
// the official per-user batching. Do not route settlement through Apply: that
// would charge the reserved amount twice and lose the task's settlement state.
func (c *UsageBillingLaneCombiner) fzyVideoRepository() (FZYVideoBillingRepository, error) {
	if c != nil {
		if repo, ok := c.repo.(FZYVideoBillingRepository); ok {
			return repo, nil
		}
	}
	return nil, ErrFZYVideoBillingUnavailable
}

func (c *UsageBillingLaneCombiner) ReserveFZYVideo(ctx context.Context, job *FZYVideoBillingJob) error {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return err
	}
	return repo.ReserveFZYVideo(ctx, job)
}

func (c *UsageBillingLaneCombiner) BindFZYVideo(ctx context.Context, id, taskID string) error {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return err
	}
	return repo.BindFZYVideo(ctx, id, taskID)
}

func (c *UsageBillingLaneCombiner) ReleaseFZYVideo(ctx context.Context, id string) (bool, error) {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return false, err
	}
	return repo.ReleaseFZYVideo(ctx, id)
}

func (c *UsageBillingLaneCombiner) GetFZYVideoByTask(ctx context.Context, taskID string, userID, apiKeyID, accountID int64) (*FZYVideoBillingJob, error) {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return nil, err
	}
	return repo.GetFZYVideoByTask(ctx, taskID, userID, apiKeyID, accountID)
}

func (c *UsageBillingLaneCombiner) SettleFZYVideo(ctx context.Context, id string, outputTokens int, amount float64, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return nil, err
	}
	return repo.SettleFZYVideo(ctx, id, outputTokens, amount, cmd)
}

func (c *UsageBillingLaneCombiner) ListPendingFZYVideos(ctx context.Context, limit int) ([]*FZYVideoBillingJob, error) {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return nil, err
	}
	return repo.ListPendingFZYVideos(ctx, limit)
}

func (c *UsageBillingLaneCombiner) TouchFZYVideo(ctx context.Context, id string) error {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return err
	}
	return repo.TouchFZYVideo(ctx, id)
}

func (c *UsageBillingLaneCombiner) ListStaleFZYReservations(ctx context.Context, limit int) ([]*FZYVideoBillingJob, error) {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return nil, err
	}
	return repo.ListStaleFZYReservations(ctx, limit)
}

func (c *UsageBillingLaneCombiner) LoadFZYVideoAPIKey(ctx context.Context, id int64) (*APIKey, error) {
	repo, err := c.fzyVideoRepository()
	if err != nil {
		return nil, err
	}
	return repo.LoadFZYVideoAPIKey(ctx, id)
}

func (c *UsageBillingLaneCombiner) ListFZYVideoCostSamples(ctx context.Context, userID int64, model string, limit int) ([]FZYVideoCostSample, error) {
	if c != nil {
		if repo, ok := c.repo.(fzyVideoCostHistoryRepository); ok {
			return repo.ListFZYVideoCostSamples(ctx, userID, model, limit)
		}
	}
	return nil, ErrFZYVideoBillingUnavailable
}
