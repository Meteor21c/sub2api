package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
)

const (
	AvailabilitySupportDeclared    = "declared"
	AvailabilitySupportUnknown     = "unknown"
	AvailabilitySupportUnsupported = "unsupported"

	AvailabilityTurnRunning   = "running"
	AvailabilityTurnSucceeded = "succeeded"
	AvailabilityTurnFailed    = "failed"
	AvailabilityTurnCancelled = "cancelled"

	AvailabilityAttemptRunning   = "running"
	AvailabilityAttemptSucceeded = "succeeded"
	AvailabilityAttemptFailed    = "failed"
	AvailabilityAttemptCancelled = "cancelled"

	AvailabilityEventTurnStarted    = "turn_started"
	AvailabilityEventRouting        = "routing"
	AvailabilityEventAttemptStarted = "attempt_started"
	AvailabilityEventAttemptFailed  = "attempt_failed"
	AvailabilityEventContentDelta   = "content_delta"
	AvailabilityEventTurnCompleted  = "turn_completed"
	AvailabilityEventTurnFailed     = "turn_failed"
	AvailabilityEventTurnCancelled  = "turn_cancelled"

	availabilityRunTimeout         = 15 * time.Minute
	availabilityPersistenceTimeout = 15 * time.Second
	availabilityCancelWait         = 5 * time.Second
	availabilityMaxSearchLength    = 100
	availabilityMaxClientRequestID = 200
	availabilityMaxPromptLength    = 256 * 1024
	availabilityMaxTitleLength     = 200
	availabilityMaxContextLength   = 512 * 1024
)

var (
	ErrAvailabilityConversationNotFound  = infraerrors.NotFound("AVAILABILITY_CONVERSATION_NOT_FOUND", "availability conversation not found")
	ErrAvailabilityTurnNotFound          = infraerrors.NotFound("AVAILABILITY_TURN_NOT_FOUND", "availability turn not found")
	ErrAvailabilityGroupRequired         = infraerrors.BadRequest("AVAILABILITY_GROUP_REQUIRED", "group_id is required")
	ErrAvailabilityModelRequired         = infraerrors.BadRequest("AVAILABILITY_MODEL_REQUIRED", "model is required")
	ErrAvailabilityPromptRequired        = infraerrors.BadRequest("AVAILABILITY_PROMPT_REQUIRED", "prompt is required")
	ErrAvailabilityClientRequestRequired = infraerrors.BadRequest("AVAILABILITY_CLIENT_REQUEST_REQUIRED", "client_request_id is required")
	ErrAvailabilityAccountNotInGroup     = infraerrors.BadRequest("AVAILABILITY_ACCOUNT_NOT_IN_GROUP", "account does not belong to the selected group")
	ErrAvailabilityAccountModelMismatch  = infraerrors.BadRequest("AVAILABILITY_ACCOUNT_MODEL_MISMATCH", "selected account cannot serve the selected composite model route")
	ErrAvailabilityModelNotAllowed       = infraerrors.BadRequest("AVAILABILITY_MODEL_NOT_ALLOWED", "model is not allowed for the selected group")
	ErrAvailabilityTurnConflict          = infraerrors.Conflict("AVAILABILITY_TURN_CONFLICT", "client_request_id is already used with different prompt")
	ErrAvailabilityTurnRunning           = infraerrors.Conflict("AVAILABILITY_TURN_RUNNING", "turn is already running")
)

// AvailabilityConversation is the durable administrator-scoped conversation
// header.  The group/account IDs are snapshots of the operator's selection;
// history remains readable even if a group or account is later removed.
type AvailabilityConversation struct {
	ID        int64     `json:"id"`
	GroupID   int64     `json:"group_id"`
	AccountID *int64    `json:"account_id"`
	Model     string    `json:"model"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AvailabilityAttempt struct {
	ID              int64      `json:"id"`
	Index           int        `json:"index"`
	AccountID       int64      `json:"account_id"`
	AccountName     string     `json:"account_name"`
	RequestedModel  string     `json:"requested_model"`
	Model           string     `json:"model"`
	Endpoint        string     `json:"endpoint"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	FirstResponseMs *int64     `json:"first_response_ms"`
	TotalMs         *int64     `json:"total_ms"`
	StatusCode      int        `json:"status_code,omitempty"`
	Error           string     `json:"error,omitempty"`
	Reason          string     `json:"reason,omitempty"`
}

type AvailabilityTurn struct {
	ID              int64                 `json:"id"`
	ConversationID  int64                 `json:"conversation_id"`
	Prompt          string                `json:"prompt"`
	Content         string                `json:"content"`
	Status          string                `json:"status"`
	CreatedAt       time.Time             `json:"created_at"`
	CompletedAt     *time.Time            `json:"completed_at"`
	FirstResponseMs *int64                `json:"first_response_ms"`
	TotalMs         *int64                `json:"total_ms"`
	Attempts        []AvailabilityAttempt `json:"attempts"`
	Error           string                `json:"error,omitempty"`
}

type AvailabilityEvent struct {
	Type           string               `json:"type"`
	ConversationID int64                `json:"conversation_id"`
	TurnID         int64                `json:"turn_id"`
	Seq            int64                `json:"seq"`
	At             time.Time            `json:"at"`
	Attempt        *AvailabilityAttempt `json:"attempt,omitempty"`
	Delta          string               `json:"delta,omitempty"`
	Turn           *AvailabilityTurn    `json:"turn,omitempty"`
	Error          string               `json:"error,omitempty"`
}

type AvailabilityAccount struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Platform           string `json:"platform"`
	Status             string `json:"status"`
	Priority           int    `json:"priority"`
	GroupPriority      int    `json:"group_priority"`
	LoadFactor         *int   `json:"load_factor"`
	Concurrency        int    `json:"concurrency"`
	Eligible           bool   `json:"eligible"`
	Reason             string `json:"reason,omitempty"`
	Rank               int    `json:"rank"`
	CurrentConcurrency *int   `json:"current_concurrency,omitempty"`
	QueueDepth         *int   `json:"queue_depth,omitempty"`
}

type AvailabilityLastTest struct {
	At              string `json:"at"`
	AccountID       int64  `json:"account_id"`
	AccountName     string `json:"account_name"`
	Model           string `json:"model"`
	Success         bool   `json:"success"`
	FirstResponseMs *int64 `json:"first_response_ms"`
	TotalMs         *int64 `json:"total_ms"`
}

type AvailabilityModel struct {
	ID                string                `json:"id"`
	UpstreamModel     string                `json:"upstream_model,omitempty"`
	UpstreamSupport   string                `json:"upstream_support"`
	DownstreamAllowed *bool                 `json:"downstream_allowed"`
	Source            string                `json:"source"`
	AccountIDs        []int64               `json:"account_ids"`
	MappingEffective  bool                  `json:"mapping_effective"`
	LastTest          *AvailabilityLastTest `json:"last_test,omitempty"`
}

type AvailabilityCatalog struct {
	Accounts       []AvailabilityAccount `json:"accounts"`
	Models         []AvailabilityModel   `json:"models"`
	SchedulingNote string                `json:"scheduling_note"`
}

type AvailabilityConversationPage struct {
	Items []AvailabilityConversation `json:"items"`
	Total int64                      `json:"total"`
}

type AvailabilityConversationDetail struct {
	Conversation AvailabilityConversation `json:"conversation"`
	Turns        []AvailabilityTurn       `json:"turns"`
	Total        int64                    `json:"total,omitempty"`
	Page         int                      `json:"page,omitempty"`
	PageSize     int                      `json:"page_size,omitempty"`
}

type AvailabilityCreateConversationInput struct {
	OwnerUserID int64
	GroupID     int64
	AccountID   *int64
	Model       string
	Title       string
}

type AvailabilityStreamTurnInput struct {
	Prompt          string
	ClientRequestID string
}

type AvailabilityV2TurnUpdate struct {
	Status          string
	Content         string
	Error           string
	CompletedAt     *time.Time
	FirstResponseMs *int64
	TotalMs         *int64
}

