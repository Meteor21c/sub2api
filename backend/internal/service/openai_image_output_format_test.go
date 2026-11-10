package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageGIFRequiresVerifiedUpstreamModel(t *testing.T) {
	a := newOpenAIImageSizeTestAccount(1, 0)
	require.False(t, a.SupportsOpenAIImageOutputFormat("gpt-image-2.6", "gif"))
	a.Extra[AccountExtraOpenAIImageOutputFormats] = map[string]any{"gpt-image-2.6": []any{"gif"}}
	require.True(t, a.SupportsOpenAIImageOutputFormat("gpt-image-2.6", "gif"))
	require.False(t, a.SupportsOpenAIImageOutputFormat("gpt-image-2", "gif"))
	require.False(t, a.SupportsOpenAIImageOutputFormat("gpt-image-2.6-sunburst", "gif"))
	a.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "gpt-image-2.6"}}
	require.True(t, a.SupportsOpenAIImageOutputFormat("alias", "gif"))
	a.Type = AccountTypeOAuth
	require.False(t, a.SupportsOpenAIImageOutputFormat("alias", "gif"))
	require.True(t, a.SupportsOpenAIImageOutputFormat("gpt-image-2", "png"))
}

func TestImageGIFRoutingCannotEscapeToUnsupportedAccounts(t *testing.T) {
	for _, mode := range openAIImageSizeSchedulerModes {
		t.Run(mode.name, func(t *testing.T) {
			accounts := []Account{newOpenAIImageSizeTestAccount(86101, 0), newOpenAIImageSizeTestAccount(86102, 9)}
			accounts[1].Extra[AccountExtraOpenAIImageOutputFormats] = map[string]any{"gpt-image-2.6": []any{"gif"}}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:gif-session": 86101}}
			svc := newOpenAIImageSizeSchedulerTestService(mode, accounts, cache, schedulerTestConcurrencyCache{})
			groupID := int64(93601)
			ctx := WithOpenAIImageOutputRouting(WithOpenAIImagesEndpoint(context.Background()), "gpt-image-2.6", "gif")
			selected, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, "gif-session", "gpt-image-2.6", nil, OpenAIImagesCapabilityNative)
			require.NoError(t, err)
			require.Equal(t, int64(86102), selected.Account.ID)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			// Failover must not reuse the otherwise higher-priority static account.
			selected, _, err = svc.SelectAccountWithSchedulerForImages(ctx, &groupID, "", "gpt-image-2.6", map[int64]struct{}{86102: {}}, OpenAIImagesCapabilityNative)
			require.Error(t, err)
			require.Nil(t, selected)
		})
	}
}

func TestImageGIFRequestBoundaries(t *testing.T) {
	base := OpenAIImagesRequest{OutputFormat: "gif", N: 1, Size: "auto", Endpoint: openAIImagesGenerationsEndpoint}
	require.NoError(t, ValidateOpenAIImageGIFRequest(&base))
	for _, mutate := range []func(*OpenAIImagesRequest){
		func(r *OpenAIImagesRequest) { r.N = 2 },
		func(r *OpenAIImagesRequest) { r.Size = "1024x1024" },
		func(r *OpenAIImagesRequest) { r.Stream = true },
		func(r *OpenAIImagesRequest) { r.InputImageURLs = []string{"https://example.invalid/ref.png"} },
		func(r *OpenAIImagesRequest) { r.Endpoint = openAIImagesEditsEndpoint },
		func(r *OpenAIImagesRequest) { n := 2; r.FrameCount = &n },
	} {
		req := base
		mutate(&req)
		require.Error(t, ValidateOpenAIImageGIFRequest(&req))
	}
}
