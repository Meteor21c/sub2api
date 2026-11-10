package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// ModelTestService owns a durable, bounded queue. User jobs re-enter the full
// gateway router (auth, ACL, subscription, quota, scheduler and billing), never
// the free administrator probe. Credentials are read just before execution.
type ModelTestService struct {
	repo        ModelTestRepository
	keys        APIKeyRepository
	groups      GroupRepository
	accountTest *AccountTestService
	mu          sync.RWMutex
	gateway     http.Handler
	startOnce   sync.Once
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	workerID    string
}

func NewModelTestService(repo ModelTestRepository, keys APIKeyRepository, groups GroupRepository, accountTest *AccountTestService) *ModelTestService {
	return &ModelTestService{repo: repo, keys: keys, groups: groups, accountTest: accountTest, workerID: uuid.NewString()}
}

// Start is called only after all gateway routes and middleware are registered.
func (s *ModelTestService) Start(gateway http.Handler) {
	s.startOnce.Do(func() {
		s.mu.Lock()
		s.gateway = gateway
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.mu.Unlock()
		s.wg.Add(3)
		go s.scheduleLoop(ctx)
		for i := 0; i < 2; i++ {
			go s.workerLoop(ctx)
		}
	})
}

func (s *ModelTestService) Stop() {
	if s == nil {
		return
	}
	s.mu.RLock()
	cancel := s.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

func modelTestPrompt(kind string) string {
	switch kind {
	case "pelican":
		return ModelTestPelicanPrompt
	case "candy":
		return ModelTestCandyPrompt
	default:
		return ""
	}
}

func validModelTestModel(model string) bool {
	if model == "" || len(model) > 200 || !utf8.ValidString(model) || strings.ContainsAny(model, "\r\n\x00") {
		return false
	}
	// Only text inference belongs in these fixed-prompt tests.
	lower := strings.ToLower(model)
	for _, part := range []string{"image", "seedance", "sora", "veo-", "kling", "tencent-mps", "whisper", "embedding", "tts-", "suno", "dall-e"} {
		if strings.Contains(lower, part) {
			return false
		}
	}
	return true
}

func (s *ModelTestService) ownedKey(ctx context.Context, owner, keyID int64) (*APIKey, error) {
	k, err := s.keys.GetByID(ctx, keyID)
	if err != nil || k == nil || k.UserID != owner || !k.IsActive() || k.IsExpired() || k.IsQuotaExhausted() || k.GroupID == nil || *k.GroupID <= 0 || k.Key == "" {
		return nil, ErrModelTestKey
	}
	return k, nil
}

func (s *ModelTestService) KeyOptions(ctx context.Context, owner int64) ([]ModelTestKeyOption, error) {
	keys, _, err := s.keys.ListByUserID(ctx, owner, pagination.PaginationParams{Page: 1, PageSize: 1000}, APIKeyListFilters{Status: StatusActive})
	if err != nil {
		return nil, err
	}
	items := make([]ModelTestKeyOption, 0, len(keys))
	for _, k := range keys {
		if k.UserID != owner || !k.IsActive() || k.IsExpired() || k.IsQuotaExhausted() || k.GroupID == nil {
			continue
		}
		groupName := ""
		if k.Group != nil {
			groupName = k.Group.Name
		}
		items = append(items, ModelTestKeyOption{ID: k.ID, Name: k.Name, GroupName: groupName})
	}
	return items, nil
}

func (s *ModelTestService) GroupOptions(ctx context.Context) ([]ModelTestGroupOption, error) {
	groups, err := s.groups.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ModelTestGroupOption, 0, len(groups))
	for _, g := range groups {
		items = append(items, ModelTestGroupOption{ID: g.ID, Name: g.Name})
	}
	return items, nil
}