// AvailabilityV2Repository is deliberately narrower than the existing Ent
// repositories.  V2 records are append-only event data and are implemented by
// the raw SQL repository so adding the feature does not regenerate Ent code.
type AvailabilityV2Repository interface {
	CreateConversation(ctx context.Context, ownerUserID, groupID int64, accountID *int64, model, title string) (*AvailabilityConversation, error)
	GetConversation(ctx context.Context, ownerUserID, id int64) (*AvailabilityConversation, error)
	ListConversations(ctx context.Context, ownerUserID int64, page, pageSize int, search string) ([]AvailabilityConversation, int64, error)

	CreateTurn(ctx context.Context, ownerUserID, conversationID int64, prompt, clientRequestID string) (*AvailabilityTurn, bool, error)
	GetTurn(ctx context.Context, ownerUserID, conversationID, turnID int64) (*AvailabilityTurn, error)
	GetTurnByClientRequestID(ctx context.Context, ownerUserID, conversationID int64, clientRequestID string) (*AvailabilityTurn, error)
	ListTurns(ctx context.Context, ownerUserID, conversationID int64, page, pageSize int) ([]AvailabilityTurn, int64, error)
	ListSuccessfulTurns(ctx context.Context, ownerUserID, conversationID, beforeTurnID int64) ([]AvailabilityTurn, error)
	FinalizeTurn(ctx context.Context, ownerUserID, conversationID, turnID int64, update AvailabilityV2TurnUpdate) error
	UpdateTurnContent(ctx context.Context, ownerUserID, conversationID, turnID int64, content string, firstResponseMs *int64) error
	MarkTurnCancelled(ctx context.Context, ownerUserID, conversationID, turnID int64, message string) (bool, error)
	MarkRunningAttemptsCancelled(ctx context.Context, ownerUserID, conversationID, turnID int64, message string) error

	CreateAttempt(ctx context.Context, ownerUserID, conversationID, turnID int64, attempt *AvailabilityAttempt) (*AvailabilityAttempt, error)
	UpdateAttempt(ctx context.Context, ownerUserID, conversationID, turnID int64, attempt *AvailabilityAttempt) error

	AppendEvent(ctx context.Context, ownerUserID int64, event AvailabilityEvent) error
	ListEvents(ctx context.Context, ownerUserID, conversationID, turnID int64) ([]AvailabilityEvent, error)
	UpdateConversationAt(ctx context.Context, ownerUserID, conversationID int64, at time.Time) error
	ListRecentTests(ctx context.Context, ownerUserID, groupID int64, accountIDs []int64) ([]AvailabilityLastTest, error)
}

// AvailabilityV2Service owns the persistent turn lifecycle and the in-process
// fan-out used by duplicate/reconnected POST streams.  The database remains
// the source of truth; the fan-out only prevents duplicate upstream requests
// while one process is serving the same client_request_id.
type AvailabilityV2Service struct {
	repo        AvailabilityV2Repository
	accountRepo AccountRepository
	groupRepo   GroupRepository
	concurrency *ConcurrencyService
	accountTest *AccountTestService

	runsMu sync.Mutex
	runs   map[string]*availabilityRun
}

func NewAvailabilityV2Service(
	repo AvailabilityV2Repository,
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	concurrency *ConcurrencyService,
	accountTest *AccountTestService,
) *AvailabilityV2Service {
	return &AvailabilityV2Service{
		repo:        repo,
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		concurrency: concurrency,
		accountTest: accountTest,
		runs:        make(map[string]*availabilityRun),
	}
}

func ProvideAvailabilityV2Service(
	repo AvailabilityV2Repository,
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	concurrency *ConcurrencyService,
	accountTest *AccountTestService,
) *AvailabilityV2Service {
	return NewAvailabilityV2Service(repo, accountRepo, groupRepo, concurrency, accountTest)
}

func (s *AvailabilityV2Service) CreateConversation(ctx context.Context, input AvailabilityCreateConversationInput) (*AvailabilityConversation, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("availability repository is unavailable")
	}
	if input.OwnerUserID <= 0 {
		return nil, infraerrors.Unauthorized("AVAILABILITY_OWNER_REQUIRED", "authenticated administrator is required")
	}
	if input.GroupID <= 0 {
		return nil, ErrAvailabilityGroupRequired
	}
	if s.groupRepo == nil {
		return nil, errors.New("group repository is unavailable")
	}
	group, err := s.groupRepo.GetByID(ctx, input.GroupID)
	if err != nil || group == nil {
		if err != nil {
			return nil, err
		}
		return nil, ErrGroupNotFound
	}

	var account *Account
	if input.AccountID != nil {
		if *input.AccountID <= 0 {
			return nil, ErrAvailabilityAccountNotInGroup
		}
		if s.accountRepo == nil {
			return nil, errors.New("account repository is unavailable")
		}
		account, err = s.accountRepo.GetByID(ctx, *input.AccountID)
		if err != nil {
			return nil, err
		}
		if !accountBelongsToAvailabilityGroup(account, input.GroupID) {
			return nil, ErrAvailabilityAccountNotInGroup
		}
	}

	model := strings.TrimSpace(input.Model)
	if model == "" {
		if account != nil {
			model = defaultGroupDebugModel(account.Platform)
		} else {
			model = defaultGroupDebugModel(group.Platform)
		}
	}
	if model == "" || (group.Platform == PlatformComposite && strings.TrimSpace(input.Model) == "") {
		return nil, ErrAvailabilityModelRequired
	}
	if !group.ModelAllowlist.Allows(model) {
		return nil, ErrAvailabilityModelNotAllowed
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "Availability test"
	}
	if len(title) > availabilityMaxTitleLength {
		title = title[:availabilityMaxTitleLength]
	}
	return s.repo.CreateConversation(ctx, input.OwnerUserID, input.GroupID, input.AccountID, model, title)
}

func (s *AvailabilityV2Service) ListConversations(ctx context.Context, ownerUserID int64, page, pageSize int, search string) (*AvailabilityConversationPage, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("availability repository is unavailable")
	}
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("AVAILABILITY_OWNER_REQUIRED", "authenticated administrator is required")
	}
	page, pageSize = normalizeAvailabilityPage(page, pageSize, 100)
	search = strings.TrimSpace(search)
	if len(search) > availabilityMaxSearchLength {
		search = search[:availabilityMaxSearchLength]
	}
	items, total, err := s.repo.ListConversations(ctx, ownerUserID, page, pageSize, search)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []AvailabilityConversation{}
	}
	return &AvailabilityConversationPage{Items: items, Total: total}, nil
}

func (s *AvailabilityV2Service) GetConversation(ctx context.Context, ownerUserID, conversationID int64, page, pageSize int) (*AvailabilityConversationDetail, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("availability repository is unavailable")
	}
	if ownerUserID <= 0 || conversationID <= 0 {
		if ownerUserID <= 0 {
			return nil, infraerrors.Unauthorized("AVAILABILITY_OWNER_REQUIRED", "authenticated administrator is required")
		}
		return nil, ErrAvailabilityConversationNotFound
	}
	conversation, err := s.repo.GetConversation(ctx, ownerUserID, conversationID)
	if err != nil {
		return nil, err
	}
	page, pageSize = normalizeAvailabilityPage(page, pageSize, 100)
	turns, total, err := s.repo.ListTurns(ctx, ownerUserID, conversationID, page, pageSize)
	if err != nil {
		return nil, err
	}
	if turns == nil {
		turns = []AvailabilityTurn{}
	}
	return &AvailabilityConversationDetail{
		Conversation: *conversation,
		Turns:        turns,
		Total:        total,
		Page:         page,
		PageSize:     pageSize,
	}, nil
}

