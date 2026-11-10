package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type modelTestRepository struct{ db *sql.DB }

func NewModelTestRepository(db *sql.DB) service.ModelTestRepository {
	return &modelTestRepository{db: db}
}

const modelTestPlanColumns = `id, label, group_id, model, test_kind, enabled, next_run_at, last_run_at`
const modelTestJobColumns = `id, source, owner_user_id, api_key_id, plan_id, group_id, label, model, test_kind,
 status, verdict, answer, content, error_code, total_ms, input_tokens, output_tokens,
 COALESCE(client_request_id::text, ''), client_ip, created_at, started_at, completed_at, expires_at,
 upstream_account_id, upstream_account_name, upstream_attempts, manual, retry_of::text`

func scanModelTestPlan(s availabilityScanner) (*service.ModelTestPlan, error) {
	p := &service.ModelTestPlan{}
	err := s.Scan(&p.ID, &p.Label, &p.GroupID, &p.Model, &p.TestKind, &p.Enabled, &p.NextRunAt, &p.LastRunAt)
	return p, err
}

func scanModelTestJob(s availabilityScanner) (*service.ModelTestJob, error) {
	j := &service.ModelTestJob{}
	var attempts []byte
	err := s.Scan(&j.ID, &j.Source, &j.OwnerUserID, &j.APIKeyID, &j.PlanID, &j.GroupID, &j.Label,
		&j.Model, &j.TestKind, &j.Status, &j.Verdict, &j.Answer, &j.Content, &j.ErrorCode,
		&j.TotalMs, &j.InputTokens, &j.OutputTokens, &j.ClientRequestID, &j.ClientIP,
		&j.CreatedAt, &j.StartedAt, &j.CompletedAt, &j.ExpiresAt,
		&j.UpstreamAccountID, &j.UpstreamAccountName, &attempts, &j.Manual, &j.RetryOf)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrModelTestNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(attempts, &j.UpstreamAttempts); err != nil {
		return nil, err
	}
	return j, nil
}

