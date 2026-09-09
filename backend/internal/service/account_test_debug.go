package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// TestUsage is the usage breakdown exposed by the administrator channel
// debugger.  Values reported by an upstream are exact; values filled in by
// the debugger are explicitly marked with UsageSource="estimated".
type TestUsage struct {
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CachedTokens        int    `json:"cached_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	ReasoningTokens     int    `json:"reasoning_tokens"`
	UsageSource         string `json:"usage_source,omitempty"`
}

// AccountTestDebugTiming contains monotonic request timings in milliseconds.
type AccountTestDebugTiming struct {
	FirstResponseMs int64 `json:"first_response_ms"`
	TotalMs         int64 `json:"total_ms"`
}

// AccountTestDebugBilling is intentionally informational.  Debug requests do
// not debit an administrator or user balance.
type AccountTestDebugBilling struct {
	USD        float64 `json:"usd"`
	CostSource string  `json:"cost_source,omitempty"`
}

// AccountTestDebugResult is returned by the administrator-only channel test
// endpoint.  RawResponse contains the redacted SSE events emitted by the
// account tester, never account credentials or upstream request headers.
type AccountTestDebugResult struct {
	Content     string                  `json:"content"`
	RawResponse string                  `json:"raw_response"`
	Model       string                  `json:"model"`
	Timing      AccountTestDebugTiming  `json:"timing"`
	Usage       TestUsage               `json:"usage"`
	Billing     AccountTestDebugBilling `json:"billing"`
	Success     bool                    `json:"success"`
}

// AccountTestCapture collects the safe TestEvent stream without writing SSE
// bytes to the HTTP response.  It is used only by the dedicated debug API;
// the existing account-test endpoint continues to stream normally.
type AccountTestCapture struct {
	mu         sync.Mutex
	startedAt  time.Time
	finishedAt time.Time
	events     []TestEvent
	eventTimes []time.Time
	content    strings.Builder
	model      string
	usage      TestUsage
	usageSeen  bool
	firstAt    time.Time
	completed  bool
	failed     bool
}

const accountTestCaptureContextKey = "account_test_debug_capture"

// accountTestCaptureResponseWriter prevents the existing SSE probe code from
// committing text/event-stream headers before the debug endpoint writes its
// final JSON response. The probe still uses the same request path; only its
// output transport is buffered.
type accountTestCaptureResponseWriter struct {
	gin.ResponseWriter
}

func (w *accountTestCaptureResponseWriter) Flush() {}

func NewAccountTestCapture() *AccountTestCapture {
	return &AccountTestCapture{startedAt: time.Now()}
}

func setAccountTestCapture(c *gin.Context, capture *AccountTestCapture) {
	if c != nil {
		c.Set(accountTestCaptureContextKey, capture)
	}
}

func accountTestCaptureFromContext(c *gin.Context) *AccountTestCapture {
	if c == nil {
		return nil
	}
	value, exists := c.Get(accountTestCaptureContextKey)
	if !exists {
		return nil
	}
	capture, _ := value.(*AccountTestCapture)
	return capture
}

func (capture *AccountTestCapture) record(event TestEvent) {
	if capture == nil {
		return
	}
	now := time.Now()
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.startedAt.IsZero() {
		capture.startedAt = now
	}
	capture.events = append(capture.events, event)
	capture.eventTimes = append(capture.eventTimes, now)
	if event.Model != "" {
		capture.model = event.Model
	}
	if event.Text != "" && event.Type == "content" {
		capture.content.WriteString(event.Text)
	}
	if event.Usage != nil {
		capture.mergeUsage(*event.Usage)
	}
	if event.Type == "content" || event.Type == "image" || event.Type == "audio" || event.Type == "video" {
		if capture.firstAt.IsZero() {
			capture.firstAt = now
		}
	}
	if event.Type == "test_complete" || event.Type == "error" {
		capture.finishedAt = now
		capture.completed = event.Type == "test_complete" && event.Success
		capture.failed = event.Type == "error"
	}
}

func (capture *AccountTestCapture) mergeUsage(next TestUsage) {
	if next.PromptTokens > 0 {
		capture.usage.PromptTokens = next.PromptTokens
	}
	if next.CompletionTokens > 0 {
		capture.usage.CompletionTokens = next.CompletionTokens
	}
	if next.TotalTokens > 0 {
		capture.usage.TotalTokens = next.TotalTokens
	}
	if next.CachedTokens > 0 {
		capture.usage.CachedTokens = next.CachedTokens
	}
	if next.CacheCreationTokens > 0 {
		capture.usage.CacheCreationTokens = next.CacheCreationTokens
	}
	if next.ReasoningTokens > 0 {
		capture.usage.ReasoningTokens = next.ReasoningTokens
	}
	if next.UsageSource != "" {
		capture.usage.UsageSource = next.UsageSource
	}
	capture.usageSeen = true
}

// extractTestUsage accepts OpenAI, Responses, Anthropic, Gemini and common
// OpenAI-compatible usage shapes.  It returns nil when no usage object is
// present, allowing the caller to distinguish unknown from zero.
func extractTestUsage(data map[string]any) *TestUsage {
	if data == nil {
		return nil
	}
	raw, ok := data["usage"].(map[string]any)
	if !ok {
		// Gemini uses usageMetadata rather than usage. Compatible gateways may
		// preserve that native name while wrapping the response under response.
		raw, ok = data["usageMetadata"].(map[string]any)
	}
	if !ok {
		if response, responseOK := data["response"].(map[string]any); responseOK {
			raw, ok = response["usage"].(map[string]any)
			if !ok {
				raw, ok = response["usageMetadata"].(map[string]any)
			}
		}
	}
	if !ok || raw == nil {
		return nil
	}
	usage := &TestUsage{
		PromptTokens:        testUsageInt(raw, "prompt_tokens", "input_tokens", "promptTokenCount", "inputTokenCount"),
		CompletionTokens:    testUsageInt(raw, "completion_tokens", "output_tokens", "candidatesTokenCount", "outputTokenCount"),
		TotalTokens:         testUsageInt(raw, "total_tokens", "totalTokenCount"),
		CachedTokens:        testUsageInt(raw, "cached_tokens", "prompt_cache_hit_tokens", "cache_read_input_tokens", "cachedContentTokenCount"),
		CacheCreationTokens: testUsageInt(raw, "cache_creation_tokens", "cache_creation_input_tokens"),
		ReasoningTokens:     testUsageInt(raw, "reasoning_tokens", "thoughtsTokenCount"),
		UsageSource:         "upstream",
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details", "completion_tokens_details", "output_tokens_details"} {
		if details, ok := raw[key].(map[string]any); ok {
			if usage.CachedTokens == 0 {
				usage.CachedTokens = testUsageInt(details, "cached_tokens", "cache_read_input_tokens")
			}
			if usage.ReasoningTokens == 0 {
				usage.ReasoningTokens = testUsageInt(details, "reasoning_tokens")
			}
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return usage
}

func testUsageInt(values map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			if value > 0 {
				return int(value)
			}
		case float32:
			if value > 0 {
				return int(value)
			}
		case int:
			if value > 0 {
				return value
			}
		case json.Number:
			if parsed, err := value.Int64(); err == nil && parsed > 0 {
				return int(parsed)
			}
		}
	}
	return 0
}

func estimateTestTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return max(1, (utf8.RuneCountInString(value)+3)/4)
}

// Result finalizes timing and fills missing usage fields conservatively.  A
// missing upstream usage object is never presented as an exact count.
func (capture *AccountTestCapture) Result(prompt string) (content, raw string, model string, timing AccountTestDebugTiming, usage TestUsage) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	end := capture.finishedAt
	if end.IsZero() {
		end = time.Now()
	}
	if capture.startedAt.IsZero() {
		capture.startedAt = end
	}
	first := capture.firstAt
	if first.IsZero() {
		first = end
	}
	timing = AccountTestDebugTiming{
		FirstResponseMs: nonNegativeDurationMs(first.Sub(capture.startedAt)),
		TotalMs:         nonNegativeDurationMs(end.Sub(capture.startedAt)),
	}
	content = capture.content.String()
	model = capture.model
	usage = capture.usage
	if !capture.usageSeen {
		usage.UsageSource = "estimated"
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateTestTokens(prompt)
		if capture.usageSeen {
			usage.UsageSource = "mixed"
		}
	}
	if usage.CompletionTokens == 0 && content != "" {
		usage.CompletionTokens = estimateTestTokens(content)
		if capture.usageSeen {
			usage.UsageSource = "mixed"
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	var rawEvents []string
	for _, event := range capture.events {
		encoded, err := json.Marshal(event)
		if err == nil {
			rawEvents = append(rawEvents, string(encoded))
		}
	}
	raw = strings.Join(rawEvents, "\n")
	return
}

func nonNegativeDurationMs(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

// TestAccountDebug executes the existing account tester while capturing its
// safe event stream for the dedicated administrator channel test page.
func (s *AccountTestService) TestAccountDebug(c *gin.Context, accountID int64, modelID, prompt string) (*AccountTestDebugResult, error) {
	if s == nil {
		return nil, fmt.Errorf("account test service is unavailable")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "hello"
	}
	capture := NewAccountTestCapture()
	setAccountTestCapture(c, capture)
	originalWriter := c.Writer
	if originalWriter != nil {
		c.Writer = &accountTestCaptureResponseWriter{ResponseWriter: originalWriter}
	}
	defer func() {
		setAccountTestCapture(c, nil)
		c.Writer = originalWriter
	}()

	err := s.TestAccountConnection(c, accountID, modelID, prompt, AccountTestModeDefault)
	content, raw, resolvedModel, timing, usage := capture.Result(prompt)
	if resolvedModel == "" {
		resolvedModel = strings.TrimSpace(modelID)
	}
	if err != nil {
		return nil, err
	}

	billing := AccountTestDebugBilling{CostSource: "unavailable"}
	if s.billingService != nil && resolvedModel != "" {
		breakdown, billingErr := s.billingService.CalculateCost(resolvedModel, UsageTokens{
			InputTokens:         usage.PromptTokens,
			OutputTokens:        usage.CompletionTokens,
			CacheReadTokens:     usage.CachedTokens,
			CacheCreationTokens: usage.CacheCreationTokens,
		}, 1)
		if billingErr == nil && breakdown != nil {
			billing.USD = breakdown.ActualCost
			billing.CostSource = "catalog"
		}
	}

	return &AccountTestDebugResult{
		Content:     content,
		RawResponse: raw,
		Model:       resolvedModel,
		Timing:      timing,
		Usage:       usage,
		Billing:     billing,
		Success:     true,
	}, nil
}
