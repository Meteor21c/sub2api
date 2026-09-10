package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtractTestUsageSupportsCommonProviderShapes(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want TestUsage
	}{
		{
			name: "openai responses",
			data: map[string]any{"usage": map[string]any{
				"input_tokens":         12.0,
				"output_tokens":        4.0,
				"total_tokens":         16.0,
				"input_tokens_details": map[string]any{"cached_tokens": 3.0},
			}},
			want: TestUsage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16, CachedTokens: 3, UsageSource: "upstream"},
		},
		{
			name: "anthropic",
			data: map[string]any{"usage": map[string]any{
				"input_tokens":                8.0,
				"output_tokens":               2.0,
				"cache_read_input_tokens":     5.0,
				"cache_creation_input_tokens": 1.0,
			}},
			want: TestUsage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10, CachedTokens: 5, CacheCreationTokens: 1, UsageSource: "upstream"},
		},
		{
			name: "gemini",
			data: map[string]any{"usageMetadata": map[string]any{
				"promptTokenCount":     7.0,
				"candidatesTokenCount": 3.0,
				"totalTokenCount":      10.0,
				"thoughtsTokenCount":   2.0,
			}},
			want: TestUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10, ReasoningTokens: 2, UsageSource: "upstream"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, &tt.want, extractTestUsage(tt.data))
		})
	}
}

func TestAccountTestCaptureResultUsesFirstContentAndEstimatesMissingUsage(t *testing.T) {
	capture := NewAccountTestCapture()
	capture.startedAt = time.Now().Add(-20 * time.Millisecond)
	capture.record(TestEvent{Type: "test_start", Model: "test-model"})
	capture.record(TestEvent{Type: "content", Text: "hello back"})
	capture.record(TestEvent{Type: "test_complete", Success: true})

	content, raw, model, timing, usage := capture.Result("hello")

	require.Equal(t, "hello back", content)
	require.Equal(t, "test-model", model)
	require.GreaterOrEqual(t, timing.FirstResponseMs, int64(0))
	require.GreaterOrEqual(t, timing.TotalMs, timing.FirstResponseMs)
	require.Equal(t, "estimated", usage.UsageSource)
	require.Greater(t, usage.PromptTokens, 0)
	require.Greater(t, usage.CompletionTokens, 0)
	require.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)

	var event TestEvent
	require.NoError(t, json.Unmarshal([]byte(strings.Split(raw, "\n")[0]), &event))
	require.Equal(t, "test_start", event.Type)
}

func TestCreateTestPayloadUsesRequestedPrompt(t *testing.T) {
	payload, err := createTestPayload("claude-test", "hello from debugger")
	require.NoError(t, err)

	messages, ok := payload["messages"].([]map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, messages)
	content, ok := messages[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, content)
	require.Equal(t, "hello from debugger", content[0]["text"])

	openAI := createOpenAITestPayload("gpt-test", false, "hello from debugger")
	input, ok := openAI["input"].([]map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, input)
	inputContent, ok := input[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, inputContent)
	require.Equal(t, "hello from debugger", inputContent[0]["text"])
}