func (s *AvailabilityV2Service) Catalog(ctx context.Context, ownerUserID, groupID int64, accountID *int64) (*AvailabilityCatalog, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("availability repository is unavailable")
	}
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("AVAILABILITY_OWNER_REQUIRED", "authenticated administrator is required")
	}
	if groupID <= 0 {
		return nil, ErrAvailabilityGroupRequired
	}
	if s.groupRepo == nil || s.accountRepo == nil {
		return nil, errors.New("availability catalog dependencies are unavailable")
	}
	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, ErrGroupNotFound
	}

	accounts := make([]Account, 0)
	if accountID != nil {
		if *accountID <= 0 {
			return nil, ErrAvailabilityAccountNotInGroup
		}
		account, getErr := s.accountRepo.GetByID(ctx, *accountID)
		if getErr != nil {
			return nil, getErr
		}
		if !accountBelongsToAvailabilityGroup(account, groupID) {
			return nil, ErrAvailabilityAccountNotInGroup
		}
		accounts = append(accounts, *account)
	} else {
		accounts, err = s.accountRepo.ListByGroup(ctx, groupID)
		if err != nil {
			return nil, err
		}
	}

	accountIDs := make([]int64, 0, len(accounts))
	for i := range accounts {
		if accounts[i].ID > 0 {
			accountIDs = append(accountIDs, accounts[i].ID)
		}
	}
	recent, err := s.repo.ListRecentTests(ctx, ownerUserID, groupID, accountIDs)
	if err != nil {
		return nil, err
	}
	recentByAccountModel := make(map[string]AvailabilityLastTest, len(recent))
	for _, item := range recent {
		recentByAccountModel[availabilityModelKey(item.AccountID, item.Model)] = item
	}

	loadMap := map[int64]*AccountLoadInfo{}
	if s.concurrency != nil && len(accounts) > 0 {
		loads := make([]AccountWithConcurrency, 0, len(accounts))
		for i := range accounts {
			loads = append(loads, AccountWithConcurrency{ID: accounts[i].ID, MaxConcurrency: accounts[i].EffectiveLoadFactor()})
		}
		if loaded, loadErr := s.concurrency.GetAccountsLoadBatch(ctx, loads); loadErr == nil && loaded != nil {
			loadMap = loaded
		}
	}

	accountRows := make([]AvailabilityAccount, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		load := loadMap[account.ID]
		row := AvailabilityAccount{
			ID:            account.ID,
			Name:          account.Name,
			Platform:      account.Platform,
			Status:        account.Status,
			Priority:      account.Priority,
			GroupPriority: availabilityGroupPriority(account, groupID),
			LoadFactor:    account.LoadFactor,
			Concurrency:   account.Concurrency,
			Eligible:      account.IsSchedulable(),
		}
		if load != nil {
			current := load.CurrentConcurrency
			queue := load.WaitingCount
			row.CurrentConcurrency = &current
			row.QueueDepth = &queue
			if load.LoadRate >= 100 {
				row.Eligible = false
				row.Reason = "concurrency capacity is full"
			}
		}
		if row.Reason == "" && !row.Eligible {
			row.Reason = availabilityAccountIneligibleReason(account)
		}
		accountRows = append(accountRows, row)
	}
	sortAvailabilityAccountRows(accountRows)
	for i := range accountRows {
		accountRows[i].Rank = i + 1
	}

	models := buildAvailabilityModels(group, accounts, recentByAccountModel)
	return &AvailabilityCatalog{
		Accounts:       accountRows,
		Models:         models,
		SchedulingNote: "Rank is the current scheduler order by group priority, account priority, live concurrency, and account ID. It is an order, not a probability; runtime rate limits, model capability, and other scheduler constraints still apply.",
	}, nil
}

func normalizeAvailabilityPage(page, pageSize, maxPageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

func accountBelongsToAvailabilityGroup(account *Account, groupID int64) bool {
	if account == nil || groupID <= 0 {
		return false
	}
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID {
			return true
		}
	}
	return false
}

func availabilityGroupPriority(account *Account, groupID int64) int {
	if account == nil {
		return 0
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID {
			return binding.Priority
		}
	}
	return account.Priority
}

func sortAvailabilityAccountRows(accounts []AvailabilityAccount) {
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].GroupPriority != accounts[j].GroupPriority {
			return accounts[i].GroupPriority < accounts[j].GroupPriority
		}
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		leftLoad, rightLoad := 0, 0
		if accounts[i].CurrentConcurrency != nil {
			leftLoad = *accounts[i].CurrentConcurrency
		}
		if accounts[j].CurrentConcurrency != nil {
			rightLoad = *accounts[j].CurrentConcurrency
		}
		if leftLoad != rightLoad {
			return leftLoad < rightLoad
		}
		return accounts[i].ID < accounts[j].ID
	})
}

