package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

const fzyPricingFixture = `{"code":200,"data":[
  {"innerCode":"doubao-seedance-2.0-mini","discount":"0.3000","billingRule":{"displayMode":"VIDEO_SCENARIOS","pricingCurrency":"CNY","tokenUnitSize":1000000,"scenarioRules":[
    {"sceneCode":"mini-no-video","sceneName":"无输入视频","inputMode":"无输入视频","resolution":"480p/720p","outputPricePerMillion":"23"},
    {"sceneCode":"mini-video","sceneName":"含输入视频","inputMode":"含输入视频","resolution":"480p/720p","outputPricePerMillion":"14"}
  ]}},
  {"innerCode":"doubao-seedance-1.5-pro","discount":"0.7800","billingRule":{"displayMode":"VIDEO_SCENARIOS","pricingCurrency":"CNY","tokenUnitSize":1000000,"scenarioRules":[
    {"sceneCode":"online-audio","sceneName":"在线推理有声","outputPricePerMillion":"16"},
    {"sceneCode":"online-silent","sceneName":"在线推理无声","outputPricePerMillion":"8"},
    {"sceneCode":"offline-audio","sceneName":"离线推理有声","outputPricePerMillion":"8"}
  ]}}
]}`

func TestFZYVideoPriceSnapshotUsesKeyDiscountAndActualTokens(t *testing.T) {
	t.Parallel()
	quote, err := ParseFZYVideoPriceSnapshot([]byte(fzyPricingFixture), "doubao-seedance-2.0-mini", []byte(`{"resolution":"720p"}`))
	require.NoError(t, err)
	require.Equal(t, "mini-no-video", quote.SceneCode)
	require.Equal(t, "23", quote.OfficialPerMillion)
	require.Equal(t, "0.3", quote.DiscountRate)
	require.Equal(t, "7.935", quote.SalePerMillion)
	cost, err := quote.CostForOutputTokens(40594)
	require.NoError(t, err)
	require.Equal(t, 0.32211339, cost)
	providerCost, err := quote.ProviderCostForOutputTokens(40594)
	require.NoError(t, err)
	require.Equal(t, 0.2800986, providerCost)
	breakdown, err := calculateFZYVideoTokenCost(&FZYVideoBillingJob{Price: quote}, 40594)
	require.NoError(t, err)
	require.Equal(t, 0.933662, breakdown.TotalCost)
	require.Equal(t, 0.32211339, breakdown.ActualCost)

	withVideo, err := ParseFZYVideoPriceSnapshot([]byte(fzyPricingFixture), "doubao-seedance-2.0-mini", []byte(`{"resolution":"480p","reference_images":["https://example.com/ref.mp4"]}`))
	require.NoError(t, err)
	require.Equal(t, "mini-video", withVideo.SceneCode)
	require.Equal(t, "4.83", withVideo.SalePerMillion)
}

func TestFZYVideoPriceSnapshotSelectsOnlineAudio(t *testing.T) {
	t.Parallel()
	audio, err := ParseFZYVideoPriceSnapshot([]byte(fzyPricingFixture), "doubao-seedance-1.5-pro", []byte(`{"audio":true}`))
	require.NoError(t, err)
	require.Equal(t, "online-audio", audio.SceneCode)
	silent, err := ParseFZYVideoPriceSnapshot([]byte(fzyPricingFixture), "doubao-seedance-1.5-pro", []byte(`{"audio":false}`))
	require.NoError(t, err)
	require.Equal(t, "online-silent", silent.SceneCode)
}

func TestFZYVideoPriceSnapshotFailsClosedOnMissingPricing(t *testing.T) {
	t.Parallel()
	_, err := ParseFZYVideoPriceSnapshot([]byte(fzyPricingFixture), "doubao-seedance-2.0-mini", []byte(`{"resolution":"4K"}`))
	require.Error(t, err)
	_, err = ParseFZYVideoPriceSnapshot([]byte(`{"code":200,"data":[{"innerCode":"doubao-seedance-2.0-mini","discount":null}]}`), "doubao-seedance-2.0-mini", nil)
	require.Error(t, err)
}

func TestFetchFZYVideoPriceSnapshotDoesNotLeakKeyInErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "private-provider-key", r.URL.Query().Get("apiKey"))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, "error")
	}))
	defer server.Close()
	_, err := FetchFZYVideoPriceSnapshot(context.Background(), server.Client(), server.URL, "private-provider-key", "doubao-seedance-2.0-mini", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-provider-key")
}

func TestFZYVideoTokenBillingIsLimitedToTheProviderBridge(t *testing.T) {
	t.Parallel()
	bridge := &Account{Platform: PlatformGrok, Credentials: map[string]any{"base_url": "http://meteor-mcp-bridge:3102/provider/fzyinghe/v1"}}
	require.True(t, IsFZYTokenVideoAccount(bridge, "doubao-seedance-2.0-mini"))
	require.False(t, IsFZYTokenVideoAccount(bridge, "kling-v3"))
	require.False(t, IsFZYTokenVideoAccount(&Account{Platform: PlatformGrok, Credentials: map[string]any{"base_url": "https://api.x.ai/v1"}}, "doubao-seedance-2.0-mini"))
}
