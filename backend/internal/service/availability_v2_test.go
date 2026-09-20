package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type availabilityV2TestRepo struct {
	conversation      *AvailabilityConversation
	conversationOwner int64
	turn              *AvailabilityTurn
	attempts          []*AvailabilityAttempt
	events            []AvailabilityEvent
	nextAttemptID     int64
}

func (r *availabilityV2TestRepo) CreateConversation(_ context.Context, ownerUserID, groupID int64, accountID *int64, model, title string) (*AvailabilityConversation, error) {
	r.conversationOwner = ownerUserID
	r.conversation = &AvailabilityConversation{ID: 1, GroupID: groupID, AccountID: accountID, Model: model, Title: title}
	return r.conversation, nil
}

func (r *availabilityV2TestRepo) GetConversation(_ context.Context, ownerUserID, id int64) (*AvailabilityConversation, error) {
	if r.conversation == nil || r.conversation.ID != id || r.conversationOwner != ownerUserID {
		return nil, ErrAvailabilityConversationNotFound
	}
	copy := *r.conversation
	return &copy, nil
}

func (r *availabilityV2TestRepo) ListConversations(context.Context, int64, int, int, string) ([]AvailabilityConversation, int64, error) {
	if r.conversation == nil {
		return []AvailabilityConversation{}, 0, nil
	}
	return []AvailabilityConversation{*r.conversation}, 1, nil
}

func (r *availabilityV2TestRepo) CreateTurn(_ context.Context, ownerUserID, conversationID int64, prompt, _ string) (*AvailabilityTurn, bool, error) {
	if r.turn != nil {
		copy := cloneAvailabilityTurn(r.turn)
		return copy, false, nil
	}
	r.turn = &AvailabilityTurn{ID: 9, ConversationID: conversationID, Prompt: prompt, Status: AvailabilityTurnRunning, CreatedAt: time.Now().UTC(), Attempts: []AvailabilityAttempt{}}
	if r.conversationOwner == 0 {
		r.conversationOwner = ownerUserID
	}
	return cloneAvailabilityTurn(r.turn), true, nil
}

func (r *availabilityV2TestRepo) GetTurn(context.Context, int64, int64, int64) (*AvailabilityTurn, error) {
	if r.turn == nil {
		return nil, ErrAvailabilityTurnNotFound
	}
	return cloneAvailabilityTurn(r.turn), nil
}

func (r *availabilityV2TestRepo) GetTurnByClientRequestID(context.Context, int64, int64, string) (*AvailabilityTurn, error) {
	if r.turn == nil {
		return nil, ErrAvailabilityTurnNotFound
	}
	return cloneAvailabilityTurn(r.turn), nil
}

func (r *availabilityV2TestRepo) ListTurns(context.Context, int64, int64, int, int) ([]AvailabilityTurn, int64, error) {
	if r.turn == nil {
		return []AvailabilityTurn{}, 0, nil
	}
	return []AvailabilityTurn{*cloneAvailabilityTurn(r.turn)}, 1, nil
}

func (r *availabilityV2TestRepo) ListSuccessfulTurns(context.Context, int64, int64, int64) ([]AvailabilityTurn, error) {
	return []AvailabilityTurn{}, nil
}

func (r *availabilityV2TestRepo) FinalizeTurn(_ context.Context, _ int64, _ int64, _ int64, update AvailabilityV2TurnUpdate) error {
	if r.turn == nil {
		return ErrAvailabilityTurnNotFound
	}
	r.turn.Status = update.Status
	r.turn.Content = update.Content
	r.turn.Error = update.Error
	r.turn.CompletedAt = update.CompletedAt
	r.turn.FirstResponseMs = update.FirstResponseMs
	r.turn.TotalMs = update.TotalMs
	return nil
}

func (r *availabilityV2TestRepo) UpdateTurnContent(_ context.Context, _ int64, _ int64, _ int64, content string, firstResponseMs *int64) error {
	if r.turn == nil {
		return ErrAvailabilityTurnNotFound
	}
	r.turn.Content = content
	if r.turn.FirstResponseMs == nil {
		r.turn.FirstResponseMs = firstResponseMs
	}
	return nil
}

func (r *availabilityV2TestRepo) MarkTurnCancelled(_ context.Context, _ int64, _ int64, _ int64, message string) (bool, error) {
	if r.turn == nil {
		return false, ErrAvailabilityTurnNotFound
	}
	if r.turn.Status != AvailabilityTurnRunning {
		return false, nil
	}
	r.turn.Status = AvailabilityTurnCancelled
	r.turn.Error = message
	now := time.Now().UTC()
	r.turn.CompletedAt = &now
	return true, nil
}