func availabilityAccountIneligibleReason(account *Account) string {
	if account == nil {
		return "account is unavailable"
	}
	if !account.IsActive() {
		return "account is not active"
	}
	if !account.Schedulable {
		return "account is not schedulable"
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !time.Now().Before(*account.ExpiresAt) {
		return "account has expired"
	}
	if account.TempUnschedulableUntil != nil && time.Now().Before(*account.TempUnschedulableUntil) {
		return "account is temporarily unschedulable"
	}
	if account.RateLimitResetAt != nil && time.Now().Before(*account.RateLimitResetAt) {
		return "account is rate limited"
	}
	if account.OverloadUntil != nil && time.Now().Before(*account.OverloadUntil) {
		return "account is overloaded"
	}
	return "account is not eligible under current scheduler constraints"
}

func availabilityModelKey(accountID int64, model string) string {
	return fmt.Sprintf("%d\x00%s", accountID, strings.ToLower(strings.TrimSpace(model)))
}

func buildAvailabilityModels(group *Group, accounts []Account, recent map[string]AvailabilityLastTest) []AvailabilityModel {
	if group == nil {
		return []AvailabilityModel{}
	}
	type modelAccum struct {
		model        AvailabilityModel
		seenAccounts map[int64]struct{}
		seenSources  map[string]struct{}
	}
	accum := make(map[string]*modelAccum)
	order := make([]string, 0)
	add := func(publicModel, upstreamModel, source string, support string, account *Account, mappingEffective bool) {
		publicModel = strings.TrimSpace(publicModel)
		if publicModel == "" || strings.Contains(publicModel, "*") {
			return
		}
		key := strings.ToLower(publicModel)
		entry, ok := accum[key]
		if !ok {
			allowed := group.ModelAllowlist.Allows(publicModel)
			entry = &modelAccum{
				model: AvailabilityModel{
					ID:                publicModel,
					UpstreamModel:     strings.TrimSpace(upstreamModel),
					UpstreamSupport:   support,
					DownstreamAllowed: boolPointer(allowed),
					Source:            source,
					AccountIDs:        []int64{},
					MappingEffective:  mappingEffective,
				},
				seenAccounts: make(map[int64]struct{}),
				seenSources:  make(map[string]struct{}),
			}
			accum[key] = entry
			order = append(order, key)
		}
		if entry.model.UpstreamModel == "" && strings.TrimSpace(upstreamModel) != "" {
			entry.model.UpstreamModel = strings.TrimSpace(upstreamModel)
		}
		if entry.model.UpstreamSupport == AvailabilitySupportUnknown && support == AvailabilitySupportDeclared {
			entry.model.UpstreamSupport = support
		}
		if entry.model.UpstreamSupport == AvailabilitySupportUnsupported && support != AvailabilitySupportUnsupported {
			entry.model.UpstreamSupport = support
		}
		entry.model.MappingEffective = entry.model.MappingEffective || mappingEffective
		if source != "" {
			entry.seenSources[source] = struct{}{}
			entry.model.Source = joinAvailabilitySources(entry.seenSources)
		}
		if account != nil && account.ID > 0 {
			if _, seen := entry.seenAccounts[account.ID]; !seen {
				entry.seenAccounts[account.ID] = struct{}{}
				entry.model.AccountIDs = append(entry.model.AccountIDs, account.ID)
			}
			if last, ok := recent[availabilityModelKey(account.ID, publicModel)]; ok && availabilityLastTestIsNewer(&last, entry.model.LastTest) {
				copy := last
				entry.model.LastTest = &copy
			}
			if strings.TrimSpace(upstreamModel) != "" {
				if last, ok := recent[availabilityModelKey(account.ID, upstreamModel)]; ok && availabilityLastTestIsNewer(&last, entry.model.LastTest) {
					copy := last
					entry.model.LastTest = &copy
				}
			}
		}
	}

	for i := range accounts {
		account := &accounts[i]
		mapping := account.GetModelMapping()
		snapshot := account.GetUpstreamModelMetadataSnapshot()
		knownUpstream := make(map[string]struct{})
		if snapshot != nil {
			for id := range snapshot.Models {
				id = strings.TrimSpace(id)
				if id != "" {
					knownUpstream[id] = struct{}{}
				}
			}
		}
		addUpstream := func(public, upstream, source string) {
			support := AvailabilitySupportUnknown
			if snapshot != nil {
				if _, ok := knownUpstream[strings.TrimSpace(upstream)]; ok {
					support = AvailabilitySupportDeclared
				} else {
					support = AvailabilitySupportUnsupported
				}
			}
			add(public, upstream, source, support, account, !account.IsOpenAIPassthroughEnabled())
		}

		if snapshot != nil {
			for upstream := range knownUpstream {
				public := upstream
				for requested, mapped := range mapping {
					if strings.EqualFold(strings.TrimSpace(mapped), upstream) {
						public = requested
						addUpstream(public, upstream, "upstream_snapshot")
					}
				}
				addUpstream(public, upstream, "upstream_snapshot")
			}
		}
		if len(mapping) > 0 {
			for requested, mapped := range mapping {
				if strings.Contains(requested, "*") {
					continue
				}
				addUpstream(requested, mapped, "account_mapping")
			}
		} else {
			for _, model := range availabilityDefaultModels(account) {
				addUpstream(model, model, "platform_default")
			}
		}
	}

	// Keep group allowlist entries visible even when no current account has
	// advertised them. Their account_ids remain empty instead of fabricating
	// capability, and downstream_allowed explains the policy decision.
	if group.ModelAllowlist.Enabled {
		for _, model := range group.ModelAllowlist.Models {
			if strings.Contains(model, "*") {
				continue
			}
			add(model, model, "group_allowlist", AvailabilitySupportUnknown, nil, true)
		}
	}

	models := make([]AvailabilityModel, 0, len(order))
	for _, key := range order {
		entry := accum[key]
		if entry == nil {
			continue
		}
		sort.Slice(entry.model.AccountIDs, func(i, j int) bool { return entry.model.AccountIDs[i] < entry.model.AccountIDs[j] })
		models = append(models, entry.model)
	}
	sort.SliceStable(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	return models
}

func boolPointer(value bool) *bool { return &value }

func availabilityLastTestIsNewer(candidate *AvailabilityLastTest, current *AvailabilityLastTest) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	candidateAt, candidateErr := time.Parse(time.RFC3339, candidate.At)
	currentAt, currentErr := time.Parse(time.RFC3339, current.At)
	if candidateErr != nil || currentErr != nil {
		return false
	}
	return candidateAt.After(currentAt)
}

func joinAvailabilitySources(sources map[string]struct{}) string {
	values := make([]string, 0, len(sources))
	for source := range sources {
		values = append(values, source)
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}

func availabilityDefaultModels(account *Account) []string {
	if account == nil {
		return nil
	}
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	switch platform {
	case PlatformOpenAI:
		return openai.DefaultModelIDs()
	case PlatformGemini:
		models := make([]string, 0, len(geminicli.DefaultModels))
		if account.IsGeminiGoogleOne() {
			for _, model := range geminicli.GoogleOneModels {
				models = append(models, model.ID)
			}
			return models
		}
		for _, model := range geminicli.DefaultModels {
			models = append(models, model.ID)
		}
		return models
	case PlatformGrok:
		return xai.DefaultModelIDs()
	case PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		// These providers do not share one stable built-in model catalog.  If
		// an account has neither a persisted upstream snapshot nor an explicit
		// mapping, leave the model list empty instead of fabricating Claude or
		// OpenAI support for a CN account.
		return nil
	default:
		return claude.DefaultModelIDs()
	}
}

type availabilityRun struct {
	key            string
	turnID         int64
	conversationID int64
	workCtx        context.Context
	cancel         context.CancelFunc

	mu          sync.Mutex
	events      []AvailabilityEvent
	nextSeq     int64
	subscribers map[uint64]chan AvailabilityEvent
	nextSubID   uint64
	done        bool
	doneCh      chan struct{}
}

func (r *availabilityRun) subscribe() ([]AvailabilityEvent, uint64, <-chan AvailabilityEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := append([]AvailabilityEvent(nil), r.events...)
	r.nextSubID++
	id := r.nextSubID
	ch := make(chan AvailabilityEvent, 256)
	if r.subscribers == nil {
		r.subscribers = make(map[uint64]chan AvailabilityEvent)
	}
	if !r.done {
		r.subscribers[id] = ch
	} else {
		close(ch)
	}
	return events, id, ch
}

func (r *availabilityRun) unsubscribe(id uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	ch, ok := r.subscribers[id]
	if ok {
		delete(r.subscribers, id)
		close(ch)
	}
	shouldCancel := !r.done && len(r.subscribers) == 0
	r.mu.Unlock()
	if shouldCancel && r.cancel != nil {
		r.cancel()
	}
}

func (r *availabilityRun) finish() {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.done = true
	if r.doneCh != nil {
		close(r.doneCh)
	}
	for id, ch := range r.subscribers {
		close(ch)
		delete(r.subscribers, id)
	}
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *availabilityRun) hasEventType(eventType string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func (r *availabilityRun) hasTerminalEvent() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if isAvailabilityTerminalEvent(event.Type) {
			return true
		}
	}
	return false
}

func availabilityRunKey(ownerUserID, conversationID, turnID int64) string {
	return fmt.Sprintf("%d:%d:%d", ownerUserID, conversationID, turnID)
}

func (s *AvailabilityV2Service) StreamTurn(ctx context.Context, ownerUserID, conversationID int64, input AvailabilityStreamTurnInput, sink func(AvailabilityEvent) error) error {
	if s == nil || s.repo == nil {
		return errors.New("availability repository is unavailable")
	}
	if ownerUserID <= 0 {
		return infraerrors.Unauthorized("AVAILABILITY_OWNER_REQUIRED", "authenticated administrator is required")
	}
	if conversationID <= 0 {
		return ErrAvailabilityConversationNotFound
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.Prompt == "" {
		return ErrAvailabilityPromptRequired
	}
	if len(input.Prompt) > availabilityMaxPromptLength {
		return infraerrors.BadRequest("AVAILABILITY_PROMPT_TOO_LARGE", "prompt is too large")
	}
	if input.ClientRequestID == "" {
		return ErrAvailabilityClientRequestRequired
	}
	if len(input.ClientRequestID) > availabilityMaxClientRequestID {
		return infraerrors.BadRequest("AVAILABILITY_CLIENT_REQUEST_ID_TOO_LARGE", "client_request_id is too large")
	}
	conversation, err := s.repo.GetConversation(ctx, ownerUserID, conversationID)
	if err != nil {
		return err
	}
	turn, created, err := s.repo.CreateTurn(ctx, ownerUserID, conversationID, input.Prompt, input.ClientRequestID)
	if err != nil {
		return err
	}
	if turn == nil {
		return ErrAvailabilityTurnNotFound
	}
	if !created && turn.Prompt != input.Prompt {
		return ErrAvailabilityTurnConflict
	}

	if turn.Status != AvailabilityTurnRunning {
		return s.replayTurn(ctx, ownerUserID, conversationID, turn.ID, sink)
	}
	key := availabilityRunKey(ownerUserID, conversationID, turn.ID)
	s.runsMu.Lock()
	run := s.runs[key]
	start := false
	if run == nil {
		workCtx, cancel := context.WithTimeout(context.Background(), availabilityRunTimeout)
		run = &availabilityRun{
			key:            key,
			turnID:         turn.ID,
			conversationID: conversationID,
			workCtx:        workCtx,
			cancel:         cancel,
			subscribers:    make(map[uint64]chan AvailabilityEvent),
			doneCh:         make(chan struct{}),
		}
		// A retry may attach after the first request has already persisted a
		// prefix. Seed the in-process replay buffer before starting work.
		persistCtx, persistCancel := availabilityPersistenceContext()
		existingEvents, eventErr := s.repo.ListEvents(persistCtx, ownerUserID, conversationID, turn.ID)
		persistCancel()
		if eventErr != nil {
			s.runsMu.Unlock()
			cancel()
			return eventErr
		}
		run.events = append(run.events, existingEvents...)
		for _, event := range existingEvents {
			if event.Seq > run.nextSeq {
				run.nextSeq = event.Seq
			}
		}
		s.runs[key] = run
		start = true
	}
	s.runsMu.Unlock()

	events, subID, eventCh := run.subscribe()
	if start {
		go s.runAvailabilityTurn(run, ownerUserID, conversation, turn)
	}
	for _, event := range events {
		if err := sink(event); err != nil {
			run.unsubscribe(subID)
			return err
		}
		if isAvailabilityTerminalEvent(event.Type) {
			run.unsubscribe(subID)
			return nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			run.unsubscribe(subID)
			return ctx.Err()
		case event, ok := <-eventCh:
			if !ok {
				return nil
			}
			if err := sink(event); err != nil {
				run.unsubscribe(subID)
				return err
			}
			if isAvailabilityTerminalEvent(event.Type) {
				run.unsubscribe(subID)
				return nil
			}
		}
	}
}

func (s *AvailabilityV2Service) replayTurn(ctx context.Context, ownerUserID, conversationID, turnID int64, sink func(AvailabilityEvent) error) error {
	events, err := s.repo.ListEvents(ctx, ownerUserID, conversationID, turnID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := sink(event); err != nil {
			return err
		}
	}
	return nil
}

func availabilityPersistenceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), availabilityPersistenceTimeout)
}

