package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fzyCostHistoryStub struct {
	UsageBillingRepository
	owner   int64
	samples []FZYVideoCostSample
}

func (r *fzyCostHistoryStub) ListFZYVideoCostSamples(_ context.Context, owner int64, _ string, limit int) ([]FZYVideoCostSample, error) {
	r.owner = owner
	if len(r.samples) > limit {
		return r.samples[:limit], nil
	}
	return r.samples, nil
}

func TestFZYHistoryRepricesUsageAndCapsOnlyHold(t *testing.T) {
	p := FZYVideoPriceSnapshot{Model: "doubao-seedance-2.0", SceneCode: "no-video", Resolution: "720p", Currency: "CNY", SalePerMillion: "20", AspectRatio: "16:9"}
	job := &FZYVideoBillingJob{UserID: 42, Price: p, DurationSeconds: 10, HoldAmount: defaultFZYVideoHold(p.Model)}
	repo := &fzyCostHistoryStub{}
	for _, tokens := range []int64{400000, 500000, 600000} {
		repo.samples = append(repo.samples, FZYVideoCostSample{Price: p, ActualSeconds: 10, RequestedSeconds: 15, OutputTokens: tokens, ActualAmount: "999"})
	}
	s := &OpenAIGatewayService{usageBillingRepo: repo}
	require.NoError(t, s.applyFZYVideoEstimate(context.Background(), job))
	require.Equal(t, int64(42), repo.owner)
	require.Equal(t, "personal", job.Estimate.Source)
	require.Equal(t, 3, job.Estimate.Samples)
	require.Equal(t, "10", *job.Estimate.Amount, "use current tokens tariff, not historical paid amount")
	require.Equal(t, "9", *job.Estimate.Low)
	require.Equal(t, "11", *job.Estimate.High)
	require.Equal(t, 5.0, job.HoldAmount)
	require.Equal(t, job.Estimate.Amount, job.PricingPreview()["estimated_amount"])
}

func TestFZYHistoryNeedsMatchingSamplesAndKnownDuration(t *testing.T) {
	p := FZYVideoPriceSnapshot{Model: "doubao-seedance-2.0-mini", SceneCode: "no-video", Resolution: "720p", Currency: "CNY", SalePerMillion: "10"}
	job := &FZYVideoBillingJob{Price: p, DurationSeconds: 5}
	base := FZYVideoCostSample{Price: p, RequestedSeconds: 5, OutputTokens: 100000}
	cases := []struct {
		name   string
		change func(*FZYVideoCostSample)
	}{
		{"model", func(s *FZYVideoCostSample) { s.Price.Model = "other" }},
		{"scene", func(s *FZYVideoCostSample) { s.Price.SceneCode = "with-video" }},
		{"resolution", func(s *FZYVideoCostSample) { s.Price.Resolution = "4K" }},
		{"frame-rate", func(s *FZYVideoCostSample) { s.Price.FrameRate = 60 }},
		{"missing-duration", func(s *FZYVideoCostSample) { s.RequestedSeconds = 0 }},
		{"auto-duration", func(s *FZYVideoCostSample) { s.Price.ModelSelectedDuration = true }},
		{"missing-tokens", func(s *FZYVideoCostSample) { s.OutputTokens = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := base
			tc.change(&bad)
			est := estimateFZYVideoCost(job, []FZYVideoCostSample{base, base, bad})
			require.Equal(t, "default", est.Source)
			require.Nil(t, est.Amount)
		})
	}
	base.Price.ModelSelectedDuration = true
	base.ActualSeconds = 5
	require.Equal(t, "personal", estimateFZYVideoCost(job, []FZYVideoCostSample{base, base, base}).Source)
}

func TestFZYDefaultsAndSecondsReferenceRemainDistinctFromHold(t *testing.T) {
	repo := &fzyCostHistoryStub{}
	s := &OpenAIGatewayService{usageBillingRepo: repo}
	for model, expected := range map[string]float64{"doubao-seedance-2.0": 5, "doubao-seedance-2.5": 5, "doubao-seedance-2.0-mini": 1, "doubao-seedance-2.0-fast": 2, "tencent-mps-superres": 1} {
		job := &FZYVideoBillingJob{Price: FZYVideoPriceSnapshot{Model: model}, HoldAmount: defaultFZYVideoHold(model)}
		require.NoError(t, s.applyFZYVideoEstimate(context.Background(), job))
		require.Equal(t, expected, job.HoldAmount)
		require.Equal(t, "default", job.Estimate.Source)
		require.Nil(t, job.Estimate.Amount)
	}
	job := &FZYVideoBillingJob{Price: FZYVideoPriceSnapshot{BillingMode: "second"}, HoldAmount: 15}
	require.NoError(t, s.applyFZYVideoEstimate(context.Background(), job))
	require.Equal(t, "15", *job.Estimate.Amount)
	require.Equal(t, 5.0, job.HoldAmount)
}

func TestFZYCostHistoryExposesOnlyAccountingFields(t *testing.T) {
	repo := &fzyCostHistoryStub{samples: []FZYVideoCostSample{{Price: FZYVideoPriceSnapshot{Model: "doubao-seedance-2.0", SceneName: "private-provider", Currency: "CNY", Resolution: "720p", SalePerMillion: "secret-rate", MarkupRate: "1.15"}, RequestedSeconds: 10, ActualAmount: "12", CreatedAt: time.Now()}}}
	s := &OpenAIGatewayService{usageBillingRepo: repo}
	rows, err := s.FZYVideoCostHistory(context.Background(), 42, "doubao-seedance-2.0")
	require.NoError(t, err)
	require.Equal(t, int64(42), repo.owner)
	require.Equal(t, "requested", rows[0].DurationSource)
	require.Equal(t, "1.2", *rows[0].AveragePerSecond)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	for _, forbidden := range []string{"private-provider", "secret-rate", "1.15", "prompt", "url", "api_key", "user_id", "task_id", "markup"} {
		require.False(t, strings.Contains(string(raw), forbidden), forbidden)
	}
}