func (r *modelTestRepository) ListPlans(ctx context.Context) ([]service.ModelTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+modelTestPlanColumns+` FROM model_test_plans WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ModelTestPlan, 0)
	for rows.Next() {
		p, err := scanModelTestPlan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *p)
	}
	return items, rows.Err()
}

func (r *modelTestRepository) SavePlan(ctx context.Context, id, actor int64, in service.ModelTestPlanInput) (*service.ModelTestPlan, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(725201)`); err != nil {
		return nil, err
	}
	var p *service.ModelTestPlan
	if id == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_test_plans WHERE deleted_at IS NULL`).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 50 {
			return nil, service.ErrModelTestLimit
		}
		p, err = scanModelTestPlan(tx.QueryRowContext(ctx, `INSERT INTO model_test_plans
		 (label, group_id, model, test_kind, enabled, next_run_at, created_by)
		 VALUES ($1,$2,$3,$4,$5,CASE WHEN $5 THEN NOW() ELSE NULL END,$6) RETURNING `+modelTestPlanColumns,
			in.Label, in.GroupID, in.Model, in.TestKind, in.Enabled, actor))
	} else {
		p, err = scanModelTestPlan(tx.QueryRowContext(ctx, `UPDATE model_test_plans SET
		 label=$2, group_id=$3, model=$4, test_kind=$5, enabled=$6,
		 next_run_at=CASE WHEN NOT $6 THEN NULL WHEN NOT enabled THEN NOW() ELSE COALESCE(next_run_at,NOW()) END,
		 updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL RETURNING `+modelTestPlanColumns,
			id, in.Label, in.GroupID, in.Model, in.TestKind, in.Enabled))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrModelTestNotFound
	}
	if err != nil {
		return nil, err
	}
	// Disabling or changing a plan must not launch a queued probe of its old configuration.
	if id != 0 {
		_, err = tx.ExecContext(ctx, `UPDATE model_test_jobs SET status='interrupted', error_code='plan_changed', completed_at=NOW()
		 WHERE plan_id=$1 AND status='queued' AND deleted_at IS NULL`, id)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *modelTestRepository) DeletePlan(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(725201)`); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE model_test_plans SET enabled=FALSE,next_run_at=NULL,deleted_at=NOW(),updated_at=NOW()
	 WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrModelTestNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE model_test_jobs SET status='interrupted',error_code='plan_changed',completed_at=NOW()
	 WHERE plan_id=$1 AND status='queued' AND deleted_at IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *modelTestRepository) EnqueueDue(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(725201)`).Scan(&locked); err != nil || !locked {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_test_jobs WHERE status IN ('queued','running') AND expires_at>NOW() AND deleted_at IS NULL`).Scan(&count); err != nil {
		return err
	}
	if count >= 200 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+modelTestPlanColumns+` FROM model_test_plans
	 WHERE enabled AND deleted_at IS NULL AND next_run_at<=NOW() ORDER BY next_run_at,id LIMIT $1 FOR UPDATE SKIP LOCKED`, 200-count)
	if err != nil {
		return err
	}
	plans := make([]service.ModelTestPlan, 0)
	for rows.Next() {
		p, scanErr := scanModelTestPlan(rows)
		if scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		plans = append(plans, *p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, p := range plans {
		_, err = tx.ExecContext(ctx, `INSERT INTO model_test_jobs
		 (id,source,plan_id,group_id,label,model,test_kind,scheduled_for)
		 VALUES ($1,'scheduled',$2,$3,$4,$5,$6,$7) ON CONFLICT (plan_id,scheduled_for) DO NOTHING`,
			uuid.NewString(), p.ID, p.GroupID, p.Label, p.Model, p.TestKind, p.NextRunAt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE model_test_plans SET last_run_at=NOW(),next_run_at=NOW()+INTERVAL '2 hours' WHERE id=$1`, p.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *modelTestRepository) CreateUserJob(ctx context.Context, j *service.ModelTestJob) (*service.ModelTestJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// The shared lock bounds the total queue even when several app instances submit concurrently.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(725201)`); err != nil {
		return nil, err
	}
	old, err := scanModelTestJob(tx.QueryRowContext(ctx, `SELECT `+modelTestJobColumns+` FROM model_test_jobs
	 WHERE owner_user_id=$1 AND client_request_id=$2 AND expires_at>NOW()`, j.OwnerUserID, j.ClientRequestID))
	if err == nil {
		if old.APIKeyID == nil || j.APIKeyID == nil || *old.APIKeyID != *j.APIKeyID || old.Model != j.Model || old.TestKind != j.TestKind {
			return nil, service.ErrModelTestConflict
		}
		return old, nil
	}
	if !errors.Is(err, service.ErrModelTestNotFound) {
		return nil, err
	}
	var active, recent, total int
	err = tx.QueryRowContext(ctx, `SELECT
	 COUNT(*) FILTER (WHERE owner_user_id=$1 AND status IN ('queued','running')),
	 COUNT(*) FILTER (WHERE owner_user_id=$1 AND created_at>NOW()-INTERVAL '1 hour'),
	 COUNT(*) FILTER (WHERE status IN ('queued','running'))
	 FROM model_test_jobs WHERE expires_at>NOW() AND deleted_at IS NULL`, j.OwnerUserID).Scan(&active, &recent, &total)
	if err != nil {
		return nil, err
	}
	if active >= 2 || recent >= 20 || total >= 200 {
		return nil, service.ErrModelTestLimit
	}
	got, err := scanModelTestJob(tx.QueryRowContext(ctx, `INSERT INTO model_test_jobs
	 (id,source,owner_user_id,api_key_id,group_id,label,model,test_kind,client_request_id,client_ip)
	 VALUES ($1,'user',$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+modelTestJobColumns,
		j.ID, j.OwnerUserID, j.APIKeyID, j.GroupID, j.Label, j.Model, j.TestKind, j.ClientRequestID, j.ClientIP))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return got, nil
}

// Both list and detail enforce source + owner + expiration in SQL, including
// public IDs supplied by clients. Summaries never load generated SVG blobs.
const modelTestVisibility = `expires_at>NOW() AND deleted_at IS NULL AND (
 ($2='user' AND source='user' AND owner_user_id=$1) OR
 ($2='scheduled' AND source='scheduled' AND EXISTS
   (SELECT 1 FROM model_test_plans p WHERE p.id=plan_id AND p.deleted_at IS NULL)))`

func (r *modelTestRepository) ListJobs(ctx context.Context, owner int64, source string) ([]service.ModelTestJob, error) {
	cols := strings.Replace(modelTestJobColumns, "answer, content,", "answer, '' AS content,", 1)
	rows, err := r.db.QueryContext(ctx, `SELECT `+cols+` FROM model_test_jobs WHERE `+modelTestVisibility+` ORDER BY created_at DESC,id DESC LIMIT 1000`, owner, source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ModelTestJob, 0)
	for rows.Next() {
		j, err := scanModelTestJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *j)
	}
	return items, rows.Err()
}

func (r *modelTestRepository) GetJob(ctx context.Context, owner int64, source, id string) (*service.ModelTestJob, error) {
	return scanModelTestJob(r.db.QueryRowContext(ctx, `SELECT `+modelTestJobColumns+` FROM model_test_jobs
	 WHERE `+modelTestVisibility+` AND id=$3`, owner, source, id))
}

const modelTestAdminVisibility = `source='scheduled' AND deleted_at IS NULL AND expires_at>NOW()`

func (r *modelTestRepository) ListAdminJobs(ctx context.Context) ([]service.ModelTestJob, error) {
	cols := strings.Replace(modelTestJobColumns, "answer, content,", "answer, '' AS content,", 1)
	rows, err := r.db.QueryContext(ctx, `SELECT `+cols+` FROM model_test_jobs WHERE `+modelTestAdminVisibility+` ORDER BY created_at DESC,id DESC LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ModelTestJob, 0)
	for rows.Next() {
		j, scanErr := scanModelTestJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *j)
	}
	return items, rows.Err()
}

func (r *modelTestRepository) GetAdminJob(ctx context.Context, id string) (*service.ModelTestJob, error) {
	return scanModelTestJob(r.db.QueryRowContext(ctx, `SELECT `+modelTestJobColumns+` FROM model_test_jobs
	 WHERE `+modelTestAdminVisibility+` AND id=$1`, id))
}

func (r *modelTestRepository) DeleteAdminJob(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Claim, manual enqueue and deletion serialize so a queued delete cannot
	// race into a billable upstream call. Running tasks must finish first.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(725201)`); err != nil {
		return err
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM model_test_jobs WHERE `+modelTestAdminVisibility+` AND id=$1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrModelTestNotFound
	}
	if err != nil {
		return err
	}
	if status == "running" {
		return service.ErrModelTestBusy
	}
	_, err = tx.ExecContext(ctx, `UPDATE model_test_jobs SET deleted_at=NOW(),content='',
	 status=CASE WHEN status='queued' THEN 'interrupted' ELSE status END,
	 error_code=CASE WHEN status='queued' THEN 'interrupted' ELSE error_code END,
	 completed_at=COALESCE(completed_at,NOW()),lease_expires_at=NULL WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CreateManualJob starts a new public probe, never a free replay of a customer's
// paid request. A retry preserves the original parameters, while a plan run
// snapshots its current parameters. Neither operation changes next_run_at.
func (r *modelTestRepository) CreateManualJob(ctx context.Context, actor, planID int64, retryID, requestID string) (*service.ModelTestJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(725201)`); err != nil {
		return nil, err
	}
	// Include tombstones in idempotency lookup: deleting a result must never
	// make a lost-response retry create another upstream charge.
	var deleted bool
	existing, err := scanModelTestJob(tx.QueryRowContext(ctx, `SELECT `+modelTestJobColumns+` FROM model_test_jobs
	 WHERE source='scheduled' AND admin_actor_id=$1 AND admin_request_id=$2`, actor, requestID))
	if err == nil {
		if existing.PlanID == nil || (retryID == "" && (*existing.PlanID != planID || existing.RetryOf != nil)) ||
			(retryID != "" && (existing.RetryOf == nil || *existing.RetryOf != retryID)) {
			return nil, service.ErrModelTestConflict
		}
		if err = tx.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM model_test_jobs WHERE id=$1`, existing.ID).Scan(&deleted); err != nil {
			return nil, err
		}
		if deleted || !existing.ExpiresAt.After(time.Now()) {
			return nil, service.ErrModelTestNotFound
		}
		return existing, nil
	}
	if !errors.Is(err, service.ErrModelTestNotFound) {
		return nil, err
	}
	var original *service.ModelTestJob
	if retryID != "" {
		original, err = scanModelTestJob(tx.QueryRowContext(ctx, `SELECT `+modelTestJobColumns+` FROM model_test_jobs
		 WHERE `+modelTestAdminVisibility+` AND id=$1 FOR UPDATE`, retryID))
		if err != nil {
			return nil, err
		}
		if original.Status == "queued" || original.Status == "running" {
			return nil, service.ErrModelTestBusy
		}
		if original.PlanID == nil {
			return nil, service.ErrModelTestNotFound
		}
		planID = *original.PlanID
	}
	p, err := scanModelTestPlan(tx.QueryRowContext(ctx, `SELECT `+modelTestPlanColumns+` FROM model_test_plans
	 WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrModelTestNotFound
	}
	if err != nil {
		return nil, err
	}
	var active, total int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE plan_id=$1),COUNT(*) FROM model_test_jobs
	 WHERE status IN ('queued','running') AND expires_at>NOW() AND deleted_at IS NULL`, planID).Scan(&active, &total)
	if err != nil {
		return nil, err
	}
	if active != 0 {
		return nil, service.ErrModelTestBusy
	}
	if total >= 200 {
		return nil, service.ErrModelTestLimit
	}
	var retryOf *string
	if original != nil {
		p.GroupID, p.Label, p.Model, p.TestKind = original.GroupID, original.Label, original.Model, original.TestKind
		retryOf = &retryID
	}
	job, err := scanModelTestJob(tx.QueryRowContext(ctx, `INSERT INTO model_test_jobs
	 (id,source,plan_id,group_id,label,model,test_kind,manual,retry_of,admin_actor_id,admin_request_id)
	 VALUES ($1,'scheduled',$2,$3,$4,$5,$6,TRUE,$7,$8,$9) RETURNING `+modelTestJobColumns,
		uuid.NewString(), planID, p.GroupID, p.Label, p.Model, p.TestKind, retryOf, actor, requestID))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *modelTestRepository) Claim(ctx context.Context, worker string) (*service.ModelTestJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(725201)`); err != nil {
		return nil, err
	}
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_test_jobs WHERE status='running' AND lease_expires_at>NOW() AND deleted_at IS NULL`).Scan(&running); err != nil {
		return nil, err
	}
	if running >= 2 {
		return nil, nil
	}
	j, err := scanModelTestJob(tx.QueryRowContext(ctx, `UPDATE model_test_jobs SET status='running',
	 worker_id=$1,started_at=NOW(),lease_expires_at=NOW()+INTERVAL '12 minutes'
	 WHERE id=(SELECT j.id FROM model_test_jobs j WHERE j.status='queued' AND j.expires_at>NOW() AND j.deleted_at IS NULL
	 AND (j.source='user' OR EXISTS (SELECT 1 FROM model_test_plans p WHERE p.id=j.plan_id AND (p.enabled OR j.manual) AND p.deleted_at IS NULL))
	 ORDER BY j.created_at,j.id LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+modelTestJobColumns, worker))
	if errors.Is(err, service.ErrModelTestNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return j, nil
}