func (s *AvailabilityV2Service) emitAvailabilityEvent(run *availabilityRun, ownerUserID int64, event AvailabilityEvent) error {
	if run == nil {
		return errors.New("availability run is unavailable")
	}
	if event.ConversationID == 0 {
		event.ConversationID = run.conversationID
	}
	if event.TurnID == 0 {
		event.TurnID = run.turnID
	}
	event.At = time.Now().UTC()
	run.mu.Lock()
	run.nextSeq++
	event.Seq = run.nextSeq
	run.mu.Unlock()
	persistCtx, persistCancel := availabilityPersistenceContext()
	err := s.repo.AppendEvent(persistCtx, ownerUserID, event)
	persistCancel()
	if err != nil {
		return err
	}
	run.mu.Lock()
	run.events = append(run.events, event)
	for _, ch := range run.subscribers {
		select {
		case ch <- event:
		default:
			// The event is durable. A slow subscriber can reconnect and replay
			// the persisted sequence rather than blocking the upstream request.
		}
	}
	run.mu.Unlock()
	return nil
}

type availabilityAttemptTracker struct {
	service        *AvailabilityV2Service
	run            *availabilityRun
	ownerUserID    int64
	conversationID int64
	turn           *AvailabilityTurn
	requestedModel string

	attempts               []*AvailabilityAttempt
	initialAttemptCount    int
	current                *AvailabilityAttempt
	content                strings.Builder
	firstResponseAt        time.Time
	attemptFirstResponseAt time.Time
	persistErr             error
	emitErr                error
	currentGinContext      *gin.Context
}

func (t *availabilityAttemptTracker) seedExistingAttempts(attempts []AvailabilityAttempt) {
	if t == nil {
		return
	}
	for i := range attempts {
		attempt := cloneAvailabilityAttempt(&attempts[i])
		if attempt == nil {
			continue
		}
		if attempt.Status == AvailabilityAttemptRunning {
			now := time.Now().UTC()
			attempt.Status = AvailabilityAttemptCancelled
			attempt.CompletedAt = &now
			attempt.TotalMs = availabilityAttemptElapsedMs(attempt.StartedAt, now)
			attempt.Error = "availability worker restarted"
			attempt.Reason = "availability worker restarted"
		}
		t.attempts = append(t.attempts, attempt)
	}
	t.initialAttemptCount = len(t.attempts)
}

func availabilityAttemptElapsedMs(startedAt, completedAt time.Time) *int64 {
	if startedAt.IsZero() || completedAt.IsZero() {
		return nil
	}
	value := nonNegativeDurationMs(completedAt.Sub(startedAt))
	return &value
}

func (t *availabilityAttemptTracker) nextAttemptIndex() int {
	if t == nil {
		return 1
	}
	maxIndex := 0
	for _, attempt := range t.attempts {
		if attempt != nil && attempt.Index > maxIndex {
			maxIndex = attempt.Index
		}
	}
	return maxIndex + 1
}

func (t *availabilityAttemptTracker) observe(event TestEvent) {
	if t == nil || t.service == nil || t.run == nil {
		return
	}
	switch event.Type {
	case "test_start":
		t.observeStart(event)
	case "content":
		t.observeContent(event)
	case "error":
		t.observeFailure(event)
	case "test_complete":
		if event.Success {
			t.completeCurrent(AvailabilityAttemptSucceeded, "", 0)
		}
	}
}

func (t *availabilityAttemptTracker) observeStart(event TestEvent) {
	if t.current != nil && t.current.Status == AvailabilityAttemptRunning {
		// Adaptive probes may call multiple native endpoints for one account;
		// a new test_start means the previous endpoint returned successfully.
		t.completeCurrent(AvailabilityAttemptSucceeded, "", 0)
	}
	t.attemptFirstResponseAt = time.Time{}
	account := accountTestActiveAccountFromContext(t.currentGinContext)
	if account == nil {
		// The real probe did not expose a concrete account. Do not invent an
		// account ID merely to make the event shape look complete.
		return
	}
	now := time.Now().UTC()
	attempt := &AvailabilityAttempt{
		Index:          t.nextAttemptIndex(),
		AccountID:      account.ID,
		AccountName:    account.Name,
		RequestedModel: t.requestedModel,
		Model:          strings.TrimSpace(event.Model),
		Endpoint:       safeAccountTestEndpoint(event.Endpoint),
		Status:         AvailabilityAttemptRunning,
		StartedAt:      now,
	}
	persistCtx, persistCancel := availabilityPersistenceContext()
	persisted, err := t.service.repo.CreateAttempt(persistCtx, t.ownerUserID, t.conversationID, t.turn.ID, attempt)
	persistCancel()
	if err != nil {
		t.persistErr = err
		t.run.cancel()
		return
	}
	if persisted != nil {
		attempt = persisted
	}
	t.attempts = append(t.attempts, attempt)
	t.current = attempt
	if err := t.service.emitAvailabilityEvent(t.run, t.ownerUserID, AvailabilityEvent{Type: AvailabilityEventAttemptStarted, Attempt: cloneAvailabilityAttempt(attempt)}); err != nil {
		t.emitErr = err
		t.run.cancel()
	}
}

// currentGinContext is set by executeAvailabilityProbe immediately before the
// real tester is invoked. Keeping it on the tracker avoids putting account
// objects into the durable event payload.
func (t *availabilityAttemptTracker) setGinContext(c *gin.Context) {
	if t != nil {
		t.currentGinContext = c
	}
}

func (t *availabilityAttemptTracker) observeContent(event TestEvent) {
	if strings.TrimSpace(event.Text) == "" {
		return
	}
	now := time.Now().UTC()
	if t.firstResponseAt.IsZero() {
		t.firstResponseAt = now
	}
	if t.attemptFirstResponseAt.IsZero() {
		t.attemptFirstResponseAt = now
	}
	_, _ = t.content.WriteString(event.Text)
	if t.current != nil && t.current.FirstResponseMs == nil {
		ms := nonNegativeDurationMs(t.attemptFirstResponseAt.Sub(t.current.StartedAt))
		t.current.FirstResponseMs = &ms
		t.updateCurrent()
	}
	persistCtx, persistCancel := availabilityPersistenceContext()
	err := t.service.repo.UpdateTurnContent(persistCtx, t.ownerUserID, t.conversationID, t.turn.ID, t.content.String(), t.turnFirstResponseMs())
	persistCancel()
	if err != nil && t.persistErr == nil {
		t.persistErr = err
	}
	if err := t.service.emitAvailabilityEvent(t.run, t.ownerUserID, AvailabilityEvent{Type: AvailabilityEventContentDelta, Delta: event.Text}); err != nil && t.emitErr == nil {
		t.emitErr = err
	}
}

