package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type modelTestKeysStub struct {
	APIKeyRepository
	key *APIKey
}

func (s modelTestKeysStub) GetByID(context.Context, int64) (*APIKey, error) { return s.key, nil }
func (s modelTestKeysStub) ListByUserID(context.Context, int64, pagination.PaginationParams, APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	return []APIKey{*s.key}, nil, nil
}

type modelTestRepoStub struct {
	ModelTestRepository
	mu       sync.Mutex
	job      *ModelTestJob
	finished chan *ModelTestJob
}

func (s *modelTestRepoStub) CreateUserJob(_ context.Context, j *ModelTestJob) (*ModelTestJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job = j
	return j, nil
}
func (s *modelTestRepoStub) Claim(context.Context, string) (*ModelTestJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil || s.job.Status != "queued" {
		return nil, nil
	}
	j := *s.job
	s.job.Status = "running"
	return &j, nil
}
func (s *modelTestRepoStub) Finish(_ context.Context, _ string, j *ModelTestJob) error {
	copy := *j
	if s.finished != nil {
		s.finished <- &copy
	}
	return nil
}
func (*modelTestRepoStub) Cleanup(context.Context) error    { return nil }
func (*modelTestRepoStub) EnqueueDue(context.Context) error { return nil }

func modelTestFixture() (*ModelTestService, *modelTestRepoStub) {
	group := int64(12)
	repo := &modelTestRepoStub{finished: make(chan *ModelTestJob, 2)}
	key := &APIKey{ID: 2, UserID: 7, Key: "private-test-key", Status: StatusActive, GroupID: &group, Group: &Group{ID: group, Name: "Pro"}}
	return NewModelTestService(repo, modelTestKeysStub{key: key}, nil, nil), repo
}

func TestModelTestsUserInferenceReentersGatewayAndSurvivesBrowserContext(t *testing.T) {
	svc, repo := modelTestFixture()
	started := make(chan struct{})
	finish := make(chan struct{})
	gateway := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer private-test-key", r.Header.Get("Authorization"))
		require.Equal(t, "198.51.100.20:0", r.RemoteAddr)
		require.Empty(t, r.Header.Get("X-Forwarded-For"))
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, false, body["stream"])
		require.Equal(t, ModelTestCandyPrompt, body["input"])
		close(started)
		select {
		case <-finish:
		case <-r.Context().Done():
			t.Error("browser cancellation reached inference")
			return
		}
		// The full router is responsible for the real debit. The worker receives
		// only the final, already billed response from that router.
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"content":[{"type":"output_text","text":"9 圆形 + 12 星形\nFINAL_ANSWER: 21"}]}],"usage":{"input_tokens":20,"output_tokens":30}}`)
	})
	svc.Start(gateway)
	defer svc.Stop()
	browser, cancel := context.WithCancel(context.Background())
	j, err := svc.Submit(browser, 7, "198.51.100.20", ModelTestSubmitInput{APIKeyID: 2, Model: "test-model", TestKind: "candy", ClientRequestID: uuid.NewString()})
	require.NoError(t, err)
	cancel()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not claim persisted job")
	}
	close(finish)
	select {
	case got := <-repo.finished:
		require.Equal(t, j.ID, got.ID)
		require.Equal(t, "succeeded", got.Status)
		require.Equal(t, "correct", got.Verdict)
		require.Equal(t, int64(30), got.OutputTokens)
	case <-time.After(5 * time.Second):
		t.Fatal("job did not finish")
	}
}

func TestModelTestsOwnerAndKeyRevalidation(t *testing.T) {
	svc, _ := modelTestFixture()
	_, err := svc.ownedKey(context.Background(), 8, 2)
	require.ErrorIs(t, err, ErrModelTestKey)
	owner, keyID := int64(7), int64(2)
	j := &ModelTestJob{ID: uuid.NewString(), Source: "user", OwnerUserID: &owner, APIKeyID: &keyID, GroupID: 99, TestKind: "pelican"}
	svc.runUser(context.Background(), j)
	require.Equal(t, "key_changed", j.ErrorCode)
	keys, ok := svc.keys.(modelTestKeysStub)
	require.True(t, ok)
	key := keys.key
	key.Status = StatusAPIKeyDisabled
	svc.runUser(context.Background(), j)
	require.Equal(t, "key_unavailable", j.ErrorCode)
}

func TestModelTestsGatewayDenialDoesNotPublishUpstreamDetails(t *testing.T) {
	svc, _ := modelTestFixture()
	svc.gateway = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"message":"secret-key https://private.upstream.test acct123"}}`)
	})
	owner, keyID := int64(7), int64(2)
	j := &ModelTestJob{ID: uuid.NewString(), Source: "user", OwnerUserID: &owner, APIKeyID: &keyID, GroupID: 12, TestKind: "pelican", ClientIP: "198.51.100.20"}
	svc.runJob(context.Background(), j)
	require.Equal(t, "failed", j.Status)
	require.Equal(t, "rate_limited", j.ErrorCode)
	encoded, err := json.Marshal(j)
	require.NoError(t, err)
	for _, private := range []string{"secret-key", "upstream.test", "acct123", "198.51.100.20", "owner_user_id"} {
		require.NotContains(t, string(encoded), private)
	}
}

