package service

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const FZYVideoMaxHold = 5.0
const fzyVideoHistoryLimit = 200

type FZYVideoEstimate struct {
	Source  string
	Samples int
	Amount  *string
	Low     *string
	High    *string
}

// Repository rows never contain prompts, media, task identifiers or keys.
type FZYVideoCostSample struct {
	Price            FZYVideoPriceSnapshot
	RequestedSeconds int
	ActualSeconds    float64
	OutputTokens     int64
	ActualAmount     string
	CreatedAt        time.Time
}

type FZYVideoCostRecord struct {
	CreatedAt         time.Time `json:"created_at"`
	Resolution        string    `json:"resolution"`
	DurationSeconds   float64   `json:"duration_seconds"`
	DurationSource    string    `json:"duration_source"`
	FrameRate         int       `json:"frame_rate,omitempty"`
	AspectRatio       string    `json:"aspect_ratio,omitempty"`
	InputImageCount   int       `json:"input_image_count,omitempty"`
	InputVideoSeconds float64   `json:"input_video_seconds,omitempty"`
	Audio             *bool     `json:"audio,omitempty"`
	SceneName         string    `json:"scene_name"`
	Currency          string    `json:"currency"`
	OutputTokens      int64     `json:"output_tokens"`
	ActualAmount      string    `json:"actual_amount"`
	AveragePerSecond  *string   `json:"average_per_second"`
}

type fzyVideoCostHistoryRepository interface {
	ListFZYVideoCostSamples(context.Context, int64, string, int) ([]FZYVideoCostSample, error)
}

func (s *OpenAIGatewayService) FZYVideoCostHistory(ctx context.Context, userID int64, model string) ([]FZYVideoCostRecord, error) {
	repo, ok := s.usageBillingRepo.(fzyVideoCostHistoryRepository)
	if !ok || userID <= 0 {
		return nil, ErrFZYVideoBillingUnavailable
	}
	samples, err := repo.ListFZYVideoCostSamples(ctx, userID, model, 20)
	if err != nil {
		return nil, err
	}
	result := make([]FZYVideoCostRecord, 0, len(samples))
	for _, sample := range samples {
		amount, err := decimal.NewFromString(sample.ActualAmount)
		if err != nil || !amount.IsPositive() {
			continue
		}
		seconds, source := sample.duration()
		var average *string
		if seconds > 0 {
			value := amount.Div(decimal.NewFromFloat(seconds)).Round(8).String()
			average = &value
		}
		scene := "视频生成"
		if sample.Price.Audio != nil {
			scene = "无视频参考"
		}
		if sample.Price.ExpectedInputVideo {
			scene = "含视频参考"
		}
		if model == "tencent-mps-superres" {
			scene = "视频增强"
		}
		result = append(result, FZYVideoCostRecord{CreatedAt: sample.CreatedAt, Resolution: sample.Price.Resolution, DurationSeconds: seconds, DurationSource: source, FrameRate: sample.Price.FrameRate, AspectRatio: sample.Price.AspectRatio, InputImageCount: sample.Price.InputImageCount, InputVideoSeconds: sample.Price.InputVideoSeconds, Audio: sample.Price.Audio, SceneName: scene, Currency: sample.Price.Currency, OutputTokens: sample.OutputTokens, ActualAmount: amount.String(), AveragePerSecond: average})
	}
	return result, nil
}

func (sample FZYVideoCostSample) duration() (float64, string) {
	if sample.ActualSeconds > 0 && sample.ActualSeconds <= 86400 && !math.IsNaN(sample.ActualSeconds) {
		return sample.ActualSeconds, "actual"
	}
	if !sample.Price.ModelSelectedDuration && sample.RequestedSeconds > 0 && sample.RequestedSeconds <= 86400 {
		return float64(sample.RequestedSeconds), "requested"
	}
	return 0, "unknown"
}