func (t *availabilityAttemptTracker) observeFailure(event TestEvent) {
	message := truncateAccountTestError(event.Error)
	t.completeCurrent(AvailabilityAttemptFailed, message, event.StatusCode)
	if t.current == nil {
		return
	}
	if err := t.service.emitAvailabilityEvent(t.run, t.ownerUserID, AvailabilityEvent{Type: AvailabilityEventAttemptFailed, Attempt: cloneAvailabilityAttempt(t.current), Error: message}); err != nil && t.emitErr == nil {
		t.emitErr = err
	}
}

func (t *availabilityAttemptTracker) completeCurrent(status, message string, statusCode int) {
	if t.current == nil || t.current.Status != AvailabilityAttemptRunning {
		return
	}
	now := time.Now().UTC()
	t.current.Status = status
	t.current.CompletedAt = &now
	if t.current.FirstResponseMs == nil && !t.attemptFirstResponseAt.IsZero() {
		ms := nonNegativeDurationMs(t.attemptFirstResponseAt.Sub(t.current.StartedAt))
		t.current.FirstResponseMs = &ms
	}
	total := nonNegativeDurationMs(now.Sub(t.current.StartedAt))
	t.current.TotalMs = &total
	if statusCode > 0 {
		t.current.StatusCode = statusCode
	}
	if message != "" {
		t.current.Error = truncateAccountTestError(message)
	}
	if status == AvailabilityAttemptCancelled && message != "" {
		t.current.Reason = truncateAccountTestError(message)
	}
	t.updateCurrent()
}

func (t *availabilityAttemptTracker) updateCurrent() {
	if t.current == nil {
		return
	}
	persistCtx, persistCancel := availabilityPersistenceContext()
	err := t.service.repo.UpdateAttempt(persistCtx, t.ownerUserID, t.conversationID, t.turn.ID, t.current)
	persistCancel()
	if err != nil && t.persistErr == nil {
		t.persistErr = err
	}
}

func (t *availabilityAttemptTracker) turnFirstResponseMs() *int64 {
	if t.firstResponseAt.IsZero() || t.turn == nil {
		return nil
	}
	ms := nonNegativeDurationMs(t.firstResponseAt.Sub(t.turn.CreatedAt))
	return &ms
}

func (t *availabilityAttemptTracker) finalizeRunning(status, message string) {
	for _, attempt := range t.attempts {
		if attempt.Status == AvailabilityAttemptRunning {
			if t.current != attempt {
				t.current = attempt
			}
			t.completeCurrent(status, message, 0)
		}
	}
}

func (t *availabilityAttemptTracker) applyResult(result *AccountTestDebugResult) {
	if result == nil {
		return
	}
	if t.content.Len() == 0 && result.Content != "" {
		_, _ = t.content.WriteString(result.Content)
	}
	if len(t.attempts) == t.initialAttemptCount && len(result.Attempts) > 0 {
		// A failure can happen before the tester emits test_start (for example,
		// missing credentials). Legacy result attempts are concrete scheduler
		// selections, so preserving them here is safe and useful.
		requestedModel := strings.TrimSpace(result.RequestedModel)
		if requestedModel == "" {
			requestedModel = t.requestedModel
		}
		for _, legacy := range result.Attempts {
			model := strings.TrimSpace(legacy.Model)
			if model == "" {
				model = strings.TrimSpace(result.Model)
			}
			copy := AvailabilityAttempt{
				Index:          t.nextAttemptIndex(),
				AccountID:      legacy.AccountID,
				AccountName:    legacy.AccountName,
				RequestedModel: requestedModel,
				Model:          model,
				Endpoint:       safeAccountTestEndpoint(legacy.Endpoint),
				Status:         AvailabilityAttemptFailed,
				StatusCode:     legacy.StatusCode,
				Error:          truncateAccountTestError(legacy.Error),
				StartedAt:      time.Now().UTC(),
			}
			if legacy.Success {
				copy.Status = AvailabilityAttemptSucceeded
			}
			completed := copy.StartedAt.Add(time.Duration(legacy.Timing.TotalMs) * time.Millisecond)
			copy.CompletedAt = &completed
			if legacy.Timing.FirstResponseMs > 0 {
				first := legacy.Timing.FirstResponseMs
				copy.FirstResponseMs = &first
			}
			total := legacy.Timing.TotalMs
			copy.TotalMs = &total
			persistCtx, persistCancel := availabilityPersistenceContext()
			persisted, err := t.service.repo.CreateAttempt(persistCtx, t.ownerUserID, t.conversationID, t.turn.ID, &copy)
			persistCancel()
			if err != nil {
				if t.persistErr == nil {
					t.persistErr = err
				}
				// Do not publish an attempt_failed event for an attempt that was
				// not durably created.  The terminal turn will expose the
				// persistence failure instead of presenting an orphan event.
				continue
			}
			if persisted != nil {
				copy = *persisted
			}
			t.attempts = append(t.attempts, &copy)
			if copy.Status == AvailabilityAttemptFailed {
				if emitErr := t.service.emitAvailabilityEvent(t.run, t.ownerUserID, AvailabilityEvent{
					Type:    AvailabilityEventAttemptFailed,
					Attempt: cloneAvailabilityAttempt(&copy),
					Error:   copy.Error,
				}); emitErr != nil && t.emitErr == nil {
					t.emitErr = emitErr
					t.run.cancel()
				}
			}
		}
	}
	if t.current != nil && t.current.Status == AvailabilityAttemptRunning {
		if result.Success {
			t.completeCurrent(AvailabilityAttemptSucceeded, "", result.StatusCode)
		} else {
			t.completeCurrent(AvailabilityAttemptFailed, result.Error, result.StatusCode)
		}
	}
}

// runAvailabilityTurn is the only place that invokes the real account/group
// tester for a V2 turn.  Keeping the invocation behind this method makes the
// durable state machine explicit: every exit path attempts to leave a
// terminal turn state, while all persistence operations use their own short
// timeout and are not lost merely because the client disconnected.
func (s *AvailabilityV2Service) runAvailabilityTurn(run *availabilityRun, ownerUserID int64, conversation *AvailabilityConversation, turn *AvailabilityTurn) {
	if s == nil || run == nil || conversation == nil || turn == nil {
		if run != nil {
			run.finish()
		}
		return
	}

	defer func() {
		run.finish()
		s.runsMu.Lock()
		if current := s.runs[run.key]; current == run {
			delete(s.runs, run.key)
		}
		s.runsMu.Unlock()
	}()

	// A terminal event can already be present after a worker completed just as
	// a reconnect arrived.  Reconnects replay it; they must not run upstream
	// again.
	if run.hasTerminalEvent() {
		return
	}
	if run.workCtx.Err() != nil {
		s.finalizeAvailabilityTurn(run, ownerUserID, conversation, turn, nil, AvailabilityTurnCancelled, "availability test cancelled")
		return
	}

	tracker := &availabilityAttemptTracker{
		service:        s,
		run:            run,
		ownerUserID:    ownerUserID,
		conversationID: conversation.ID,
		turn:           turn,
		requestedModel: conversation.Model,
	}

	// A process restart can leave an attempt marked running.  It is no longer
	// attached to a live worker, so close that stale attempt before retrying the
	// same durable turn.  This is intentionally done only when a new in-process
	// run is created; duplicate streams in this process share one run.
	persistCtx, persistCancel := availabilityPersistenceContext()
	_ = s.repo.MarkRunningAttemptsCancelled(persistCtx, ownerUserID, conversation.ID, turn.ID, "availability worker restarted")
	persistCancel()
	tracker.seedExistingAttempts(turn.Attempts)

	if !run.hasEventType(AvailabilityEventTurnStarted) {
		if err := s.emitAvailabilityEvent(run, ownerUserID, AvailabilityEvent{
			Type: AvailabilityEventTurnStarted,
			Turn: cloneAvailabilityTurn(turn),
		}); err != nil {
			s.finalizeAvailabilityTurn(run, ownerUserID, conversation, turn, tracker, AvailabilityTurnFailed, "failed to persist turn start: "+err.Error())
			return
		}
	}
	if !run.hasEventType(AvailabilityEventRouting) {
		if err := s.emitAvailabilityEvent(run, ownerUserID, AvailabilityEvent{Type: AvailabilityEventRouting}); err != nil {
			s.finalizeAvailabilityTurn(run, ownerUserID, conversation, turn, tracker, AvailabilityTurnFailed, "failed to persist routing event: "+err.Error())
			return
		}
	}

	history, err := s.repo.ListSuccessfulTurns(run.workCtx, ownerUserID, conversation.ID, turn.ID)
	if err != nil {
		status := AvailabilityTurnFailed
		message := "failed to load conversation context: " + err.Error()
		if run.workCtx.Err() != nil {
			status = AvailabilityTurnCancelled
			message = "availability test cancelled"
		}
		s.finalizeAvailabilityTurn(run, ownerUserID, conversation, turn, tracker, status, message)
		return
	}
	prompt := buildAvailabilityPrompt(history, turn.Prompt)
	result, probeErr := s.executeAvailabilityProbe(run, conversation, tracker, prompt)
	tracker.applyResult(result)
	if result != nil && result.Success && result.Content != "" && len(tracker.attempts) > 1 {
		// Group failover can emit deltas from a failed first attempt before the
		// final account succeeds.  Keep those deltas in the event log for
		// observability, but make the durable answer (and future context) the
		// final successful probe response rather than a concatenated half-answer.
		tracker.content.Reset()
		_, _ = tracker.content.WriteString(result.Content)
	}

	status := AvailabilityTurnSucceeded
	message := ""
	if tracker.persistErr != nil {
		status = AvailabilityTurnFailed
		message = "failed to persist availability output: " + tracker.persistErr.Error()
	} else if tracker.emitErr != nil {
		status = AvailabilityTurnFailed
		message = "failed to persist availability event: " + tracker.emitErr.Error()
	} else if run.workCtx.Err() != nil {
		status = AvailabilityTurnCancelled
		message = "availability test cancelled"
	} else if probeErr != nil {
		status = AvailabilityTurnFailed
		message = truncateAccountTestError(probeErr.Error())
	} else if result == nil || !result.Success {
		status = AvailabilityTurnFailed
		if result != nil {
			message = truncateAccountTestError(result.Error)
		}
		if message == "" {
			message = "availability test failed"
		}
	}

	s.finalizeAvailabilityTurn(run, ownerUserID, conversation, turn, tracker, status, message)
}