func (s *ModelTestService) Models(ctx context.Context, owner, keyID int64, clientIP string) ([]string, error) {
	k, err := s.ownedKey(ctx, owner, keyID)
	if err != nil {
		return nil, err
	}
	response, err := s.callGateway(ctx, k.Key, clientIP, uuid.NewString(), http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if response.status < 200 || response.status >= 300 {
		return nil, ErrModelTestKey
	}
	items := make([]string, 0)
	seen := map[string]bool{}
	for _, entry := range gjson.GetBytes(response.body.Bytes(), "data").Array() {
		model := entry.Get("id").String()
		if validModelTestModel(model) && !seen[model] {
			seen[model] = true
			items = append(items, model)
		}
	}
	sort.Strings(items)
	return items, nil
}

func (s *ModelTestService) Submit(ctx context.Context, owner int64, clientIP string, in ModelTestSubmitInput) (*ModelTestJob, error) {
	in.Model = strings.TrimSpace(in.Model)
	if owner <= 0 || in.APIKeyID <= 0 || !validModelTestModel(in.Model) || modelTestPrompt(in.TestKind) == "" || net.ParseIP(clientIP) == nil {
		return nil, ErrModelTestInvalid
	}
	requestID, err := uuid.Parse(in.ClientRequestID)
	if err != nil {
		return nil, ErrModelTestInvalid
	}
	k, err := s.ownedKey(ctx, owner, in.APIKeyID)
	if err != nil {
		return nil, err
	}
	models, err := s.Models(ctx, owner, in.APIKeyID, clientIP)
	if err != nil {
		return nil, err
	}
	found := false
	for _, m := range models {
		if m == in.Model {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrModelTestInvalid
	}
	label := k.Name
	if k.Group != nil {
		label = k.Group.Name
	}
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:80])
	}
	return s.repo.CreateUserJob(ctx, &ModelTestJob{ID: uuid.NewString(), Source: "user", OwnerUserID: &owner,
		APIKeyID: &in.APIKeyID, GroupID: *k.GroupID, Label: label, Model: in.Model, TestKind: in.TestKind,
		ClientIP: clientIP, ClientRequestID: requestID.String(), Status: "queued", Verdict: "ungraded"})
}

func (s *ModelTestService) ListPlans(ctx context.Context) ([]ModelTestPlan, error) {
	return s.repo.ListPlans(ctx)
}
func (s *ModelTestService) SavePlan(ctx context.Context, actor, id int64, in ModelTestPlanInput) (*ModelTestPlan, error) {
	in.Label = strings.TrimSpace(in.Label)
	in.Model = strings.TrimSpace(in.Model)
	if id < 0 || actor <= 0 || in.GroupID <= 0 || in.Label == "" || len([]rune(in.Label)) > 80 || !validModelTestModel(in.Model) || modelTestPrompt(in.TestKind) == "" {
		return nil, ErrModelTestInvalid
	}
	g, err := s.groups.GetByID(ctx, in.GroupID)
	if err != nil || g == nil || g.Status != StatusActive || (g.ModelAllowlistEnabled() && !g.ModelAllowlist.Allows(in.Model)) {
		return nil, ErrModelTestInvalid
	}
	return s.repo.SavePlan(ctx, id, actor, in)
}
func (s *ModelTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.repo.DeletePlan(ctx, id)
}
func (s *ModelTestService) ListJobs(ctx context.Context, owner int64, source string) ([]ModelTestJob, error) {
	if source != "scheduled" && source != "user" {
		return nil, ErrModelTestInvalid
	}
	return s.repo.ListJobs(ctx, owner, source)
}
func (s *ModelTestService) GetJob(ctx context.Context, owner int64, source, id string) (*ModelTestJob, error) {
	if source != "scheduled" && source != "user" {
		return nil, ErrModelTestInvalid
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrModelTestNotFound
	}
	return s.repo.GetJob(ctx, owner, source, parsed.String())
}

func (s *ModelTestService) scheduleLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	lastCleanup := time.Time{}
	for {
		stepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if time.Since(lastCleanup) >= time.Minute {
			if err := s.repo.Cleanup(stepCtx); err != nil && ctx.Err() == nil {
				log.Print("[ModelTests] cleanup failed")
			}
			lastCleanup = time.Now()
		}
		if err := s.repo.EnqueueDue(stepCtx); err != nil && ctx.Err() == nil {
			log.Print("[ModelTests] scheduling failed")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *ModelTestService) workerLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		job, err := s.repo.Claim(claimCtx, s.workerID)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Print("[ModelTests] queue claim failed")
		}
		if job != nil {
			s.runJob(ctx, job)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *ModelTestService) runJob(parent context.Context, j *ModelTestJob) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, modelTestTimeout)
	defer cancel()
	// Always finish a claimed job, including panics or shutdown. Never repeat an
	// inference whose completion/charge is uncertain.
	defer func() {
		if recover() != nil {
			j.Status = "interrupted"
			j.ErrorCode = "interrupted"
			j.Content = ""
		}
		if ctx.Err() != nil {
			j.Status = "interrupted"
			j.ErrorCode = "interrupted"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				j.ErrorCode = "timeout"
			}
		}
		j.TotalMs = time.Since(started).Milliseconds()
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finishCancel()
		if err := s.repo.Finish(finishCtx, s.workerID, j); err != nil {
			log.Print("[ModelTests] result persistence failed; inference will not be retried")
		}
	}()
	j.Status = "failed"
	j.ErrorCode = "request_failed"
	j.Verdict = "ungraded"
	if j.Source == "user" {
		s.runUser(ctx, j)
	} else {
		s.runScheduled(ctx, j)
	}
	if j.Status == "succeeded" && j.TestKind == "candy" {
		j.Verdict, j.Answer = evaluateModelTestCandy(j.Content)
	}
}

