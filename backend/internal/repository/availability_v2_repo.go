package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// availabilityV2Repository uses the same PostgreSQL connection as Ent but
// keeps the append-only availability records out of generated Ent code.  The
// owner predicate is repeated on every read so an administrator can never
// discover another administrator's conversations by guessing an ID.
type availabilityV2Repository struct {
	db *sql.DB
}

func NewAvailabilityV2Repository(db *sql.DB) service.AvailabilityV2Repository {
	return &availabilityV2Repository{db: db}
}

type availabilityScanner interface {
	Scan(dest ...any) error
}

func (r *availabilityV2Repository) ensureDB() error {
	if r == nil || r.db == nil {
		return errors.New("nil availability repository database")
	}
	return nil
}

func (r *availabilityV2Repository) CreateConversation(ctx context.Context, ownerUserID, groupID int64, accountID *int64, model, title string) (*service.AvailabilityConversation, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	var conversation service.AvailabilityConversation
	var storedAccountID sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO availability_conversations
			(owner_user_id, group_id, account_id, model, title)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, group_id, account_id, model, title, created_at, updated_at
	`, ownerUserID, groupID, availabilityInt64Arg(accountID), model, title).Scan(
		&conversation.ID,
		&conversation.GroupID,
		&storedAccountID,
		&conversation.Model,
		&conversation.Title,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if storedAccountID.Valid {
		value := storedAccountID.Int64
		conversation.AccountID = &value
	}
	return &conversation, nil
}

func (r *availabilityV2Repository) GetConversation(ctx context.Context, ownerUserID, id int64) (*service.AvailabilityConversation, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	var conversation service.AvailabilityConversation
	var accountID sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT id, group_id, account_id, model, title, created_at, updated_at
		FROM availability_conversations
		WHERE owner_user_id = $1 AND id = $2
	`, ownerUserID, id).Scan(
		&conversation.ID,
		&conversation.GroupID,
		&accountID,
		&conversation.Model,
		&conversation.Title,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAvailabilityConversationNotFound
	}
	if err != nil {
		return nil, err
	}
	if accountID.Valid {
		value := accountID.Int64
		conversation.AccountID = &value
	}
	return &conversation, nil
}

