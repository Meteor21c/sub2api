package service

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Sanitized key-specific marketplace snapshot: deliberately excludes API keys
// and account identifiers, and includes no 1.5 Pro entry for this key.
func fzyCurrentVideoPricing(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/fzy-video-marketplace.json")
	require.NoError(t, err)
	return body
}

func TestFZYVideoScenarioPricesUseRealBillingUnits(t *testing.T) {
	body := fzyCurrentVideoPricing(t)
	for _, tc := range []struct{ model, request, scene, mode, sale string }{
		{"doubao-seedance-2.0-mini", `{"resolution":"720p"}`, "video-doubao-seedance-2.0-mini-novideo", "token", "7.935"},
		{"doubao-seedance-2.5", `{"resolution":"720p"}`, "video-doubao-seedance-2.5-novideo-720p", "token", "63.595"},
		{"doubao-seedance-2.5", `{"resolution":"1080p"}`, "video-doubao-seedance-2.5-novideo-1080p", "token", "69.9545"},
		{"doubao-seedance-2.0", `{"resolution":"4K"}`, "video-doubao-seedance-2.0-novideo-4k", "token", "23.621"},
		{"kling-v3", `{"resolution":"720p","audio":true}`, "video-tencent-kling-3.0-audio-720p", "second", "0.82225"},
		{"kling-v3", `{}`, "video-tencent-kling-3.0-noaudio-1080p", "second", "0.598"},
		{"kling-v3-omni", `{"resolution":"720p"}`, "video-tencent-kling-3.0-omni-norefvideo-noaudio-720p", "second", "0.4485"},
		{"kling-v3-omni", `{"resolution":"720p","reference_images":["video:https://example.com/asset"]}`, "video-tencent-kling-3.0-omni-refvideo-noaudio-720p", "second", "0.67275"},
		{"kling-v3-omni", `{"resolution":"4K","audio":false,"reference_images":["https://example.com/ref.mp4"]}`, "video-tencent-kling-3.0-omni-refvideo-noaudio-4k", "second", "2.2425"},
	} {
		t.Run(tc.model+tc.scene, func(t *testing.T) {
			price, err := ParseFZYVideoPriceSnapshot(body, tc.model, []byte(tc.request))
			require.NoError(t, err)
			require.Equal(t, tc.scene, price.SceneCode)
			require.Equal(t, tc.mode, price.BillingMode)
			if tc.mode == "second" {
				require.Equal(t, tc.sale, price.SalePerSecond)
				require.Equal(t, "0", price.SalePerMillion)
			} else {
				require.Equal(t, tc.sale, price.SalePerMillion)
			}
		})
	}
}

func TestFZYVideoSecondCostDoesNotFabricateTokenUsage(t *testing.T) {
	price, err := ParseFZYVideoPriceSnapshot(fzyCurrentVideoPricing(t), "kling-v3-omni", []byte(`{"resolution":"720p"}`))
	require.NoError(t, err)
	job := &FZYVideoBillingJob{Price: price, DurationSeconds: 5, HoldAmount: 2.2425}
	cost, err := calculateFZYVideoCost(job, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 3.0, cost.TotalCost)
	require.Equal(t, 2.2425, cost.ActualCost)
	require.Equal(t, string(BillingModeVideo), cost.BillingMode)
	provider, err := price.ProviderCostForUsage(0, 5)
	require.NoError(t, err)
	require.Equal(t, 1.95, provider)
	preview := job.PricingPreview()
	require.Equal(t, "second", preview["billing_mode"])
	require.Equal(t, "0.6", preview["official_per_second"])
	require.Equal(t, "0.39", preview["upstream_per_second"])
	require.Equal(t, "0.4485", preview["sale_per_second"])
	require.Equal(t, "0.7475", preview["sale_rate_to_official"])
	require.NotContains(t, preview, "sale_per_million")
}