func (s *ModelTestService) runUser(ctx context.Context, j *ModelTestJob) {
	if j.OwnerUserID == nil || j.APIKeyID == nil {
		j.ErrorCode = "key_unavailable"
		return
	}
	k, err := s.ownedKey(ctx, *j.OwnerUserID, *j.APIKeyID)
	if err != nil {
		j.ErrorCode = "key_unavailable"
		return
	}
	// A queued job may not silently move to a newly bound billing group.
	if *k.GroupID != j.GroupID {
		j.ErrorCode = "key_changed"
		return
	}
	maxTokens := 16384
	if j.TestKind == "candy" {
		maxTokens = 4096
	}
	body, err := json.Marshal(map[string]any{"model": j.Model, "input": modelTestPrompt(j.TestKind), "stream": false, "max_output_tokens": maxTokens})
	if err != nil {
		return
	}
	response, err := s.callGateway(ctx, k.Key, j.ClientIP, j.ID, http.MethodPost, "/v1/responses", body)
	if err != nil {
		j.ErrorCode = "service_unavailable"
		return
	}
	if response.overflow {
		j.ErrorCode = "output_too_large"
		return
	}
	if response.status < 200 || response.status >= 300 {
		j.ErrorCode = modelTestGatewayError(response.status, response.body.Bytes())
		return
	}
	content, input, output, code := parseModelTestResponse(response.body.Bytes())
	j.Content = content
	j.InputTokens = input
	j.OutputTokens = output
	if code != "" {
		j.ErrorCode = code
		return
	}
	j.Status = "succeeded"
	j.ErrorCode = ""
}

func (s *ModelTestService) runScheduled(ctx context.Context, j *ModelTestJob) {
	if s.accountTest == nil {
		j.ErrorCode = "service_unavailable"
		return
	}
	g, err := s.groups.GetByID(ctx, j.GroupID)
	if err != nil || g == nil || g.Status != StatusActive || (g.ModelAllowlistEnabled() && !g.ModelAllowlist.Allows(j.Model)) {
		j.ErrorCode = "group_unavailable"
		return
	}
	w := newModelTestResponse()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/internal/model-test", nil)
	result, err := s.accountTest.TestGroupDebug(c, j.GroupID, j.Model, modelTestPrompt(j.TestKind))
	if err != nil || result == nil || !result.Success {
		j.ErrorCode = "request_failed"
		return
	}
	if len(result.Content) > modelTestMaxBody {
		j.ErrorCode = "output_too_large"
		return
	}
	j.Content = result.Content
	if result.Usage.UsageSource == "" || result.Usage.UsageSource == "upstream" {
		j.InputTokens = int64(result.Usage.PromptTokens)
		j.OutputTokens = int64(result.Usage.CompletionTokens)
	}
	if strings.TrimSpace(j.Content) == "" {
		j.ErrorCode = "empty_output"
		return
	}
	j.Status = "succeeded"
	j.ErrorCode = ""
}

// This handler is the same engine used by external clients. No auth context or
// forwarded headers from the browser are copied into this internal request.
func (s *ModelTestService) callGateway(ctx context.Context, key, clientIP, requestID, method, path string, body []byte) (*modelTestResponse, error) {
	s.mu.RLock()
	gateway := s.gateway
	s.mu.RUnlock()
	if gateway == nil {
		return nil, ErrModelTestUnavailable
	}
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, requestID)
	req, err := http.NewRequestWithContext(ctx, method, "http://model-tests.internal"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Meteor-ModelTests/1.0")
	req.RemoteAddr = net.JoinHostPort(clientIP, "0")
	w := newModelTestResponse()
	gateway.ServeHTTP(w, req)
	return w, nil
}

type modelTestResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
	closed   chan bool
}