func (r *availabilityV2TestRepo) MarkRunningAttemptsCancelled(context.Context, int64, int64, int64, string) error {
	return nil
}

func (r *availabilityV2TestRepo) CreateAttempt(_ context.Context, _ int64, _ int64, _ int64, attempt *AvailabilityAttempt) (*AvailabilityAttempt, error) {
	if attempt == nil {
		return nil, errors.New("nil attempt")
	}
	r.nextAttemptID++
	copy := *cloneAvailabilityAttempt(attempt)
	copy.ID = r.nextAttemptID
	r.attempts = append(r.attempts, &copy)
	return cloneAvailabilityAttempt(&copy), nil
}

func (r *availabilityV2TestRepo) UpdateAttempt(context.Context, int64, int64, int64, *AvailabilityAttempt) error {
	return nil
}

func (r *availabilityV2TestRepo) AppendEvent(_ context.Context, _ int64, event AvailabilityEvent) error {
	r.events = append(r.events, event)
	return nil
}

func (r *availabilityV2TestRepo) ListEvents(context.Context, int64, int64, int64) ([]AvailabilityEvent, error) {
	return append([]AvailabilityEvent(nil), r.events...), nil
}

func (r *availabilityV2TestRepo) UpdateConversationAt(context.Context, int64, int64, time.Time) error {
	return nil
}

func (r *availabilityV2TestRepo) ListRecentTests(context.Context, int64, int64, []int64) ([]AvailabilityLastTest, error) {
	return []AvailabilityLastTest{}, nil
}

func TestAvailabilityAccountPrioritiesStayDistinct(t *testing.T) {
	account := &Account{
		ID:       7,
		Priority: 40,
		AccountGroups: []AccountGroup{
			{GroupID: 12, Priority: 1},
		},
	}

	if got := availabilityGroupPriority(account, 12); got != 1 {
		t.Fatalf("group priority = %d, want 1", got)
	}
	if account.Priority != 40 {
		t.Fatalf("account priority = %d, want 40", account.Priority)
	}
}

func TestSortAvailabilityAccountRowsMatchesSchedulerPriorityLoadAndLRU(t *testing.T) {
	load0, load20 := 0, 20
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	rows := []AvailabilityAccount{
		{ID: 1, Priority: 10, GroupPriority: 2, LoadRate: &load0},
		{ID: 2, Priority: 50, GroupPriority: 1, LoadRate: &load0},
		{ID: 3, Priority: 20, GroupPriority: 1, LoadRate: &load20},
		{ID: 4, Priority: 20, GroupPriority: 1, LoadRate: &load0, LastUsedAt: &newer},
		{ID: 5, Priority: 20, GroupPriority: 1, LoadRate: &load0, LastUsedAt: &older},
		{ID: 6, Priority: 20, GroupPriority: 1, LoadRate: &load0},
	}

	sortAvailabilityAccountRows(rows)

	want := []int64{6, 5, 4, 3, 2, 1}
	for i, id := range want {
		if rows[i].ID != id {
			t.Fatalf("row[%d].ID = %d, want %d", i, rows[i].ID, id)
		}
	}
}

