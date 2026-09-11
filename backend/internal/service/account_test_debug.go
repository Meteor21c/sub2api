package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
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

// AccountTestDebugAttempt records one concrete channel probe. Group tests can
// contain more than one attempt because a failed channel is excluded and the
// normal scheduler is asked for the next channel in the same group.
type AccountTestDebugAttempt struct {
	AccountID   int64                  `json:"account_id"`
	AccountName string                 `json:"account_name"`
	Model       string                 `json:"model,omitempty"`
	Endpoint    string                 `json:"endpoint,omitempty"`
	StatusCode  int                    `json:"status_code,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Timing      AccountTestDebugTiming `json:"timing"`
	Success     bool                   `json:"success"`
}

// AccountTestDebugResult is returned by the administrator-only channel test
// endpoint.  RawResponse contains the redacted SSE events emitted by the
// account tester, never account credentials or upstream request headers.
type AccountTestDebugResult struct {
	AccountID         int64                     `json:"account_id"`
	AccountName       string                    `json:"account_name"`
	GroupID           *int64                    `json:"group_id,omitempty"`
	GroupName         string                    `json:"group_name,omitempty"`
	RequestedModel    string                    `json:"requested_model,omitempty"`
	Content           string                    `json:"content"`
	RawResponse       string                    `json:"raw_response"`
	Model             string                    `json:"model"`
	Endpoint          string                    `json:"endpoint,omitempty"`
	StatusCode        int                       `json:"status_code,omitempty"`
	Error             string                    `json:"error,omitempty"`
	Timing            AccountTestDebugTiming    `json:"timing"`
	Usage             TestUsage                 `json:"usage"`
	Billing           AccountTestDebugBilling   `json:"billing"`
	Success           bool                      `json:"success"`
	Attempts          []AccountTestDebugAttempt `json:"attempts,omitempty"`
	FailoverAttempted bool                      `json:"failover_attempted"`
	FailoverSucceeded bool                      `json:"failover_succeeded"`
}

func isOpenAICompatibleGroupPlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		return true
	default:
		return false
	}
}

const maxAccountTestErrorLength = 2048

// TestGroupDebug selects accounts through the normal group scheduler and runs
// the same non-billing upstream probe used by the account debugger. A failed
// channel is excluded for the remainder of this test, so the next selection
// can exercise the group's real failover path.
func (s *AccountTestService) TestGroupDebug(c *gin.Context, groupID int64, modelID, prompt string) (*AccountTestDebugResult, error) {
	if s == nil || s.gatewayService == nil {
		return nil, fmt.Errorf("group scheduler is unavailable")
	}
	if groupID <= 0 {
		return nil, fmt.Errorf("invalid group ID")
	}

	ctx := c.Request.Context()
	requestedModel := strings.TrimSpace(modelID)
	publicModel := requestedModel
	group, resolvedGroupID, err := s.gatewayService.resolveGatewayGroup(ctx, &groupID)
	if err != nil {
		return nil, err
	}
	if group == nil || resolvedGroupID == nil {
		return nil, ErrGroupNotFound
	}
	probeModel := requestedModel
	if probeModel == "" && group.Platform != PlatformComposite {
		probeModel = defaultGroupDebugModel(group.Platform)
		requestedModel = probeModel
	}
	if group.Platform == PlatformComposite {
		if requestedModel == "" {
			return nil, fmt.Errorf("a model is required when testing a composite group")
		}
		decision, ok, resolveErr := s.gatewayService.resolveCompositeRouteDecision(
			ctx, group, requestedModel, CompositeRouteEndpointAny,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !ok {
			return nil, fmt.Errorf("no composite route supports model: %s", requestedModel)
		}
		probeModel = decision.UpstreamModel
		ctx = WithCompositeRouteDecision(ctx, decision)
		c.Request = c.Request.WithContext(ctx)
		// The scheduler must filter on the concrete upstream model, while the
		// request context retains the public model for billing/routing metadata.
		requestedModel = probeModel
	}

	sessionHash := fmt.Sprintf("admin-group-test-%d", time.Now().UnixNano())
	excludedIDs := make(map[int64]struct{})
	attemptedIDs := make(map[int64]struct{})
	attempts := make([]AccountTestDebugAttempt, 0, 2)
	var lastResult *AccountTestDebugResult

	for {
		selection, selectErr := s.selectGroupDebugAccountWithWait(ctx, resolvedGroupID, sessionHash, requestedModel, group.Platform, excludedIDs)
		if selectErr != nil || selection == nil || selection.Account == nil {
			message := "no available account in group"
			if selectErr != nil {
				message = fmt.Sprintf("no available account in group: %s", selectErr.Error())
			}
			if lastResult != nil {
				lastResult.Error = appendAccountTestError(lastResult.Error, message)
				lastResult.Attempts = attempts
				lastResult.FailoverAttempted = len(excludedIDs) > 0
				lastResult.FailoverSucceeded = false
				lastResult.GroupID = resolvedGroupID
				lastResult.GroupName = group.Name
				lastResult.RequestedModel = publicModel
				return lastResult, nil
			}
			return &AccountTestDebugResult{
				GroupID:           resolvedGroupID,
				GroupName:         group.Name,
				RequestedModel:    publicModel,
				Model:             probeModel,
				Error:             message,
				Attempts:          attempts,
				Success:           false,
				FailoverAttempted: len(excludedIDs) > 0,
			}, nil
		}

		account := selection.Account
		if _, alreadyAttempted := attemptedIDs[account.ID]; alreadyAttempted {
			message := fmt.Sprintf("scheduler returned channel %s (%d) again after it was excluded", account.Name, account.ID)
			if lastResult == nil {
				lastResult = newFailedAccountTestResult(account, probeModel, message)
			}
			lastResult.Error = appendAccountTestError(lastResult.Error, message)
			lastResult.Attempts = attempts
			lastResult.FailoverAttempted = len(excludedIDs) > 0
			lastResult.FailoverSucceeded = false
			lastResult.GroupID = resolvedGroupID
			lastResult.GroupName = group.Name
			lastResult.RequestedModel = publicModel
			return lastResult, nil
		}
		attemptedIDs[account.ID] = struct{}{}

		if !selection.Acquired {
			message := "channel was selected but not tested because its concurrency capacity is full"
			attempt := AccountTestDebugAttempt{
				AccountID:   account.ID,
				AccountName: account.Name,
				Model:       probeModel,
				Endpoint:    safeAccountTestEndpoint(accountTestEndpointHint(account, probeModel)),
				Error:       message,
				Success:     false,
			}
			attempts = append(attempts, attempt)
			s.gatewayService.ReleaseAccountSession(context.Background(), account, sessionHash)
			excludedIDs[account.ID] = struct{}{}
			lastResult = failedAccountTestResultFromAttempt(attempt)
			continue
		}

		result, testErr := func() (*AccountTestDebugResult, error) {
			defer s.gatewayService.ReleaseAccountSession(context.Background(), account, sessionHash)
			if selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			// The V2 observer uses this request-scoped value to attach the
			// concrete scheduler choice to test_start before the upstream call.
			// It is deliberately set only around the real account probe.
			setAccountTestActiveAccount(c, account)
			defer setAccountTestActiveAccount(c, nil)
			return s.TestAccountDebug(c, account.ID, probeModel, prompt)
		}()
		if testErr != nil {
			return nil, testErr
		}
		if result == nil {
			return nil, fmt.Errorf("channel test returned no result")
		}

		if result.AccountID == 0 {
			result.AccountID = account.ID
		}
		if result.AccountName == "" {
			result.AccountName = account.Name
		}
		attempts = append(attempts, result.Attempts...)
		if len(result.Attempts) == 0 {
			attempts = append(attempts, accountTestAttemptFromResult(result))
		}
		result.GroupID = resolvedGroupID
		result.GroupName = group.Name
		result.RequestedModel = publicModel
		result.Attempts = attempts
		result.FailoverAttempted = len(attempts) > 1
		result.FailoverSucceeded = result.Success && len(attempts) > 1
		if result.Success {
			return result, nil
		}

		excludedIDs[account.ID] = struct{}{}
		lastResult = result
	}
}

const (
	groupDebugSelectionRetryInterval = 250 * time.Millisecond
	groupDebugMaxWait                = 10 * time.Second
)

func defaultGroupDebugModel(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		return openai.DefaultTestModel
	case PlatformGemini:
		return geminicli.DefaultTestModel
	case PlatformGrok:
		return grokDefaultResponsesModel
	default:
		return claude.DefaultTestModel
	}
}

// selectGroupDebugAccount mirrors production scheduler selection for the
// OpenAI-compatible platforms, including endpoint capability checks and
// composite target-platform overrides. Other providers use the shared
// load-aware scheduler used by their gateway handlers.
func (s *AccountTestService) selectGroupDebugAccount(
	ctx context.Context,
	groupID *int64,
	sessionHash, requestedModel, groupPlatform string,
	excludedIDs map[int64]struct{},
) (*AccountSelectionResult, error) {
	platform := strings.ToLower(strings.TrimSpace(groupPlatform))
	if resolved, ok := ResolvedTargetPlatformFromContext(ctx); ok {
		platform = strings.ToLower(strings.TrimSpace(resolved))
	}
	if isOpenAICompatibleGroupPlatform(platform) && s.openaiGatewayService != nil {
		capability := OpenAIEndpointCapabilityChatCompletions
		if isOpenAIImageModel(requestedModel) {
			capability = OpenAIEndpointCapabilityResponses
		} else if platform == PlatformGrok && (isGrokImageGenerationModel(requestedModel) || isGrokVideoGenerationModel(requestedModel)) {
			capability = OpenAIEndpointCapabilityGrokMediaGeneration
		}
		returnSelection, _, err := s.openaiGatewayService.SelectAccountWithSchedulerForCapability(
			ctx,
			groupID,
			"",
			sessionHash,
			requestedModel,
			excludedIDs,
			OpenAIUpstreamTransportAny,
			capability,
			false,
			false,
			true,
			platform,
		)
		return returnSelection, err
	}
	return s.gatewayService.SelectAccountWithLoadAwareness(
		ctx, groupID, sessionHash, requestedModel, excludedIDs, "", 0,
	)
}

// selectGroupDebugAccountWithWait preserves the normal scheduler's bounded
// wait for a temporarily full channel. Once that wait is exhausted, the
// caller can record the channel as unavailable and continue with the group's
// next candidate instead of returning an opaque "no result" response.
func (s *AccountTestService) selectGroupDebugAccountWithWait(
	ctx context.Context,
	groupID *int64,
	sessionHash, requestedModel, groupPlatform string,
	excludedIDs map[int64]struct{},
) (*AccountSelectionResult, error) {
	selection, err := s.selectGroupDebugAccount(ctx, groupID, sessionHash, requestedModel, groupPlatform, excludedIDs)
	if err != nil || selection == nil || selection.Acquired || selection.WaitPlan == nil {
		return selection, err
	}

	waitFor := selection.WaitPlan.Timeout
	if waitFor <= 0 || waitFor > groupDebugMaxWait {
		waitFor = groupDebugMaxWait
	}
	deadline := time.Now().Add(waitFor)
	for !selection.Acquired && selection.WaitPlan != nil {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		delay := groupDebugSelectionRetryInterval
		if remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}

		selection, err = s.selectGroupDebugAccount(ctx, groupID, sessionHash, requestedModel, groupPlatform, excludedIDs)
		if err != nil || selection == nil || selection.Acquired || selection.WaitPlan == nil {
			return selection, err
		}
	}
	return selection, nil
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
	endpoint   string
	statusCode int
	errorMsg   string
	usage      TestUsage
	usageSeen  bool
	firstAt    time.Time
	completed  bool
	failed     bool
	observer   AccountTestEventObserver
}

const accountTestCaptureContextKey = "account_test_debug_capture"

// AccountTestEventObserver receives the same sanitized events that the legacy
// SSE test endpoint emits.  It is intentionally attached to the request
// context rather than the AccountTestService so concurrent account tests do
// not share mutable observers.
type AccountTestEventObserver func(TestEvent)

const (
	accountTestEventObserverContextKey = "account_test_event_observer"
	accountTestActiveAccountContextKey = "account_test_active_account"
)

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

func setAccountTestEventObserver(c *gin.Context, observer AccountTestEventObserver) {
	if c != nil {
		c.Set(accountTestEventObserverContextKey, observer)
	}
}

func accountTestEventObserverFromContext(c *gin.Context) AccountTestEventObserver {
	if c == nil {
		return nil
	}
	value, exists := c.Get(accountTestEventObserverContextKey)
	if !exists {
		return nil
	}
	observer, _ := value.(AccountTestEventObserver)
	return observer
}

func setAccountTestActiveAccount(c *gin.Context, account *Account) {
	if c != nil {
		c.Set(accountTestActiveAccountContextKey, account)
	}
}

func accountTestActiveAccountFromContext(c *gin.Context) *Account {
	if c == nil {
		return nil
	}
	value, exists := c.Get(accountTestActiveAccountContextKey)
	if !exists {
		return nil
	}
	account, _ := value.(*Account)
	return account
}

func (capture *AccountTestCapture) record(event TestEvent) {
	if capture == nil {
		return
	}
	now := time.Now()
	capture.mu.Lock()
	if capture.startedAt.IsZero() {
		capture.startedAt = now
	}
	capture.events = append(capture.events, event)
	capture.eventTimes = append(capture.eventTimes, now)
	if event.Model != "" {
		capture.model = event.Model
	}
	if event.Endpoint != "" {
		capture.endpoint = safeAccountTestEndpoint(event.Endpoint)
	}
	if event.StatusCode > 0 {
		capture.statusCode = event.StatusCode
	}
	if event.Error != "" {
		capture.errorMsg = event.Error
		if capture.statusCode == 0 {
			capture.statusCode = accountTestStatusCode(event.Error)
		}
	}
	if event.Text != "" && event.Type == "content" {
		_, _ = capture.content.WriteString(event.Text)
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
	observer := capture.observer
	capture.mu.Unlock()
	if observer != nil {
		observer(event)
	}
}

// Metadata returns failure and request metadata collected from the safe test
// event stream. It is separate from Result to keep the original capture API
// used by the existing SSE parser tests stable.
func (capture *AccountTestCapture) Metadata() (errorMsg, endpoint string, statusCode int) {
	if capture == nil {
		return "", "", 0
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return truncateAccountTestError(capture.errorMsg), capture.endpoint, capture.statusCode
}

func (s *AccountTestService) sendTestStart(c *gin.Context, model, endpoint string) {
	s.sendEvent(c, TestEvent{
		Type:     "test_start",
		Model:    model,
		Endpoint: safeAccountTestEndpoint(endpoint),
	})
}

func safeAccountTestEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err == nil {
		parsed.User = nil
		query := parsed.Query()
		for key := range query {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "key") || strings.Contains(lower, "token") ||
				strings.Contains(lower, "secret") || strings.Contains(lower, "password") ||
				strings.Contains(lower, "auth") {
				query.Set(key, "[redacted]")
			}
		}
		parsed.RawQuery = query.Encode()
		raw = parsed.String()
	}
	if len(raw) > 512 {
		return raw[:512] + "..."
	}
	return raw
}

func accountTestStatusCode(message string) int {
	for _, token := range strings.Fields(message) {
		token = strings.Trim(token, "()[]{}:;,\"")
		if len(token) != 3 {
			continue
		}
		if token[0] < '1' || token[0] > '5' || token[1] < '0' || token[1] > '9' || token[2] < '0' || token[2] > '9' {
			continue
		}
		return int(token[0]-'0')*100 + int(token[1]-'0')*10 + int(token[2]-'0')
	}
	return 0
}

func truncateAccountTestError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxAccountTestErrorLength {
		return message
	}
	return message[:maxAccountTestErrorLength] + "..."
}

func appendAccountTestError(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = truncateAccountTestError(next)
	if existing == "" {
		return next
	}
	if next == "" || strings.Contains(existing, next) {
		return existing
	}
	return truncateAccountTestError(existing + "; " + next)
}

func accountTestEndpointHint(account *Account, modelID string) string {
	if account == nil {
		return ""
	}
	modelID = strings.TrimSpace(modelID)
	if account.IsOpenAI() {
		if account.IsOAuth() {
			return chatgptCodexAPIURL
		}
		base := strings.TrimRight(strings.TrimSpace(account.GetOpenAIBaseURL()), "/")
		if base == "" {
			base = "https://api.openai.com"
		}
		if isOpenAIImageModel(modelID) {
			return buildOpenAIImagesURL(base, openAIImagesGenerationsEndpoint)
		}
		if account.GetAPIProtocol() == APIProtocolChatCompletions {
			return buildOpenAIChatCompletionsURL(base)
		}
		return buildOpenAIResponsesURLForPlatform(account.Platform, base)
	}
	if account.IsGemini() {
		return "Gemini generateContent endpoint"
	}
	if account.Platform == PlatformGrok {
		return "Grok connectivity endpoint"
	}
	if account.Platform == PlatformAntigravity {
		return "Antigravity gateway test endpoint"
	}
	base := strings.TrimRight(strings.TrimSpace(account.GetBaseURL()), "/")
	if base == "" {
		return testClaudeAPIURL
	}
	return base + "/v1/messages?beta=true"
}

func accountTestAttemptFromResult(result *AccountTestDebugResult) AccountTestDebugAttempt {
	if result == nil {
		return AccountTestDebugAttempt{}
	}
	return AccountTestDebugAttempt{
		AccountID:   result.AccountID,
		AccountName: result.AccountName,
		Model:       result.Model,
		Endpoint:    result.Endpoint,
		StatusCode:  result.StatusCode,
		Error:       result.Error,
		Timing:      result.Timing,
		Success:     result.Success,
	}
}

func failedAccountTestResultFromAttempt(attempt AccountTestDebugAttempt) *AccountTestDebugResult {
	return &AccountTestDebugResult{
		AccountID:   attempt.AccountID,
		AccountName: attempt.AccountName,
		Model:       attempt.Model,
		Endpoint:    attempt.Endpoint,
		StatusCode:  attempt.StatusCode,
		Error:       attempt.Error,
		Timing:      attempt.Timing,
		Attempts:    []AccountTestDebugAttempt{attempt},
		Success:     false,
	}
}

func newFailedAccountTestResult(account *Account, modelID, message string) *AccountTestDebugResult {
	return failedAccountTestResultFromAttempt(AccountTestDebugAttempt{
		AccountID:   account.ID,
		AccountName: account.Name,
		Model:       strings.TrimSpace(modelID),
		Endpoint:    safeAccountTestEndpoint(accountTestEndpointHint(account, modelID)),
		Error:       truncateAccountTestError(message),
		Success:     false,
	})
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
	requestedModel := strings.TrimSpace(modelID)
	accountName := ""
	endpointHint := ""
	if s.accountRepo != nil {
		if account, accountErr := s.accountRepo.GetByID(c.Request.Context(), accountID); accountErr == nil && account != nil {
			accountName = account.Name
			endpointHint = accountTestEndpointHint(account, requestedModel)
		}
	}
	capture := NewAccountTestCapture()
	capture.observer = accountTestEventObserverFromContext(c)
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
	errorMsg, endpoint, statusCode := capture.Metadata()
	if resolvedModel == "" {
		resolvedModel = requestedModel
	}
	if endpoint == "" {
		endpoint = safeAccountTestEndpoint(endpointHint)
	}
	if errorMsg == "" && err != nil {
		errorMsg = truncateAccountTestError(err.Error())
	}
	if statusCode == 0 {
		statusCode = accountTestStatusCode(errorMsg)
	}

	billing := AccountTestDebugBilling{CostSource: "unavailable"}
	if err == nil && s.billingService != nil && resolvedModel != "" {
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

	result := &AccountTestDebugResult{
		AccountID:      accountID,
		AccountName:    accountName,
		RequestedModel: requestedModel,
		Content:        content,
		RawResponse:    raw,
		Model:          resolvedModel,
		Endpoint:       endpoint,
		StatusCode:     statusCode,
		Timing:         timing,
		Usage:          usage,
		Billing:        billing,
		Error:          errorMsg,
		Success:        err == nil,
	}
	result.Attempts = []AccountTestDebugAttempt{accountTestAttemptFromResult(result)}
	return result, nil
}