func (s *AvailabilityV2Service) executeAvailabilityProbe(run *availabilityRun, conversation *AvailabilityConversation, tracker *availabilityAttemptTracker, prompt string) (*AccountTestDebugResult, error) {
	if s == nil || s.accountTest == nil {
		return nil, errors.New("account test service is unavailable")
	}
	if run == nil || conversation == nil || tracker == nil {
		return nil, errors.New("availability probe is unavailable")
	}
	if s.groupRepo == nil {
		return nil, errors.New("group repository is unavailable")
	}
	group, err := s.groupRepo.GetByID(run.workCtx, conversation.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, ErrGroupNotFound
	}
	if !group.ModelAllowlist.Allows(conversation.Model) {
		return nil, ErrAvailabilityModelNotAllowed
	}

	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/channel-test", strings.NewReader("{}"))
	request = request.WithContext(run.workCtx)
	c.Request = request
	setAccountTestEventObserver(c, tracker.observe)
	tracker.setGinContext(c)
	defer func() {
		setAccountTestEventObserver(c, nil)
		setAccountTestActiveAccount(c, nil)
		tracker.setGinContext(nil)
	}()

	if conversation.AccountID != nil {
		if s.accountRepo == nil {
			return nil, errors.New("account repository is unavailable")
		}
		account, err := s.accountRepo.GetByID(run.workCtx, *conversation.AccountID)
		if err != nil {
			return nil, err
		}
		if !accountBelongsToAvailabilityGroup(account, conversation.GroupID) {
			return nil, ErrAvailabilityAccountNotInGroup
		}
		probeModel := conversation.Model
		if group.Platform == PlatformComposite {
			if s.accountTest.gatewayService == nil {
				return nil, errors.New("composite route resolver is unavailable")
			}
			decision, matched, resolveErr := s.accountTest.gatewayService.resolveCompositeRouteDecision(
				run.workCtx, group, conversation.Model, CompositeRouteEndpointAny,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if !matched || strings.TrimSpace(decision.UpstreamModel) == "" {
				return nil, fmt.Errorf("no composite route supports model: %s", conversation.Model)
			}
			if strings.TrimSpace(account.Platform) != strings.TrimSpace(decision.TargetPlatform) {
				return nil, ErrAvailabilityAccountModelMismatch
			}
			probeModel = decision.UpstreamModel
			c.Request = c.Request.WithContext(WithCompositeRouteDecision(c.Request.Context(), decision))
		}
		// The selected-account mode deliberately bypasses group failover, but it
		// still uses the existing account tester and its real provider path.
		setAccountTestActiveAccount(c, account)
		return s.accountTest.TestAccountDebug(c, account.ID, probeModel, prompt)
	}

	return s.accountTest.TestGroupDebug(c, conversation.GroupID, conversation.Model, prompt)
}

func (s *AvailabilityV2Service) finalizeAvailabilityTurn(run *availabilityRun, ownerUserID int64, conversation *AvailabilityConversation, turn *AvailabilityTurn, tracker *availabilityAttemptTracker, status, message string) {
	if s == nil || s.repo == nil || run == nil || conversation == nil || turn == nil {
		return
	}
	if status != AvailabilityTurnSucceeded && status != AvailabilityTurnFailed && status != AvailabilityTurnCancelled {
		status = AvailabilityTurnFailed
	}
	message = truncateAccountTestError(message)

	if tracker != nil {
		tracker.finalizeRunning(statusToAvailabilityAttemptStatus(status), message)
		if tracker.persistErr != nil && status == AvailabilityTurnSucceeded {
			status = AvailabilityTurnFailed
			message = "failed to persist availability attempt: " + tracker.persistErr.Error()
		}
	}
	persistCtx, persistCancel := availabilityPersistenceContext()
	if tracker != nil && tracker.content.Len() > 0 {
		if err := s.repo.UpdateTurnContent(persistCtx, ownerUserID, conversation.ID, turn.ID, tracker.content.String(), tracker.turnFirstResponseMs()); err != nil && tracker.persistErr == nil {
			tracker.persistErr = err
			if status == AvailabilityTurnSucceeded {
				status = AvailabilityTurnFailed
				message = "failed to persist availability output: " + err.Error()
			}
		}
	}
	completedAt := time.Now().UTC()
	firstResponseMs := (*int64)(nil)
	if tracker != nil {
		firstResponseMs = tracker.turnFirstResponseMs()
	}
	totalMs := nonNegativeDurationMs(completedAt.Sub(turn.CreatedAt))
	update := AvailabilityV2TurnUpdate{
		Status:          status,
		Content:         availabilityTurnContent(tracker),
		Error:           message,
		CompletedAt:     &completedAt,
		FirstResponseMs: firstResponseMs,
		TotalMs:         &totalMs,
	}
	finalizeErr := s.repo.FinalizeTurn(persistCtx, ownerUserID, conversation.ID, turn.ID, update)
	if finalizeErr != nil {
		// A cancellation may have won the conditional update concurrently.  Do
		// not turn that into a false failure; the fresh read below decides the
		// durable terminal state.  Other persistence failures get one guarded
		// cancellation fallback so a disconnected client cannot leave a forever
		// running turn when the database is otherwise reachable.
		if status != AvailabilityTurnCancelled {
			_, _ = s.repo.MarkTurnCancelled(persistCtx, ownerUserID, conversation.ID, turn.ID, message)
		}
	}
	_ = s.repo.MarkRunningAttemptsCancelled(persistCtx, ownerUserID, conversation.ID, turn.ID, message)
	_ = s.repo.UpdateConversationAt(persistCtx, ownerUserID, conversation.ID, completedAt)

	finalTurn, readErr := s.repo.GetTurn(persistCtx, ownerUserID, conversation.ID, turn.ID)
	persistCancel()
	if readErr != nil || finalTurn == nil {
		finalTurn = cloneAvailabilityTurn(turn)
		finalTurn.Status = status
		finalTurn.Content = availabilityTurnContent(tracker)
		finalTurn.Error = message
		finalTurn.CompletedAt = &completedAt
		finalTurn.FirstResponseMs = firstResponseMs
		finalTurn.TotalMs = &totalMs
		if tracker != nil {
			finalTurn.Attempts = cloneAvailabilityAttempts(tracker.attempts)
		}
	}
	if finalTurn.Status != AvailabilityTurnSucceeded && finalTurn.Status != AvailabilityTurnFailed && finalTurn.Status != AvailabilityTurnCancelled {
		finalTurn.Status = status
	}
	switch finalTurn.Status {
	case AvailabilityTurnCancelled:
		status = AvailabilityTurnCancelled
	case AvailabilityTurnSucceeded:
		status = AvailabilityTurnSucceeded
	default:
		status = AvailabilityTurnFailed
	}

	if run.hasTerminalEvent() {
		return
	}
	terminalType := AvailabilityEventTurnFailed
	switch status {
	case AvailabilityTurnSucceeded:
		terminalType = AvailabilityEventTurnCompleted
	case AvailabilityTurnCancelled:
		terminalType = AvailabilityEventTurnCancelled
	}
	_ = s.emitAvailabilityEvent(run, ownerUserID, AvailabilityEvent{
		Type:  terminalType,
		Turn:  cloneAvailabilityTurn(finalTurn),
		Error: finalTurn.Error,
	})
}

func statusToAvailabilityAttemptStatus(status string) string {
	switch status {
	case AvailabilityTurnSucceeded:
		return AvailabilityAttemptSucceeded
	case AvailabilityTurnCancelled:
		return AvailabilityAttemptCancelled
	default:
		return AvailabilityAttemptFailed
	}
}

func availabilityTurnContent(tracker *availabilityAttemptTracker) string {
	if tracker == nil {
		return ""
	}
	return tracker.content.String()
}

func buildAvailabilityPrompt(history []AvailabilityTurn, prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(history) == 0 {
		return prompt
	}

	const historyPrefix = "Conversation history (only successful turns):\n"
	const currentPrefix = "\nCurrent user message:\n"
	current := currentPrefix + prompt
	if len(historyPrefix)+len(current) >= availabilityMaxContextLength {
		return prompt
	}

	// Keep the newest successful turns when the durable history is larger than
	// the probe context budget.  The current prompt is always kept intact.
	budget := availabilityMaxContextLength - len(historyPrefix) - len(current)
	parts := make([]string, 0, len(history))
	used := 0
	for i := len(history) - 1; i >= 0; i-- {
		part := fmt.Sprintf("User: %s\nAssistant: %s\n", history[i].Prompt, history[i].Content)
		if used+len(part) > budget {
			break
		}
		parts = append(parts, part)
		used += len(part)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return historyPrefix + strings.Join(parts, "") + current
}

func (s *AvailabilityV2Service) CancelTurn(ctx context.Context, ownerUserID, conversationID, turnID int64) (*AvailabilityTurn, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("availability repository is unavailable")
	}
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("AVAILABILITY_OWNER_REQUIRED", "authenticated administrator is required")
	}
	if conversationID <= 0 || turnID <= 0 {
		return nil, ErrAvailabilityTurnNotFound
	}
	turn, err := s.repo.GetTurn(ctx, ownerUserID, conversationID, turnID)
	if err != nil {
		return nil, err
	}
	if turn.Status != AvailabilityTurnRunning {
		return turn, nil
	}

	key := availabilityRunKey(ownerUserID, conversationID, turnID)
	s.runsMu.Lock()
	run := s.runs[key]
	s.runsMu.Unlock()

	persistCtx, persistCancel := availabilityPersistenceContext()
	_, markErr := s.repo.MarkTurnCancelled(persistCtx, ownerUserID, conversationID, turnID, "cancelled by administrator")
	if markErr != nil {
		persistCancel()
		return nil, markErr
	}
	markAttemptsErr := s.repo.MarkRunningAttemptsCancelled(persistCtx, ownerUserID, conversationID, turnID, "cancelled by administrator")
	if run != nil {
		run.cancel()
	}
	persistCancel()
	if markAttemptsErr != nil {
		return nil, markAttemptsErr
	}
	if run != nil {
		select {
		case <-run.doneCh:
		case <-ctx.Done():
		case <-time.After(availabilityCancelWait):
		}
		persistCtx, persistCancel = availabilityPersistenceContext()
		turn, err = s.repo.GetTurn(persistCtx, ownerUserID, conversationID, turnID)
		persistCancel()
		if err != nil {
			return nil, err
		}
		return turn, nil
	}

	// There is no local worker (for example after a process restart).  Make
	// the cancellation replayable immediately instead of relying on a future
	// request to discover the state transition.
	persistCtx, persistCancel = availabilityPersistenceContext()
	turn, err = s.repo.GetTurn(persistCtx, ownerUserID, conversationID, turnID)
	if err == nil && turn != nil {
		s.appendStandaloneTerminalEvent(persistCtx, ownerUserID, conversationID, turn)
	}
	persistCancel()
	if err != nil {
		return nil, err
	}
	return turn, nil
}