func TestModelTestsResponseWhitespaceAndIncomplete(t *testing.T) {
	svg := `<svg viewBox="0 0 960 600"><path d="M 1 2 L 3 4"/></svg>`
	encoded, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": svg}}}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 20}})
	content, in, out, code := parseModelTestResponse(encoded)
	require.Equal(t, svg, content)
	require.Equal(t, int64(1), in)
	require.Equal(t, int64(20), out)
	require.Empty(t, code)
	var stream strings.Builder
	for _, delta := range []string{"<svg viewBox=\"0", " ", "0", " ", "960", " ", "600\"></svg>"} {
		data, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": delta})
		_, _ = stream.WriteString("data: " + string(data) + "\n\n")
	}
	_, _ = stream.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	content, _, _, code = parseModelTestResponse([]byte(stream.String()))
	require.Equal(t, `<svg viewBox="0 0 960 600"></svg>`, content)
	require.Empty(t, code)
	_, _, _, code = parseModelTestResponse([]byte(`{"status":"incomplete","output_text":"<svg"}`))
	require.Equal(t, "output_incomplete", code)
}

func TestModelTestsCandyUsesFinalAnswerOnly(t *testing.T) {
	for _, tc := range []struct {
		content, status string
		answer          *int64
	}{
		{"21 appears in reasoning, final answer is 22", "ungraded", nil},
		{"reasoning 999\nFINAL_ANSWER: 21", "correct", ptrModelTestInt(21)},
		{"FINAL_ANSWER: 22", "incorrect", ptrModelTestInt(22)},
		{"**FINAL_ANSWER: 21**", "correct", ptrModelTestInt(21)},
		{"FINAL_ANSWER: 21\nnot final", "ungraded", nil},
		{"FINAL_ANSWER: 999999999999999999999", "ungraded", nil},
	} {
		status, answer := evaluateModelTestCandy(tc.content)
		require.Equal(t, tc.status, status)
		require.Equal(t, tc.answer, answer)
	}
}
func ptrModelTestInt(v int64) *int64 { return &v }

func TestModelTestsBoundedCaptureAndSafeModels(t *testing.T) {
	w := newModelTestResponse()
	_, err := w.Write(make([]byte, modelTestMaxBody+1))
	require.True(t, errors.Is(err, io.ErrShortBuffer))
	require.True(t, w.overflow)
	require.Zero(t, w.body.Len())
	for _, model := range []string{"gpt-image-2.6", "seedance-2.0", "embedding-large", "bad\r\nheader"} {
		require.False(t, validModelTestModel(model), model)
	}
	require.True(t, validModelTestModel("gpt-6.1"))
	require.Equal(t, 2*time.Hour, ModelTestInterval)
	require.Equal(t, 24*time.Hour, ModelTestRetention)
}