func (r *availabilityV2Repository) ListConversations(ctx context.Context, ownerUserID int64, page, pageSize int, search string) ([]service.AvailabilityConversation, int64, error) {
	if err := r.ensureDB(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM availability_conversations
		WHERE owner_user_id = $1
		  AND ($2 = '' OR title ILIKE '%' || $2 || '%' OR model ILIKE '%' || $2 || '%')
	`, ownerUserID, search).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, group_id, account_id, model, title, created_at, updated_at
		FROM availability_conversations
		WHERE owner_user_id = $1
		  AND ($2 = '' OR title ILIKE '%' || $2 || '%' OR model ILIKE '%' || $2 || '%')
		ORDER BY updated_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`, ownerUserID, search, pageSize, availabilityOffset(page, pageSize))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.AvailabilityConversation, 0)
	for rows.Next() {
		conversation, scanErr := scanAvailabilityConversation(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, *conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *availabilityV2Repository) CreateTurn(ctx context.Context, ownerUserID, conversationID int64, prompt, clientRequestID string) (*service.AvailabilityTurn, bool, error) {
	if err := r.ensureDB(); err != nil {
		return nil, false, err
	}
	var turn service.AvailabilityTurn
	var completedAt sql.NullTime
	var firstMs, total sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO availability_turns
			(owner_user_id, conversation_id, prompt, client_request_id)
		SELECT $1, $2, $3, $4
		WHERE EXISTS (
			SELECT 1 FROM availability_conversations
			WHERE id = $2 AND owner_user_id = $1
		)
		ON CONFLICT (conversation_id, client_request_id) DO NOTHING
		RETURNING id, conversation_id, prompt, content, status, created_at,
		          completed_at, first_response_ms, total_ms, error_message
	`, ownerUserID, conversationID, prompt, clientRequestID).Scan(
		&turn.ID,
		&turn.ConversationID,
		&turn.Prompt,
		&turn.Content,
		&turn.Status,
		&turn.CreatedAt,
		&completedAt,
		&firstMs,
		&total,
		&turn.Error,
	)
	if err == nil {
		if completedAt.Valid {
			value := completedAt.Time
			turn.CompletedAt = &value
		}
		if firstMs.Valid {
			value := firstMs.Int64
			turn.FirstResponseMs = &value
		}
		if total.Valid {
			value := total.Int64
			turn.TotalMs = &value
		}
		turn.Attempts = []service.AvailabilityAttempt{}
		return &turn, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	existing, getErr := r.GetTurnByClientRequestID(ctx, ownerUserID, conversationID, clientRequestID)
	if getErr != nil {
		return nil, false, getErr
	}
	return existing, false, nil
}

func (r *availabilityV2Repository) GetTurn(ctx context.Context, ownerUserID, conversationID, turnID int64) (*service.AvailabilityTurn, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, availabilityTurnQuery+`
		WHERE t.owner_user_id = $1 AND t.conversation_id = $2 AND t.id = $3
	`, ownerUserID, conversationID, turnID)
	turn, err := scanAvailabilityTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAvailabilityTurnNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadAttempts(ctx, ownerUserID, conversationID, turn); err != nil {
		return nil, err
	}
	return turn, nil
}

func (r *availabilityV2Repository) GetTurnByClientRequestID(ctx context.Context, ownerUserID, conversationID int64, clientRequestID string) (*service.AvailabilityTurn, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, availabilityTurnQuery+`
		WHERE t.owner_user_id = $1 AND t.conversation_id = $2 AND t.client_request_id = $3
	`, ownerUserID, conversationID, clientRequestID)
	turn, err := scanAvailabilityTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAvailabilityTurnNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadAttempts(ctx, ownerUserID, conversationID, turn); err != nil {
		return nil, err
	}
	return turn, nil
}

const availabilityTurnQuery = `
	SELECT t.id, t.conversation_id, t.prompt, t.content, t.status,
	       t.created_at, t.completed_at, t.first_response_ms, t.total_ms,
	       t.error_message
	FROM availability_turns t
	JOIN availability_conversations c
	  ON c.id = t.conversation_id AND c.owner_user_id = t.owner_user_id
`

func (r *availabilityV2Repository) ListTurns(ctx context.Context, ownerUserID, conversationID int64, page, pageSize int) ([]service.AvailabilityTurn, int64, error) {
	if err := r.ensureDB(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM availability_turns t
		JOIN availability_conversations c
		  ON c.id = t.conversation_id AND c.owner_user_id = t.owner_user_id
		WHERE t.owner_user_id = $1 AND t.conversation_id = $2
	`, ownerUserID, conversationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, availabilityTurnQuery+`
		WHERE t.owner_user_id = $1 AND t.conversation_id = $2
		ORDER BY t.id ASC
		LIMIT $3 OFFSET $4
	`, ownerUserID, conversationID, pageSize, availabilityOffset(page, pageSize))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.AvailabilityTurn, 0)
	for rows.Next() {
		turn, scanErr := scanAvailabilityTurn(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, *turn)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// Close the turn result before loading attempts.  Querying through the same
	// *sql.DB while rows is still open can deadlock deployments configured with
	// MaxOpenConns=1 (and needlessly holds a pooled connection on larger pools).
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	for i := range items {
		if loadErr := r.loadAttempts(ctx, ownerUserID, conversationID, &items[i]); loadErr != nil {
			return nil, 0, loadErr
		}
	}
	return items, total, nil
}

