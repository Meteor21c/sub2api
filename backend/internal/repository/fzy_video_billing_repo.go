package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageBillingRepository) ReserveFZYVideo(ctx context.Context, job *service.FZYVideoBillingJob) (err error) {
	if r == nil || r.db == nil || job == nil || job.ID == "" || job.HoldAmount <= 0 {
		return service.ErrFZYVideoBillingUnavailable
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO fzy_video_billing_jobs
		(id,user_id,api_key_id,account_id,model,scene_code,scene_name,currency,
		 official_per_million,discount_rate,markup_rate,sale_per_million,hold_amount,status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'reserved')`,
		job.ID, job.UserID, job.APIKeyID, job.AccountID, job.Price.Model,
		job.Price.SceneCode, job.Price.SceneName, job.Price.Currency,
		job.Price.OfficialPerMillion, job.Price.DiscountRate, job.Price.MarkupRate,
		job.Price.SalePerMillion, job.HoldAmount)
	if err != nil {
		return err
	}
	var remaining float64
	err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance-$1,
		frozen_balance=COALESCE(frozen_balance,0)+$1,updated_at=NOW()
		WHERE id=$2 AND deleted_at IS NULL AND balance >= $1 RETURNING balance`,
		job.HoldAmount, job.UserID).Scan(&remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrFZYVideoInsufficientBalance
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *usageBillingRepository) BindFZYVideo(ctx context.Context, id, taskID string) error {
	if r == nil || r.db == nil || id == "" || taskID == "" {
		return service.ErrFZYVideoBillingUnavailable
	}
	result, err := r.db.ExecContext(ctx, `UPDATE fzy_video_billing_jobs
		SET task_id=$2,status='pending',updated_at=NOW()
		WHERE id=$1 AND status='reserved' AND task_id IS NULL`, id, taskID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		var existingTask, status string
		if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(task_id,''),status FROM fzy_video_billing_jobs WHERE id=$1`, id).Scan(&existingTask, &status); err == nil && existingTask == taskID && status == "pending" {
			return nil
		}
		return errors.New("video reservation could not be bound to provider task")
	}
	return nil
}

func (r *usageBillingRepository) ReleaseFZYVideo(ctx context.Context, id string) (released bool, err error) {
	if r == nil || r.db == nil || id == "" {
		return false, service.ErrFZYVideoBillingUnavailable
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID int64
	var hold float64
	var status string
	err = tx.QueryRowContext(ctx, `SELECT user_id,hold_amount,status FROM fzy_video_billing_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&userID, &hold, &status)
	if err != nil {
		return false, err
	}
	if status == "released" || status == "settled" {
		return false, nil
	}
	if status != "reserved" && status != "pending" {
		return false, fmt.Errorf("unexpected video reservation status %q", status)
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$1,
		frozen_balance=COALESCE(frozen_balance,0)-$1,updated_at=NOW()
		WHERE id=$2 AND deleted_at IS NULL AND COALESCE(frozen_balance,0)>=$1`, hold, userID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, errors.New("video reserved balance could not be released")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fzy_video_billing_jobs SET status='released',updated_at=NOW(),settled_at=NOW() WHERE id=$1`, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) GetFZYVideoByTask(ctx context.Context, taskID string, userID, apiKeyID, accountID int64) (*service.FZYVideoBillingJob, error) {
	if r == nil || r.db == nil || taskID == "" {
		return nil, service.ErrFZYVideoBillingUnavailable
	}
	job := &service.FZYVideoBillingJob{}
	err := r.db.QueryRowContext(ctx, `SELECT id,user_id,api_key_id,account_id,task_id,model,
		scene_code,scene_name,currency,official_per_million,discount_rate,markup_rate,
		sale_per_million,hold_amount,COALESCE(actual_amount,0),COALESCE(output_tokens,0),status,created_at
		FROM fzy_video_billing_jobs WHERE task_id=$1 AND user_id=$2 AND api_key_id=$3 AND account_id=$4`,
		taskID, userID, apiKeyID, accountID).Scan(
		&job.ID, &job.UserID, &job.APIKeyID, &job.AccountID, &job.TaskID,
		&job.Price.Model, &job.Price.SceneCode, &job.Price.SceneName, &job.Price.Currency,
		&job.Price.OfficialPerMillion, &job.Price.DiscountRate, &job.Price.MarkupRate,
		&job.Price.SalePerMillion, &job.HoldAmount, &job.ActualAmount,
		&job.OutputTokens, &job.Status, &job.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (r *usageBillingRepository) ListPendingFZYVideos(ctx context.Context, limit int) ([]*service.FZYVideoBillingJob, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrFZYVideoBillingUnavailable
	}
	if limit < 1 || limit > 10 {
		limit = 5
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,api_key_id,account_id,task_id,model,
		scene_code,scene_name,currency,official_per_million,discount_rate,markup_rate,
		sale_per_million,hold_amount,COALESCE(actual_amount,0),COALESCE(output_tokens,0),status,created_at
		FROM fzy_video_billing_jobs WHERE status='pending' AND task_id IS NOT NULL
		AND COALESCE(last_checked_at,created_at) < NOW() - INTERVAL '30 seconds'
		ORDER BY last_checked_at NULLS FIRST,created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []*service.FZYVideoBillingJob
	for rows.Next() {
		job := &service.FZYVideoBillingJob{}
		if err := rows.Scan(&job.ID, &job.UserID, &job.APIKeyID, &job.AccountID, &job.TaskID,
			&job.Price.Model, &job.Price.SceneCode, &job.Price.SceneName, &job.Price.Currency,
			&job.Price.OfficialPerMillion, &job.Price.DiscountRate, &job.Price.MarkupRate,
			&job.Price.SalePerMillion, &job.HoldAmount, &job.ActualAmount,
			&job.OutputTokens, &job.Status, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *usageBillingRepository) TouchFZYVideo(ctx context.Context, id string) error {
	if r == nil || r.db == nil || id == "" {
		return service.ErrFZYVideoBillingUnavailable
	}
	_, err := r.db.ExecContext(ctx, `UPDATE fzy_video_billing_jobs SET last_checked_at=NOW()
		WHERE id=$1 AND status='pending'`, id)
	return err
}

func (r *usageBillingRepository) ListStaleFZYReservations(ctx context.Context, limit int) ([]*service.FZYVideoBillingJob, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrFZYVideoBillingUnavailable
	}
	if limit < 1 || limit > 10 {
		limit = 3
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,api_key_id,account_id,model,
		scene_code,scene_name,currency,official_per_million,discount_rate,markup_rate,
		sale_per_million,hold_amount,created_at FROM fzy_video_billing_jobs
		WHERE status='reserved' AND task_id IS NULL AND created_at < NOW() - INTERVAL '24 hours'
		ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []*service.FZYVideoBillingJob
	for rows.Next() {
		job := &service.FZYVideoBillingJob{Status: "reserved"}
		if err := rows.Scan(&job.ID, &job.UserID, &job.APIKeyID, &job.AccountID,
			&job.Price.Model, &job.Price.SceneCode, &job.Price.SceneName, &job.Price.Currency,
			&job.Price.OfficialPerMillion, &job.Price.DiscountRate, &job.Price.MarkupRate,
			&job.Price.SalePerMillion, &job.HoldAmount, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *usageBillingRepository) LoadFZYVideoAPIKey(ctx context.Context, id int64) (*service.APIKey, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrFZYVideoBillingUnavailable
	}
	key := &service.APIKey{}
	group := &service.Group{}
	err := r.db.QueryRowContext(ctx, `SELECT k.id,k.user_id,k.group_id,k.quota,
		k.rate_limit_5h,k.rate_limit_1d,k.rate_limit_7d,
		g.platform,g.subscription_type,g.rate_multiplier
		FROM api_keys k JOIN groups g ON g.id=k.group_id
		WHERE k.id=$1 AND k.deleted_at IS NULL AND g.deleted_at IS NULL`, id).Scan(
		&key.ID, &key.UserID, &group.ID, &key.Quota,
		&key.RateLimit5h, &key.RateLimit1d, &key.RateLimit7d,
		&group.Platform, &group.SubscriptionType, &group.RateMultiplier)
	if err != nil {
		return nil, err
	}
	key.GroupID = &group.ID
	key.Group = group
	return key, nil
}

func (r *usageBillingRepository) SettleFZYVideo(ctx context.Context, id string, outputTokens int, amount float64, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	if r == nil || r.db == nil || id == "" || outputTokens <= 0 || amount <= 0 || cmd == nil {
		return nil, service.ErrFZYVideoBillingUnavailable
	}
	cmd.Normalize()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID, apiKeyID, accountID int64
	var hold float64
	var status, taskID string
	err = tx.QueryRowContext(ctx, `SELECT user_id,api_key_id,account_id,hold_amount,status,COALESCE(task_id,'')
		FROM fzy_video_billing_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&userID, &apiKeyID, &accountID, &hold, &status, &taskID)
	if err != nil {
		return nil, err
	}
	if status == "settled" {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}
	if status != "pending" || taskID == "" || userID != cmd.UserID || apiKeyID != cmd.APIKeyID || accountID != cmd.AccountID {
		return nil, errors.New("video reservation does not match the completed task")
	}
	claimed, err := r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	if err != nil || !claimed {
		if err == nil {
			err = errors.New("video usage charge was claimed without settling its reservation")
		}
		return nil, err
	}
	var balance float64
	err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance+$1-$2,
		frozen_balance=COALESCE(frozen_balance,0)-$1,updated_at=NOW()
		WHERE id=$3 AND deleted_at IS NULL AND COALESCE(frozen_balance,0)>=$1
		RETURNING balance`, hold, amount, userID).Scan(&balance)
	if err != nil {
		return nil, err
	}
	effects := *cmd
	effects.BalanceCost = 0 // balance delta was applied atomically above
	result := &service.UsageBillingApplyResult{Applied: true, NewBalance: &balance, BalanceOverdrafted: balance < 0}
	if err := r.applyUsageBillingEffects(ctx, tx, &effects, result); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fzy_video_billing_jobs SET status='settled',actual_amount=$2,
		output_tokens=$3,updated_at=NOW(),settled_at=NOW() WHERE id=$1`, id, amount, outputTokens); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