func (r *modelTestRepository) Finish(ctx context.Context, worker string, j *service.ModelTestJob) error {
	attempts := j.UpstreamAttempts
	if attempts == nil {
		attempts = []service.ModelTestUpstreamAttempt{}
	}
	raw, err := json.Marshal(attempts)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE model_test_jobs SET status=$3,verdict=$4,answer=$5,content=$6,
	 error_code=$7,total_ms=$8,input_tokens=$9,output_tokens=$10,completed_at=NOW(),lease_expires_at=NULL,
	 upstream_account_id=$11,upstream_account_name=$12,upstream_attempts=$13::jsonb
	 WHERE id=$1 AND worker_id=$2 AND status='running' AND deleted_at IS NULL AND expires_at>NOW()`, j.ID, worker, j.Status, j.Verdict, j.Answer, j.Content,
		j.ErrorCode, j.TotalMs, j.InputTokens, j.OutputTokens, j.UpstreamAccountID, j.UpstreamAccountName, string(raw))
	return err
}

func (r *modelTestRepository) Cleanup(ctx context.Context) error {
	// An interrupted inference is never requeued: its upstream charge may already exist.
	if _, err := r.db.ExecContext(ctx, `UPDATE model_test_jobs SET status='interrupted',error_code='interrupted',completed_at=NOW(),lease_expires_at=NULL
	 WHERE status='running' AND lease_expires_at<NOW()`); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM model_test_jobs WHERE id IN
	 (SELECT id FROM model_test_jobs WHERE expires_at<=NOW() ORDER BY expires_at LIMIT 1000)`); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM model_test_plans p WHERE deleted_at<NOW()-INTERVAL '24 hours'
	 AND NOT EXISTS (SELECT 1 FROM model_test_jobs j WHERE j.plan_id=p.id)`)
	return err
}