func defaultFZYVideoHold(model string) float64 {
	switch model {
	case "doubao-seedance-2.0", "doubao-seedance-2.5":
		return 5
	case "doubao-seedance-2.0-mini", "tencent-mps-superres":
		return 1
	default:
		return 2
	}
}

func (s *OpenAIGatewayService) applyFZYVideoEstimate(ctx context.Context, job *FZYVideoBillingJob) error {
	job.Estimate.Source = "default"
	if job.Price.IsPerSecond() {
		amount := decimal.NewFromFloat(job.HoldAmount).String()
		job.Estimate = FZYVideoEstimate{Source: "exact", Amount: &amount, Low: &amount, High: &amount}
	} else {
		repo, ok := s.usageBillingRepo.(fzyVideoCostHistoryRepository)
		if !ok {
			return ErrFZYVideoBillingUnavailable
		}
		samples, err := repo.ListFZYVideoCostSamples(ctx, job.UserID, job.Price.Model, fzyVideoHistoryLimit)
		if err != nil {
			return err
		}
		job.Estimate = estimateFZYVideoCost(job, samples)
		if job.Estimate.High != nil {
			high, _ := decimal.NewFromString(*job.Estimate.High)
			job.HoldAmount = math.Max(0.01, high.RoundCeil(2).InexactFloat64())
		}
	}
	job.HoldAmount = math.Min(FZYVideoMaxHold, job.HoldAmount)
	return nil
}

// Normalize observed token use per second, then reprice with today's tariff.
// Historical amounts are displayed unchanged but never reused as current prices.
func estimateFZYVideoCost(job *FZYVideoBillingJob, samples []FZYVideoCostSample) FZYVideoEstimate {
	estimate := FZYVideoEstimate{Source: "default"}
	price, err := decimal.NewFromString(job.Price.SalePerMillion)
	if err != nil || !price.IsPositive() || job.DurationSeconds <= 0 {
		return estimate
	}
	values := []decimal.Decimal{}
	for _, sample := range samples {
		p := sample.Price
		if p.Model != job.Price.Model || p.SceneCode != job.Price.SceneCode || p.Currency != job.Price.Currency || p.IsPerSecond() || !strings.EqualFold(p.Resolution, job.Price.Resolution) || p.FrameRate != job.Price.FrameRate {
			continue
		}
		if p.AspectRatio != "" && job.Price.AspectRatio != "" && p.AspectRatio != job.Price.AspectRatio {
			continue
		}
		if p.Audio != nil && job.Price.Audio != nil && *p.Audio != *job.Price.Audio {
			continue
		}
		seconds, _ := sample.duration()
		if seconds <= 0 || sample.OutputTokens <= 0 {
			continue
		}
		value := decimal.NewFromInt(sample.OutputTokens).Div(decimal.NewFromFloat(seconds)).Mul(decimal.NewFromInt(int64(job.DurationSeconds))).Mul(price).Div(decimal.NewFromInt(1000000))
		if value.IsPositive() {
			values = append(values, value)
		}
	}
	if len(values) < 3 {
		return estimate
	}
	sort.Slice(values, func(i, j int) bool { return values[i].LessThan(values[j]) })
	quantile := func(percent int) *string {
		// Linear interpolation retains the median for an even sample count.
		position := (len(values) - 1) * percent
		i, fraction := position/100, position%100
		value := values[i]
		if fraction > 0 {
			value = value.Add(values[i+1].Sub(value).Mul(decimal.NewFromInt(int64(fraction))).Div(decimal.NewFromInt(100)))
		}
		result := value.Round(8).String()
		return &result
	}
	return FZYVideoEstimate{Source: "personal", Samples: len(values), Amount: quantile(50), Low: quantile(25), High: quantile(75)}
}

func fzyVideoAspectRatio(model string, body []byte) string {
	value := strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String())
	if value == "" {
		if strings.HasPrefix(model, "kling-") {
			return "16:9"
		}
		return "9:16"
	}
	switch value {
	case "16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive":
		return value
	}
	return ""
}
