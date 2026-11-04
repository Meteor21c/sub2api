package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageBillingRepository) ListFZYVideoCostSamples(ctx context.Context, userID int64, model string, limit int) ([]service.FZYVideoCostSample, error) {
	if r == nil || r.db == nil || userID <= 0 || model == "" {
		return nil, service.ErrFZYVideoBillingUnavailable
	}
	if limit < 1 || limit > 200 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `SELECT model,scene_code,currency,billing_mode,resolution,
		duration_seconds,COALESCE(actual_duration_seconds,0),COALESCE(output_tokens,0),actual_amount::text,created_at,price_snapshot
		FROM fzy_video_billing_jobs
		WHERE user_id=$1 AND model=$2 AND status='settled' AND actual_amount>0
		AND created_at >= NOW()-INTERVAL '90 days'
		ORDER BY created_at DESC,id DESC LIMIT $3`, userID, model, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []service.FZYVideoCostSample{}
	for rows.Next() {
		var sample service.FZYVideoCostSample
		var raw []byte
		if err := rows.Scan(&sample.Price.Model, &sample.Price.SceneCode, &sample.Price.Currency, &sample.Price.BillingMode, &sample.Price.Resolution, &sample.RequestedSeconds, &sample.ActualSeconds, &sample.OutputTokens, &sample.ActualAmount, &sample.CreatedAt, &raw); err != nil {
			return nil, err
		}
		var frozen service.FZYVideoPriceSnapshot
		if err := json.Unmarshal(raw, &frozen); err != nil {
			return nil, err
		}
		if frozen.Model != "" {
			if frozen.Model != sample.Price.Model || frozen.SceneCode != sample.Price.SceneCode {
				return nil, service.ErrFZYVideoBillingUnavailable
			}
			sample.Price = frozen
		}
		result = append(result, sample)
	}
	return result, rows.Err()
}