func (r *availabilityV2Repository) ListSuccessfulTurns(ctx context.Context, ownerUserID, conversationID, beforeTurnID int64) ([]service.AvailabilityTurn, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, availabilityTurnQuery+`
		WHERE t.owner_user_id = $1 AND t.conversation_id = $2
		  AND t.status = 'succeeded' AND t.id < $3
		ORDER BY t.id ASC
	`, ownerUserID, conversationID, beforeTurnID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	turns := make([]service.AvailabilityTurn, 0)
	for rows.Next() {
		turn, scanErr := scanAvailabilityTurn(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		turns = append(turns, *turn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range turns {
		if loadErr := r.loadAttempts(ctx, ownerUserID, conversationID, &turns[i]); loadErr != nil {
			return nil, loadErr
		}
	}
	return turns, nil
}

func (r *availabilityV2Repository) loadAttempts(ctx context.Context, ownerUserID, conversationID int64, turn *service.AvailabilityTurn) error {
	if turn == nil {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, attempt_index, account_id, account_name, requested_model,
		       model, endpoint, status, started_at, completed_at,
		       first_response_ms, total_ms, status_code, error_message, reason
		FROM availability_attempts
		WHERE owner_user_id = $1 AND conversation_id = $2 AND turn_id = $3
		ORDER BY attempt_index ASC, id ASC
	`, ownerUserID, conversationID, turn.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	turn.Attempts = make([]service.AvailabilityAttempt, 0)
	for rows.Next() {
		attempt, scanErr := scanAvailabilityAttempt(rows)
		if scanErr != nil {
			return scanErr
		}
		turn.Attempts = append(turn.Attempts, *attempt)
	}
	return rows.Err()
}

func (r *availabilityV2Repository) FinalizeTurn(ctx context.Context, ownerUserID, conversationID, turnID int64, update service.AvailabilityV2TurnUpdate) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	completedAt := availabilityTimeArg(update.CompletedAt)
	firstResponseMs := availabilityInt64Arg(update.FirstResponseMs)
	totalMs := availabilityInt64Arg(update.TotalMs)
	result, err := r.db.ExecContext(ctx, `
		UPDATE availability_turns
		SET status = $4,
		    content = $5,
		    error_message = $6,
		    completed_at = $7,
		    first_response_ms = $8,
		    total_ms = $9,
		    updated_at = NOW()
		WHERE owner_user_id = $1 AND conversation_id = $2 AND id = $3
		  AND status = 'running'
	`, ownerUserID, conversationID, turnID, update.Status, update.Content, update.Error, completedAt, firstResponseMs, totalMs)
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		// Idempotent finalization is expected when CancelTurn won the race.
		if _, getErr := r.GetTurn(ctx, ownerUserID, conversationID, turnID); getErr != nil {
			return getErr
		}
	}
	return nil
}

func (r *availabilityV2Repository) UpdateTurnContent(ctx context.Context, ownerUserID, conversationID, turnID int64, content string, firstResponseMs *int64) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE availability_turns
		SET content = $4,
		    first_response_ms = COALESCE(first_response_ms, $5),
		    updated_at = NOW()
		WHERE owner_user_id = $1 AND conversation_id = $2 AND id = $3
	`, ownerUserID, conversationID, turnID, content, availabilityInt64Arg(firstResponseMs))
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		return service.ErrAvailabilityTurnNotFound
	}
	return nil
}

func (r *availabilityV2Repository) MarkTurnCancelled(ctx context.Context, ownerUserID, conversationID, turnID int64, message string) (bool, error) {
	if err := r.ensureDB(); err != nil {
		return false, err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE availability_turns
		SET status = 'cancelled',
		    error_message = $4,
		    completed_at = COALESCE(completed_at, NOW()),
		    total_ms = COALESCE(total_ms, GREATEST(0, FLOOR(EXTRACT(EPOCH FROM (NOW() - created_at)) * 1000)::bigint)),
		    updated_at = NOW()
		WHERE owner_user_id = $1 AND conversation_id = $2 AND id = $3
		  AND status = 'running'
	`, ownerUserID, conversationID, turnID, strings.TrimSpace(message))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *availabilityV2Repository) MarkRunningAttemptsCancelled(ctx context.Context, ownerUserID, conversationID, turnID int64, message string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE availability_attempts
		SET status = 'cancelled',
		    error_message = CASE WHEN $4 = '' THEN error_message ELSE $4 END,
		    reason = CASE WHEN $4 = '' THEN reason ELSE $4 END,
		    completed_at = COALESCE(completed_at, NOW()),
		    total_ms = COALESCE(total_ms, GREATEST(0, FLOOR(EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000)::bigint))
		WHERE owner_user_id = $1 AND conversation_id = $2 AND turn_id = $3
		  AND status = 'running'
	`, ownerUserID, conversationID, turnID, strings.TrimSpace(message))
	return err
}

func (r *availabilityV2Repository) CreateAttempt(ctx context.Context, ownerUserID, conversationID, turnID int64, attempt *service.AvailabilityAttempt) (*service.AvailabilityAttempt, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	if attempt == nil {
		return nil, errors.New("nil availability attempt")
	}
	created := &service.AvailabilityAttempt{}
	var completedAt sql.NullTime
	var firstResponseMs, totalMs sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO availability_attempts
			(owner_user_id, conversation_id, turn_id, attempt_index, account_id,
			 account_name, requested_model, model, endpoint, status, started_at,
			 completed_at, first_response_ms, total_ms, status_code, error_message, reason)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17
		WHERE EXISTS (
			SELECT 1
			FROM availability_turns t
			JOIN availability_conversations c
			  ON c.id = t.conversation_id AND c.owner_user_id = t.owner_user_id
			WHERE t.id = $3 AND t.conversation_id = $2 AND t.owner_user_id = $1
		)
		RETURNING id, attempt_index, account_id, account_name, requested_model,
		          model, endpoint, status, started_at, completed_at,
		          first_response_ms, total_ms, status_code, error_message, reason
	`, ownerUserID, conversationID, turnID, attempt.Index, attempt.AccountID,
		attempt.AccountName, attempt.RequestedModel, attempt.Model, attempt.Endpoint,
		attempt.Status, attempt.StartedAt, availabilityTimeArg(attempt.CompletedAt),
		availabilityInt64Arg(attempt.FirstResponseMs), availabilityInt64Arg(attempt.TotalMs),
		attempt.StatusCode, attempt.Error, attempt.Reason).Scan(
		&created.ID,
		&created.Index,
		&created.AccountID,
		&created.AccountName,
		&created.RequestedModel,
		&created.Model,
		&created.Endpoint,
		&created.Status,
		&created.StartedAt,
		&completedAt,
		&firstResponseMs,
		&totalMs,
		&created.StatusCode,
		&created.Error,
		&created.Reason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAvailabilityTurnNotFound
	}
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		value := completedAt.Time
		created.CompletedAt = &value
	}
	if firstResponseMs.Valid {
		value := firstResponseMs.Int64
		created.FirstResponseMs = &value
	}
	if totalMs.Valid {
		value := totalMs.Int64
		created.TotalMs = &value
	}
	return created, nil
}

func (r *availabilityV2Repository) UpdateAttempt(ctx context.Context, ownerUserID, conversationID, turnID int64, attempt *service.AvailabilityAttempt) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if attempt == nil || attempt.ID <= 0 {
		return errors.New("invalid availability attempt")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE availability_attempts
		SET status = $5,
		    completed_at = $6,
		    first_response_ms = $7,
		    total_ms = $8,
		    status_code = $9,
		    error_message = $10,
		    reason = $11
		WHERE id = $1 AND owner_user_id = $2 AND conversation_id = $3 AND turn_id = $4
	`, attempt.ID, ownerUserID, conversationID, turnID, attempt.Status,
		availabilityTimeArg(attempt.CompletedAt), availabilityInt64Arg(attempt.FirstResponseMs),
		availabilityInt64Arg(attempt.TotalMs), attempt.StatusCode, attempt.Error, attempt.Reason)
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		return service.ErrAvailabilityTurnNotFound
	}
	return nil
}