func TestAvailabilityAttemptTrackerPersistsOrderedEvents(t *testing.T) {
	repo := &availabilityV2TestRepo{}
	svc := &AvailabilityV2Service{repo: repo}
	run := &availabilityRun{
		conversationID: 12,
		turnID:         9,
		subscribers:    make(map[uint64]chan AvailabilityEvent),
		doneCh:         make(chan struct{}),
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{ID: 42, Name: "primary"}
	setAccountTestActiveAccount(c, account)
	tracker := &availabilityAttemptTracker{
		service:           svc,
		run:               run,
		ownerUserID:       7,
		conversationID:    12,
		turn:              &AvailabilityTurn{ID: 9, CreatedAt: time.Now().Add(-time.Second)},
		requestedModel:    "public-model",
		currentGinContext: c,
	}

	tracker.observe(TestEvent{Type: "test_start", Model: "upstream-model", Endpoint: "https://example.test/v1?api_key=secret"})
	tracker.observe(TestEvent{Type: "content", Text: "hello"})
	tracker.observe(TestEvent{Type: "error", Error: "upstream returned 502", StatusCode: 502})

	if len(repo.events) != 3 {
		t.Fatalf("persisted event count = %d, want 3", len(repo.events))
	}
	wantTypes := []string{AvailabilityEventAttemptStarted, AvailabilityEventContentDelta, AvailabilityEventAttemptFailed}
	for i, want := range wantTypes {
		if repo.events[i].Type != want || repo.events[i].Seq != int64(i+1) {
			t.Fatalf("event[%d] = (%s, seq %d), want (%s, seq %d)", i, repo.events[i].Type, repo.events[i].Seq, want, i+1)
		}
	}
	started := repo.events[0].Attempt
	if started == nil || started.AccountID != account.ID || started.Model != "upstream-model" {
		t.Fatalf("attempt_started = %#v, want concrete account and upstream model", started)
	}
	if started.Endpoint != "https://example.test/v1?api_key=%5Bredacted%5D" {
		t.Fatalf("attempt endpoint = %q, credentials were not redacted", started.Endpoint)
	}
	if repo.events[2].Attempt == nil || repo.events[2].Attempt.Status != AvailabilityAttemptFailed {
		t.Fatalf("attempt_failed payload = %#v", repo.events[2].Attempt)
	}
}

func TestAvailabilityAttemptTrackerResumesAttemptIndexAfterRestart(t *testing.T) {
	repo := &availabilityV2TestRepo{}
	svc := &AvailabilityV2Service{repo: repo}
	run := &availabilityRun{
		conversationID: 12,
		turnID:         9,
		subscribers:    make(map[uint64]chan AvailabilityEvent),
		doneCh:         make(chan struct{}),
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setAccountTestActiveAccount(c, &Account{ID: 42, Name: "recovered"})
	tracker := &availabilityAttemptTracker{
		service:           svc,
		run:               run,
		ownerUserID:       7,
		conversationID:    12,
		turn:              &AvailabilityTurn{ID: 9, CreatedAt: time.Now().Add(-time.Second)},
		requestedModel:    "public-model",
		currentGinContext: c,
	}
	tracker.seedExistingAttempts([]AvailabilityAttempt{{
		ID:          3,
		Index:       1,
		AccountID:   41,
		Status:      AvailabilityAttemptCancelled,
		StartedAt:   time.Now().Add(-time.Second),
		AccountName: "stale",
	}})

	tracker.observe(TestEvent{Type: "test_start", Model: "upstream-model", Endpoint: "https://example.test/v1"})

	if tracker.current == nil || tracker.current.Index != 2 {
		t.Fatalf("recovered attempt index = %#v, want 2", tracker.current)
	}
}

func TestAvailabilityCancelTurnIsDurableAndIdempotent(t *testing.T) {
	repo := &availabilityV2TestRepo{
		conversationOwner: 7,
		turn: &AvailabilityTurn{
			ID:             9,
			ConversationID: 12,
			Status:         AvailabilityTurnRunning,
			CreatedAt:      time.Now().Add(-time.Second),
			Attempts:       []AvailabilityAttempt{},
		},
	}
	svc := &AvailabilityV2Service{repo: repo, runs: make(map[string]*availabilityRun)}
	turn, err := svc.CancelTurn(context.Background(), 7, 12, 9)
	if err != nil {
		t.Fatalf("CancelTurn() error = %v", err)
	}
	if turn == nil || turn.Status != AvailabilityTurnCancelled {
		t.Fatalf("cancelled turn = %#v", turn)
	}
	if len(repo.events) != 1 || repo.events[0].Type != AvailabilityEventTurnCancelled || repo.events[0].Seq != 1 {
		t.Fatalf("cancel event = %#v", repo.events)
	}
	turn, err = svc.CancelTurn(context.Background(), 7, 12, 9)
	if err != nil || turn.Status != AvailabilityTurnCancelled {
		t.Fatalf("second CancelTurn() = (%#v, %v), want unchanged cancelled turn", turn, err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("second cancellation appended duplicate terminal event: %#v", repo.events)
	}
}

func TestAvailabilityConversationReadsAreOwnerScoped(t *testing.T) {
	repo := &availabilityV2TestRepo{
		conversationOwner: 7,
		conversation:      &AvailabilityConversation{ID: 1, GroupID: 2, Model: "model", Title: "test"},
	}
	svc := &AvailabilityV2Service{repo: repo}
	if _, err := svc.GetConversation(context.Background(), 8, 1, 1, 20); err == nil {
		t.Fatal("GetConversation() unexpectedly returned another administrator's conversation")
	}
}

func TestBuildAvailabilityPromptUsesSuccessfulHistoryShape(t *testing.T) {
	history := []AvailabilityTurn{{Prompt: "first", Content: "answer"}}
	got := buildAvailabilityPrompt(history, "follow up")
	if !containsAllAvailabilityText(got, "first", "answer", "follow up", "only successful turns") {
		t.Fatalf("prompt = %q, missing expected history/current markers", got)
	}
}

func containsAllAvailabilityText(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
