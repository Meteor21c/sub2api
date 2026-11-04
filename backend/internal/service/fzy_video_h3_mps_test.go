package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func newFZYModelsFixture(t *testing.T) []byte {
	t.Helper()
	entries := []map[string]any{}
	h3Rules := []map[string]any{}
	for _, r := range []struct{ resolution, price string }{{"768p", "0.5"}, {"2k", "0.8"}} {
		h3Rules = append(h3Rules, map[string]any{"sceneCode": "video-hailuo-h3-" + r.resolution, "sceneName": "MiniMax H3 " + r.resolution, "inputMode": "视频/图片输入", "resolution": r.resolution, "pricePerSecond": r.price, "pricingCurrency": "CNY", "surcharges": []map[string]any{{"code": "input_video_seconds", "metricKey": "h3_input_video_seconds", "unitPrice": r.price, "unitSize": 1, "multiplier": "1"}, {"code": "extra_images", "metricKey": "h3_input_extra_images", "unitPrice": "0.2", "unitSize": 1, "multiplier": "1"}}})
	}
	entries = append(entries, map[string]any{"innerCode": "MiniMax-H3", "discount": "0.85", "billingRule": map[string]any{"displayMode": "VIDEO_SCENARIOS", "pricingCurrency": "CNY", "scenarioRules": h3Rules}})
	entries = append(entries, map[string]any{"innerCode": "tencent-mps-superres", "discount": "0.63", "billingRule": map[string]any{"displayMode": "VIDEO_SCENARIOS", "pricingCurrency": "CNY", "tokenUnitSize": 1000000, "scenarioRules": []map[string]any{
		{"sceneCode": "video-tencent-mps-superres-720p-30fps", "sceneName": "720P 30FPS", "inputMode": "视频超分", "resolution": "720p", "outputPricePerMillion": "0.3325", "tokenUnitSize": 1000000},
		{"sceneCode": "video-tencent-mps-superres-720p-60fps", "sceneName": "720P 60FPS", "inputMode": "视频超分", "resolution": "720p", "outputPricePerMillion": "0.5325", "tokenUnitSize": 1000000},
	}}})
	body, err := json.Marshal(map[string]any{"code": 200, "data": entries})
	require.NoError(t, err)
	return body
}

func TestFZYH3ChargesActualInputSecondsAndExtraImages(t *testing.T) {
	price, err := ParseFZYVideoPriceSnapshot(newFZYModelsFixture(t), "MiniMax-H3", []byte(`{"resolution":"768P","reference_images":["video:https://example.com/ref.mp4"],"input_image_count":8}`))
	require.NoError(t, err)
	require.Equal(t, "0.48875", price.SalePerSecond)
	require.Len(t, price.Surcharges, 2)
	output, input, images := 5.0, 2.5, 8
	usage := &FZYVideoProviderUsage{OutputSeconds: &output, InputSeconds: &input, InputImageCount: &images}
	job := &FZYVideoBillingJob{Price: price, DurationSeconds: 5}
	cost, err := calculateFZYVideoCostForProviderUsage(job, 0, 5, usage)
	require.NoError(t, err)
	require.InDelta(t, 4.35, cost.TotalCost, 0.000001)
	require.InDelta(t, 4.252125, cost.ActualCost, 0.000001)
	provider, err := price.ProviderCostForProviderUsage(0, 5, usage)
	require.NoError(t, err)
	require.InDelta(t, 3.6975, provider, 0.000001)
	missingSnapshot := price
	missingSnapshot.Surcharges = nil
	_, err = missingSnapshot.ProviderCostForProviderUsage(0, 5, usage)
	require.Error(t, err, "missing frozen input tariffs must not silently omit material charges")
	_, err = calculateFZYVideoCostForProviderUsage(job, 0, 5, nil)
	require.Error(t, err)
	usage.InputSeconds = nil
	_, err = calculateFZYVideoCostForProviderUsage(job, 0, 5, usage)
	require.Error(t, err)
	usage.InputSeconds = &input
	usage.InputImageCount = nil
	_, err = calculateFZYVideoCostForProviderUsage(job, 0, 5, usage)
	require.Error(t, err)
}

func TestFZYMPSUsesFrameSpecificTokenTariffAndRealTokens(t *testing.T) {
	body := newFZYModelsFixture(t)
	p30, err := ParseFZYVideoPriceSnapshot(body, "tencent-mps-superres", []byte(`{"resolution":"720p","frameRate":30}`))
	require.NoError(t, err)
	p60, err := ParseFZYVideoPriceSnapshot(body, "tencent-mps-superres", []byte(`{"resolution":"720p","frameRate":60}`))
	require.NoError(t, err)
	require.Equal(t, "token", p30.BillingMode)
	require.Equal(t, "0.24089625", p30.SalePerMillion)
	require.Equal(t, "0.38579625", p60.SalePerMillion)
	_, err = p30.CostForUsage(0, 60)
	require.Error(t, err, "duration must never become fabricated output tokens")
	cost, err := p30.CostForUsage(1000000, 60)
	require.NoError(t, err)
	require.InDelta(t, 0.240896, cost, 0.000001)
	_, err = ParseFZYVideoPriceSnapshot(body, "tencent-mps-superres", []byte(`{"resolution":"720p","frameRate":25}`))
	require.Error(t, err)
	catalog, err := ParseFZYVideoCatalog(body, "tencent-mps-superres")
	require.NoError(t, err)
	rows, ok := catalog["scenarios"].([]map[string]any)
	if !ok {
		t.Fatal("missing pricing scenarios")
	}
	require.Len(t, rows, 2)
	require.Equal(t, 30, rows[0]["frame_rate"])
}

func TestFZYProviderUsageRejectsMissingAndInvalidPayload(t *testing.T) {
	require.Nil(t, ParseFZYVideoProviderUsage([]byte(`{"usage":{"output_seconds":5}}`)))
	usage := ParseFZYVideoProviderUsage([]byte(`{"provider_video_usage":{"output_seconds":5,"input_seconds":2.5,"input_image_count":8}}`))
	require.NotNil(t, usage)
	require.Equal(t, 5.0, *usage.OutputSeconds)
	require.Equal(t, 8, *usage.InputImageCount)
}
