//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestForwardGrokMediaFZYRetainsLongProviderDuration(t *testing.T) {
	const body = `{"id":"long-task","status":"done","model":"doubao-seedance-2.5","video":{"url":"https://example.com/result.mp4","duration":30}}`
	for _, fzy := range []bool{true, false} {
		account := grokMediaContentTestAccount()
		if fzy {
			account = fzyAssetTestAccount()
		}
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
		c, _ := grokMediaContentTestContext(http.MethodGet, "https://api.example/v1/videos/long-task", nil)
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideoStatus, "long-task", nil, "")
		require.NoError(t, err)
		if fzy {
			require.Equal(t, 30, result.VideoDurationSeconds)
		} else {
			require.Equal(t, 15, result.VideoDurationSeconds, "native Grok duration normalization is unchanged")
		}
	}
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(body),
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"video/mp4"}}, Body: io.NopCloser(strings.NewReader("video-content"))},
	}}
	c, _ := grokMediaContentTestContext(http.MethodGet, "https://api.example/v1/videos/long-task/content", nil)
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	result, err := svc.ForwardGrokMedia(context.Background(), c, fzyAssetTestAccount(), GrokMediaEndpointVideoContent, "long-task", nil, "")
	require.NoError(t, err)
	require.Equal(t, 30, result.VideoDurationSeconds)
}