func TestFZYVideoCatalogUsesOnlyAvailablePricedCapabilities(t *testing.T) {
	body := fzyCurrentVideoPricing(t)
	catalog, err := ParseFZYVideoCatalogBundle(body, "doubao-seedance-1.5-pro")
	require.NoError(t, err, "a missing selected model must still return the available catalog")
	require.NotContains(t, catalog["available_models"], "doubao-seedance-1.5-pro")
	require.Len(t, catalog["models"], 6)
	require.NotContains(t, catalog, "model")
	models, ok := catalog["models"].([]map[string]any)
	require.True(t, ok)
	for _, model := range models {
		switch model["model"] {
		case "doubao-seedance-2.0-mini", "doubao-seedance-2.0-fast":
			require.Equal(t, []string{"480p", "720p"}, model["resolutions"])
		case "doubao-seedance-2.5":
			require.Equal(t, []string{"480p", "720p", "1080p"}, model["resolutions"])
		case "doubao-seedance-2.0":
			require.Equal(t, []string{"480p", "720p", "1080p", "4K"}, model["resolutions"])
		case "kling-v3-omni":
			require.Equal(t, []string{"720p", "1080p", "4K"}, model["resolutions"])
			rows, ok := model["scenarios"].([]map[string]any)
			require.True(t, ok)
			require.Len(t, rows, 9, "upstream's three unsupported reference-video+audio tariffs must not be offered")
			for _, row := range rows {
				hasVideo, ok := row["input_video"].(*bool)
				require.True(t, ok)
				require.NotNil(t, hasVideo)
				audio, ok := row["audio"].(*bool)
				require.True(t, ok)
				require.NotNil(t, audio)
				require.False(t, *hasVideo && *audio)
				require.Equal(t, "second", row["billing_mode"])
			}
		}
	}
	encoded, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "apiKey")
}

func TestFZYVideoScenarioRejectsUnsupportedInputsAndUnits(t *testing.T) {
	body := fzyCurrentVideoPricing(t)
	for _, tc := range []struct{ model, request string }{
		{"doubao-seedance-1.5-pro", `{}`},
		{"doubao-seedance-2.5", `{"resolution":"4K"}`},
		{"doubao-seedance-2.0-fast", `{"resolution":"1080p"}`},
		{"kling-v3-omni", `{"resolution":"720p","audio":true,"reference_images":["video:https://example.com/ref"]}`},
	} {
		_, err := ParseFZYVideoPriceSnapshot(body, tc.model, []byte(tc.request))
		require.Error(t, err)
	}
	price, err := ParseFZYVideoPriceSnapshot(body, "kling-v3", []byte(`{"resolution":"720p"}`))
	require.NoError(t, err)
	_, err = price.CostForUsage(100000, 0)
	require.Error(t, err, "a token count must not substitute for missing seconds")
	_, err = price.CostForUsage(0, 31)
	require.Error(t, err)
	token, err := ParseFZYVideoPriceSnapshot(body, "doubao-seedance-2.0-mini", nil)
	require.NoError(t, err)
	token.BillingMode = "" // legacy rows predating the billing_mode column
	cost, err := token.CostForOutputTokens(40594)
	require.NoError(t, err)
	require.Equal(t, 0.32211339, cost)
	_, err = token.CostForUsage(0, 5)
	require.Error(t, err, "legacy token reservations still require upstream output tokens")
}

func TestFZYVideoReservationDurationMatchesProviderRange(t *testing.T) {
	for _, tc := range []struct {
		model          string
		duration, want int
		fail           bool
	}{
		{"kling-v3", 3, 3, false}, {"kling-v3", 15, 15, false}, {"kling-v3", 16, 0, true}, {"kling-v3", -1, 0, true},
		{"doubao-seedance-2.0-mini", 3, 0, true}, {"doubao-seedance-2.0", 15, 15, false}, {"doubao-seedance-2.0", 16, 0, true},
		{"doubao-seedance-2.5", 30, 30, false}, {"doubao-seedance-2.5", 31, 0, true}, {"doubao-seedance-2.5", -1, 30, false},
		{"doubao-seedance-1.5-pro", 12, 12, false}, {"doubao-seedance-1.5-pro", 13, 0, true},
	} {
		got, err := fzyVideoReservationDuration(tc.model, []byte(fmt.Sprintf(`{"duration":%d}`, tc.duration)))
		if tc.fail {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		}
	}
}

func TestFZYVideoInputScenarioMatchesBridgeReferenceAliases(t *testing.T) {
	for _, tc := range []struct {
		request string
		want    bool
	}{
		{`{"content":[{"type":"video_url","video_url":{"url":"https://example.com/opaque"}}]}`, true},
		{`{"video_list":[{"video_url":"https://example.com/opaque"}]}`, true},
		{`{"reference_images":["video:https://example.com/opaque"]}`, true},
		{`{"reference_images":["https://example.com/asset?name=ref.mp4"]}`, true},
		{`{"images":[{"url":"https://example.com/ref.webm"}]}`, true},
		{`{"image":"https://example.com/ref.mov"}`, true},
		{`{"reference_images":["image:https://example.com/ref.mp4"]}`, false},
		{`{"reference_images":["https://example.com/ref.png"]}`, false},
		{`{"video_list":[{}]}`, false},
	} {
		require.Equal(t, tc.want, fzyRequestHasInputVideo([]byte(tc.request)), tc.request)
	}
}