func TestModelTestsRoutingSnapshotIsAdministratorOnly(t *testing.T) {
	j := &ModelTestJob{ID: uuid.NewString(), Source: "scheduled", Status: "failed"}
	applyModelTestRouting(j, &AccountTestDebugResult{
		AccountID: 19, AccountName: "private-final-channel", Success: false,
		Endpoint: "https://private-provider.example", Error: "private provider diagnostic",
		Attempts: []AccountTestDebugAttempt{
			{AccountID: 18, AccountName: "private-first-channel", Success: false, StatusCode: 503, Endpoint: "https://private-provider.example", Error: "secret diagnostic"},
			{AccountID: 19, AccountName: "private-final-channel", Success: false, StatusCode: 429},
		},
	})
	require.Equal(t, int64(19), *j.UpstreamAccountID)
	require.Len(t, j.UpstreamAttempts, 2)
	public, err := json.Marshal(j)
	require.NoError(t, err)
	require.NotContains(t, string(public), "upstream_")
	require.NotContains(t, string(public), "private-")
	admin, err := json.Marshal(ModelTestAdminView(*j))
	require.NoError(t, err)
	require.Contains(t, string(admin), `"upstream_account_id":19`)
	require.Contains(t, string(admin), "private-first-channel")
	require.NotContains(t, string(admin), "private-provider")
	require.NotContains(t, string(admin), "diagnostic")
	var empty map[string]any
	raw, err := json.Marshal(ModelTestAdminView(ModelTestJob{}))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &empty))
	require.Equal(t, []any{}, empty["upstream_attempts"])
}

type modelTestAdminRepoStub struct {
	ModelTestRepository
	actor     int64
	plan      int64
	retry     string
	requestID string
	job       ModelTestJob
}

func (r *modelTestAdminRepoStub) CreateManualJob(_ context.Context, actor, plan int64, retry, requestID string) (*ModelTestJob, error) {
	r.actor, r.plan, r.retry, r.requestID = actor, plan, retry, requestID
	return &r.job, nil
}

func TestModelTestsManualRunValidationAndAdminShape(t *testing.T) {
	repo := &modelTestAdminRepoStub{job: ModelTestJob{ID: uuid.NewString(), Source: "scheduled", Manual: true}}
	svc := NewModelTestService(repo, nil, nil, nil)
	requestID := uuid.NewString()
	for _, in := range []ModelTestManualInput{{ClientRequestID: "bad-id"}, {ClientRequestID: uuid.Nil.String()}} {
		_, err := svc.RunAdminTest(context.Background(), 1, 2, "", in)
		require.ErrorIs(t, err, ErrModelTestInvalid)
	}
	_, err := svc.RunAdminTest(context.Background(), 1, 2, uuid.NewString(), ModelTestManualInput{ClientRequestID: requestID})
	require.ErrorIs(t, err, ErrModelTestInvalid)
	_, err = svc.RunAdminTest(context.Background(), 1, 0, "bad-id", ModelTestManualInput{ClientRequestID: requestID})
	require.ErrorIs(t, err, ErrModelTestNotFound)
	require.Zero(t, repo.actor, "invalid submissions must not enter the queue")
	view, err := svc.RunAdminTest(context.Background(), 1, 2, "", ModelTestManualInput{ClientRequestID: requestID})
	require.NoError(t, err)
	require.True(t, view.Manual)
	require.Equal(t, int64(1), repo.actor)
	require.Equal(t, int64(2), repo.plan)
	require.Equal(t, requestID, repo.requestID)
	retryID := uuid.NewString()
	_, err = svc.RunAdminTest(context.Background(), 1, 0, retryID, ModelTestManualInput{ClientRequestID: uuid.NewString()})
	require.NoError(t, err)
	require.Equal(t, retryID, repo.retry)
}