func newModelTestResponse() *modelTestResponse {
	return &modelTestResponse{header: make(http.Header), status: 200, closed: make(chan bool)}
}
func (w *modelTestResponse) Header() http.Header  { return w.header }
func (w *modelTestResponse) WriteHeader(code int) { w.status = code }
func (w *modelTestResponse) Write(b []byte) (int, error) {
	if w.body.Len()+len(b) > modelTestMaxBody {
		w.overflow = true
		return 0, io.ErrShortBuffer
	}
	return w.body.Write(b)
}
func (w *modelTestResponse) Flush()                   {}
func (w *modelTestResponse) CloseNotify() <-chan bool { return w.closed }

func modelTestGatewayError(status int, body []byte) string {
	// Only known categories cross this boundary. Never publish upstream messages,
	// endpoint URLs, account names or credentials from an error response.
	reason := strings.ToLower(gjson.GetBytes(body, "reason").String() + " " + gjson.GetBytes(body, "error.type").String() + " " + gjson.GetBytes(body, "error.code").String())
	if status == 402 || strings.Contains(reason, "balance") || strings.Contains(reason, "quota") {
		return "balance_or_quota"
	}
	switch status {
	case 401, 403:
		return "key_rejected"
	case 429:
		return "rate_limited"
	case 400, 404:
		return "model_unavailable"
	default:
		return "request_failed"
	}
}

func modelTestText(root gjson.Result) string {
	if text := root.Get("output_text"); text.Type == gjson.String {
		return text.String()
	}
	var content strings.Builder
	for _, item := range root.Get("output").Array() {
		for _, part := range item.Get("content").Array() {
			if part.Get("type").String() == "output_text" || part.Get("type").String() == "text" {
				_, _ = content.WriteString(part.Get("text").String())
			}
		}
	}
	if content.Len() == 0 {
		_, _ = content.WriteString(root.Get("choices.0.message.content").String())
	}
	return content.String()
}

func parseModelTestResponse(body []byte) (string, int64, int64, string) {
	var text string
	var input, output int64
	code := ""
	read := func(root gjson.Result) {
		if root.Get("error").Exists() && root.Get("error").Type != gjson.Null {
			code = "request_failed"
		}
		switch root.Get("status").String() {
		case "incomplete":
			code = "output_incomplete"
		case "failed", "cancelled":
			code = "request_failed"
		}
		input = root.Get("usage.input_tokens").Int()
		output = root.Get("usage.output_tokens").Int()
		if input == 0 {
			input = root.Get("usage.prompt_tokens").Int()
		}
		if output == 0 {
			output = root.Get("usage.completion_tokens").Int()
		}
		if content := modelTestText(root); content != "" {
			text = content
		}
	}
	trimmed := bytes.TrimSpace(body)
	if gjson.ValidBytes(trimmed) {
		read(gjson.ParseBytes(trimmed))
	} else {
		var deltas strings.Builder
		terminal := false
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 4096), modelTestMaxBody)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				terminal = true
				continue
			}
			if !gjson.Valid(data) {
				continue
			}
			event := gjson.Parse(data)
			switch event.Get("type").String() {
			case "response.output_text.delta":
				_, _ = deltas.WriteString(event.Get("delta").String())
			case "response.completed", "response.incomplete", "response.failed":
				terminal = true
				read(event.Get("response"))
			case "error":
				code = "request_failed"
			}
		}
		if scanner.Err() != nil {
			code = "output_too_large"
		}
		if text == "" {
			text = deltas.String()
		}
		if !terminal && code == "" {
			code = "output_incomplete"
		}
	}
	if strings.TrimSpace(text) == "" && code == "" {
		code = "empty_output"
	}
	return text, input, output, code
}

var modelTestAnswerPattern = regexp.MustCompile(`(?i)^FINAL_ANSWER\s*[:：]\s*(-?\d+)$`)

func evaluateModelTestCandy(content string) (string, *int64) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "ungraded", nil
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	last = strings.TrimLeft(last, "#")
	last = strings.TrimSpace(last)
	if strings.HasPrefix(last, "- ") || strings.HasPrefix(last, "* ") {
		last = last[2:]
	}
	for _, marker := range []string{"**", "__", "`"} {
		last = strings.ReplaceAll(last, marker, "")
	}
	matches := modelTestAnswerPattern.FindStringSubmatch(strings.TrimSpace(last))
	if matches == nil {
		return "ungraded", nil
	}
	answer, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil || answer > 9007199254740991 || answer < -9007199254740991 {
		return "ungraded", nil
	}
	if answer == 21 {
		return "correct", &answer
	}
	return "incorrect", &answer
}
