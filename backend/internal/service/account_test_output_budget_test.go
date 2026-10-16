//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestOutputBudgetIsolation(t *testing.T) {
	c, _ := newTestContext()
	other, _ := newTestContext()
	c.Set(accountTestOutputBudgetContextKey, 2048)
	restore := scopeAccountTestOutputBudget(c, accountTestDebugOutputBudget)

	payload, err := createTestPayload("claude-test", "hello")
	require.NoError(t, err)
	require.Equal(t, 1024, payload["max_tokens"])
	applyAccountTestOutputBudget(c, payload)
	require.Equal(t, 8192, payload["max_tokens"])

	otherPayload, err := createTestPayload("claude-test", "hello")
	require.NoError(t, err)
	applyAccountTestOutputBudget(other, otherPayload)
	require.Equal(t, 1024, otherPayload["max_tokens"])

	restoreNested := scopeAccountTestOutputBudget(c, 4096)
	applyAccountTestOutputBudget(c, payload)
	require.Equal(t, 4096, payload["max_tokens"])
	restoreNested()
	applyAccountTestOutputBudget(c, payload)
	require.Equal(t, 8192, payload["max_tokens"])
	restore()
	applyAccountTestOutputBudget(c, payload)
	require.Equal(t, 2048, payload["max_tokens"])
}

func accountBudgetTestStream(stopReason string) string {
	return fmt.Sprintf("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial SVG output\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q}}\n\ndata: {\"type\":\"message_stop\"}\n\n", stopReason)
}

func accountBudgetTestResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestAccountDebugOutputBudgetAndRestore(t *testing.T) {
	for _, provider := range []string{"claude", "bedrock", "cn-anthropic"} {
		for _, stopReason := range []string{"end_turn", "max_tokens"} {
			t.Run(provider+"/"+stopReason, func(t *testing.T) {
				account := &Account{
					ID:          907,
					Name:        "budget-isolation-test",
					Platform:    PlatformAnthropic,
					Type:        AccountTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://upstream.example"},
				}
				defaultBudget := int64(1024)
				body := accountBudgetTestStream(stopReason)
				nextBody := accountBudgetTestStream("end_turn")
				if provider == "bedrock" {
					account.Type = AccountTypeBedrock
					account.Credentials["auth_mode"] = "apikey"
					account.Credentials["region"] = "us-east-1"
					defaultBudget = 256
					body = fmt.Sprintf(`{"content":[{"type":"text","text":"partial SVG output"}],"stop_reason":%q}`, stopReason)
					nextBody = `{"content":[{"type":"text","text":"complete output"}],"stop_reason":"end_turn"}`
				} else if provider == "cn-anthropic" {
					account.Platform = PlatformKimi
					account.Credentials["api_protocol"] = APIProtocolAnthropic
					account.Credentials["api_base_urls"] = map[string]any{APIProtocolAnthropic: "http://upstream.example"}
				}
				repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
					accountsByID: map[int64]*Account{account.ID: account},
				}}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					accountBudgetTestResponse(body),
				}}
				greetings := []string{" HeLLo ", "hI", ""}
				for range len(greetings) + 1 {
					upstream.responses = append(upstream.responses, accountBudgetTestResponse(nextBody))
				}
				svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: rawChatCompletionsTestConfig()}
				c, _ := newTestContext()

				result, err := svc.TestAccountDebug(c, account.ID, "claude-sonnet-4-5", "draw a complete SVG")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, int64(8192), gjson.GetBytes(upstream.bodies[0], "max_tokens").Int())
				require.Equal(t, "partial SVG output", result.Content)
				require.Equal(t, stopReason != "max_tokens", result.Success)
				if stopReason == "max_tokens" {
					require.Contains(t, result.Error, "stop_reason=max_tokens")
					require.NotContains(t, result.RawResponse, `"type":"test_complete"`)
				}
				for index, greeting := range greetings {
					greetingResult, greetingErr := svc.TestAccountDebug(c, account.ID, "claude-sonnet-4-5", greeting)
					require.NoError(t, greetingErr)
					require.True(t, greetingResult.Success)
					require.Len(t, upstream.bodies, index+2)
					require.Equal(t, defaultBudget, gjson.GetBytes(upstream.bodies[index+1], "max_tokens").Int())
				}

				// Reuse this request context to catch a leaked debug-only budget.
				err = svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-5", "hello", AccountTestModeDefault)
				require.NoError(t, err)
				require.Len(t, upstream.bodies, len(greetings)+2)
				require.Equal(t, defaultBudget, gjson.GetBytes(upstream.bodies[len(greetings)+1], "max_tokens").Int())
			})
		}
	}
}

func TestAccountTestAnthropicStreamTruncation(t *testing.T) {
	svc := &AccountTestService{}
	for _, parser := range []struct {
		name string
		run  func(*gin.Context, io.Reader) error
	}{
		{name: "claude", run: svc.processClaudeStream},
		{name: "adaptive-anthropic", run: svc.processCNProviderAdaptiveAnthropicStream},
	} {
		for _, stopReason := range []string{"end_turn", "max_tokens"} {
			t.Run(parser.name+"/"+stopReason, func(t *testing.T) {
				c, _ := newTestContext()
				capture := NewAccountTestCapture()
				setAccountTestCapture(c, capture)
				err := parser.run(c, strings.NewReader(accountBudgetTestStream(stopReason)))
				content, raw, _, _, _ := capture.Result("test")
				require.Equal(t, "partial SVG output", content)
				if stopReason == "max_tokens" {
					require.ErrorContains(t, err, "stop_reason=max_tokens")
					require.Contains(t, raw, `"type":"error"`)
					require.NotContains(t, raw, `"type":"test_complete"`)
				} else {
					require.NoError(t, err)
					require.NotContains(t, raw, `"type":"error"`)
					if parser.name == "claude" {
						require.Contains(t, raw, `"type":"test_complete"`)
					}
				}
			})
		}
	}
}