func (s *AvailabilityV2Service) appendStandaloneTerminalEvent(ctx context.Context, ownerUserID, conversationID int64, turn *AvailabilityTurn) {
	if s == nil || s.repo == nil || turn == nil {
		return
	}
	events, err := s.repo.ListEvents(ctx, ownerUserID, conversationID, turn.ID)
	if err != nil {
		return
	}
	for _, event := range events {
		if isAvailabilityTerminalEvent(event.Type) {
			return
		}
	}
	var nextSeq int64
	for _, event := range events {
		if event.Seq > nextSeq {
			nextSeq = event.Seq
		}
	}
	eventType := AvailabilityEventTurnCancelled
	switch turn.Status {
	case AvailabilityTurnSucceeded:
		eventType = AvailabilityEventTurnCompleted
	case AvailabilityTurnFailed:
		eventType = AvailabilityEventTurnFailed
	}
	_ = s.repo.AppendEvent(ctx, ownerUserID, AvailabilityEvent{
		Type:           eventType,
		ConversationID: conversationID,
		TurnID:         turn.ID,
		Seq:            nextSeq + 1,
		At:             time.Now().UTC(),
		Turn:           cloneAvailabilityTurn(turn),
		Error:          turn.Error,
	})
}

func isAvailabilityTerminalEvent(eventType string) bool {
	switch eventType {
	case AvailabilityEventTurnCompleted, AvailabilityEventTurnFailed, AvailabilityEventTurnCancelled:
		return true
	default:
		return false
	}
}

func cloneAvailabilityAttempts(input []*AvailabilityAttempt) []AvailabilityAttempt {
	if len(input) == 0 {
		return []AvailabilityAttempt{}
	}
	output := make([]AvailabilityAttempt, 0, len(input))
	for _, attempt := range input {
		if attempt != nil {
			output = append(output, *cloneAvailabilityAttempt(attempt))
		}
	}
	return output
}

func cloneAvailabilityAttempt(input *AvailabilityAttempt) *AvailabilityAttempt {
	if input == nil {
		return nil
	}
	output := *input
	if input.CompletedAt != nil {
		value := *input.CompletedAt
		output.CompletedAt = &value
	}
	if input.FirstResponseMs != nil {
		value := *input.FirstResponseMs
		output.FirstResponseMs = &value
	}
	if input.TotalMs != nil {
		value := *input.TotalMs
		output.TotalMs = &value
	}
	return &output
}

func cloneAvailabilityTurn(input *AvailabilityTurn) *AvailabilityTurn {
	if input == nil {
		return nil
	}
	output := *input
	if input.CompletedAt != nil {
		value := *input.CompletedAt
		output.CompletedAt = &value
	}
	if input.FirstResponseMs != nil {
		value := *input.FirstResponseMs
		output.FirstResponseMs = &value
	}
	if input.TotalMs != nil {
		value := *input.TotalMs
		output.TotalMs = &value
	}
	output.Attempts = append([]AvailabilityAttempt(nil), input.Attempts...)
	for i := range output.Attempts {
		output.Attempts[i] = *cloneAvailabilityAttempt(&output.Attempts[i])
	}
	return &output
}