func (r *availabilityV2Repository) AppendEvent(ctx context.Context, ownerUserID int64, event service.AvailabilityEvent) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if event.Type == "" || event.ConversationID <= 0 || event.TurnID <= 0 || event.Seq <= 0 {
		return errors.New("invalid availability event")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	at := event.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO availability_events
			(owner_user_id, conversation_id, turn_id, seq, event_type, event_at, payload)
		SELECT $1,$2,$3,$4,$5,$6,$7::jsonb
		WHERE EXISTS (
			SELECT 1
			FROM availability_turns t
			JOIN availability_conversations c
			  ON c.id = t.conversation_id AND c.owner_user_id = t.owner_user_id
			WHERE t.id = $3 AND t.conversation_id = $2 AND t.owner_user_id = $1
		)
	`, ownerUserID, event.ConversationID, event.TurnID, event.Seq, event.Type, at, payload)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAvailabilityTurnNotFound
	}
	return nil
}

func (r *availabilityV2Repository) ListEvents(ctx context.Context, ownerUserID, conversationID, turnID int64) ([]service.AvailabilityEvent, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.event_type, e.seq, e.event_at, e.payload
		FROM availability_events e
		JOIN availability_turns t
		  ON t.id = e.turn_id
		 AND t.conversation_id = e.conversation_id
		 AND t.owner_user_id = e.owner_user_id
		JOIN availability_conversations c
		  ON c.id = e.conversation_id AND c.owner_user_id = e.owner_user_id
		WHERE e.owner_user_id = $1 AND e.conversation_id = $2 AND e.turn_id = $3
		ORDER BY e.seq ASC
	`, ownerUserID, conversationID, turnID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]service.AvailabilityEvent, 0)
	for rows.Next() {
		var eventType string
		var seq int64
		var at time.Time
		var payload []byte
		if err := rows.Scan(&eventType, &seq, &at, &payload); err != nil {
			return nil, err
		}
		var event service.AvailabilityEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("decode availability event %d: %w", seq, err)
		}
		event.Type = eventType
		event.Seq = seq
		event.At = at
		event.ConversationID = conversationID
		event.TurnID = turnID
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *availabilityV2Repository) UpdateConversationAt(ctx context.Context, ownerUserID, conversationID int64, at time.Time) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE availability_conversations
		SET updated_at = GREATEST(updated_at, $3)
		WHERE owner_user_id = $1 AND id = $2
	`, ownerUserID, conversationID, at)
	return err
}

func (r *availabilityV2Repository) ListRecentTests(ctx context.Context, ownerUserID, groupID int64, accountIDs []int64) ([]service.AvailabilityLastTest, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	if len(accountIDs) == 0 {
		return []service.AvailabilityLastTest{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (a.account_id, a.model)
		       COALESCE(a.completed_at, a.started_at), a.account_id, a.account_name,
		       a.model, (a.status = 'succeeded'), a.first_response_ms, a.total_ms
		FROM availability_attempts a
		JOIN availability_turns t ON t.id = a.turn_id
		  AND t.conversation_id = a.conversation_id
		  AND t.owner_user_id = a.owner_user_id
		JOIN availability_conversations c ON c.id = a.conversation_id
		  AND c.owner_user_id = a.owner_user_id AND c.group_id = $2
		WHERE a.owner_user_id = $1
		  AND a.account_id = ANY($3)
		  AND a.status IN ('succeeded', 'failed')
		ORDER BY a.account_id, a.model, a.completed_at DESC NULLS LAST, a.id DESC
	`, ownerUserID, groupID, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]service.AvailabilityLastTest, 0)
	for rows.Next() {
		var at time.Time
		var item service.AvailabilityLastTest
		var firstResponseMs, totalMs sql.NullInt64
		if err := rows.Scan(&at, &item.AccountID, &item.AccountName, &item.Model, &item.Success, &firstResponseMs, &totalMs); err != nil {
			return nil, err
		}
		item.At = at.UTC().Format(time.RFC3339)
		if firstResponseMs.Valid {
			value := firstResponseMs.Int64
			item.FirstResponseMs = &value
		}
		if totalMs.Valid {
			value := totalMs.Int64
			item.TotalMs = &value
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func scanAvailabilityConversation(scanner availabilityScanner) (*service.AvailabilityConversation, error) {
	var conversation service.AvailabilityConversation
	var accountID sql.NullInt64
	if err := scanner.Scan(&conversation.ID, &conversation.GroupID, &accountID, &conversation.Model, &conversation.Title, &conversation.CreatedAt, &conversation.UpdatedAt); err != nil {
		return nil, err
	}
	if accountID.Valid {
		value := accountID.Int64
		conversation.AccountID = &value
	}
	return &conversation, nil
}

func scanAvailabilityTurn(scanner availabilityScanner) (*service.AvailabilityTurn, error) {
	var turn service.AvailabilityTurn
	var completedAt sql.NullTime
	var firstResponseMs, totalMs sql.NullInt64
	if err := scanner.Scan(&turn.ID, &turn.ConversationID, &turn.Prompt, &turn.Content, &turn.Status, &turn.CreatedAt, &completedAt, &firstResponseMs, &totalMs, &turn.Error); err != nil {
		return nil, err
	}
	if completedAt.Valid {
		value := completedAt.Time
		turn.CompletedAt = &value
	}
	if firstResponseMs.Valid {
		value := firstResponseMs.Int64
		turn.FirstResponseMs = &value
	}
	if totalMs.Valid {
		value := totalMs.Int64
		turn.TotalMs = &value
	}
	turn.Attempts = []service.AvailabilityAttempt{}
	return &turn, nil
}

func scanAvailabilityAttempt(scanner availabilityScanner) (*service.AvailabilityAttempt, error) {
	var attempt service.AvailabilityAttempt
	var completedAt sql.NullTime
	var firstResponseMs, totalMs sql.NullInt64
	if err := scanner.Scan(
		&attempt.ID,
		&attempt.Index,
		&attempt.AccountID,
		&attempt.AccountName,
		&attempt.RequestedModel,
		&attempt.Model,
		&attempt.Endpoint,
		&attempt.Status,
		&attempt.StartedAt,
		&completedAt,
		&firstResponseMs,
		&totalMs,
		&attempt.StatusCode,
		&attempt.Error,
		&attempt.Reason,
	); err != nil {
		return nil, err
	}
	if completedAt.Valid {
		value := completedAt.Time
		attempt.CompletedAt = &value
	}
	if firstResponseMs.Valid {
		value := firstResponseMs.Int64
		attempt.FirstResponseMs = &value
	}
	if totalMs.Valid {
		value := totalMs.Int64
		attempt.TotalMs = &value
	}
	return &attempt, nil
}

func availabilityInt64Arg(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func availabilityTimeArg(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func availabilityOffset(page, pageSize int) int {
	if page <= 1 || pageSize <= 0 {
		return 0
	}
	if page > int(^uint(0)>>1)/pageSize {
		return int(^uint(0) >> 1)
	}
	return (page - 1) * pageSize
}
