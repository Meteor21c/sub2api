//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Real PostgreSQL acceptance of the queue locks, owner predicates, leases,
// retention and plan cadence. No inference or wallet mutation is involved.
func TestModelTestsRepositoryDurabilityAndPrivacy(t *testing.T) {
	ctx := context.Background()
	owner := mustCreateUser(t, integrationEntClient, &service.User{Balance: 50})
	other := mustCreateUser(t, integrationEntClient, &service.User{})
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "benchmark-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	key, err := integrationEntClient.APIKey.Create().SetUserID(owner.ID).SetGroupID(group.ID).SetKey("benchmark-" + uuid.NewString()).SetName("test key").SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	repo := NewModelTestRepository(integrationDB)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM model_test_jobs WHERE group_id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM model_test_plans WHERE group_id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM api_keys WHERE id=$1`, key.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, owner.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, other.ID)
	})
	makeJob := func() *service.ModelTestJob {
		return &service.ModelTestJob{ID: uuid.NewString(), Source: "user", OwnerUserID: &owner.ID, APIKeyID: &key.ID, GroupID: group.ID,
			Label: "Pro", Model: "test-model", TestKind: "candy", ClientRequestID: uuid.NewString(), ClientIP: "198.51.100.20"}
	}
	first := makeJob()
	got, err := repo.CreateUserJob(ctx, first)
	require.NoError(t, err)
	require.WithinDuration(t, got.CreatedAt.Add(24*time.Hour), got.ExpiresAt, time.Second)
	_, err = repo.GetJob(ctx, other.ID, "user", got.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	_, err = repo.GetJob(ctx, owner.ID, "scheduled", got.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	// Racing retries with the same browser submission must share one paid job.
	var wg sync.WaitGroup
	ids := make(chan string, 6)
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *first
			copy.ID = uuid.NewString()
			j, e := repo.CreateUserJob(ctx, &copy)
			errs <- e
			if j != nil {
				ids <- j.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	for id := range ids {
		require.Equal(t, first.ID, id)
	}
	changed := *first
	changed.Model = "other-model"
	_, err = repo.CreateUserJob(ctx, &changed)
	require.ErrorIs(t, err, service.ErrModelTestConflict)
	second, err := repo.CreateUserJob(ctx, makeJob())
	require.NoError(t, err)
	_, err = repo.CreateUserJob(ctx, makeJob())
	require.ErrorIs(t, err, service.ErrModelTestLimit)
	worker := uuid.NewString()
	claimed, err := repo.Claim(ctx, worker)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	claimed2, err := repo.Claim(ctx, worker)
	require.NoError(t, err)
	require.NotNil(t, claimed2)
	third, err := repo.Claim(ctx, uuid.NewString())
	require.NoError(t, err)
	require.Nil(t, third)
	claimed.Status = "succeeded"
	claimed.Content = "private response\nFINAL_ANSWER: 21"
	claimed.Verdict = "correct"
	require.NoError(t, repo.Finish(ctx, uuid.NewString(), claimed))
	unchanged, err := repo.GetJob(ctx, owner.ID, "user", claimed.ID)
	require.NoError(t, err)
	require.Equal(t, "running", unchanged.Status)
	require.NoError(t, repo.Finish(ctx, worker, claimed))
	list, err := repo.ListJobs(ctx, owner.ID, "user")
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, j := range list {
		require.Empty(t, j.Content)
	}
	detail, err := repo.GetJob(ctx, owner.ID, "user", claimed.ID)
	require.NoError(t, err)
	require.Equal(t, claimed.Content, detail.Content)
	public, err := json.Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(public), "198.51.100.20")
	require.NotContains(t, string(public), "owner_user_id")
	// Expiry is enforced before the physical cleanup tick runs.
	_, err = integrationDB.ExecContext(ctx, `UPDATE model_test_jobs SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, claimed.ID)
	require.NoError(t, err)
	_, err = repo.GetJob(ctx, owner.ID, "user", claimed.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	_, err = integrationDB.ExecContext(ctx, `UPDATE model_test_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, claimed2.ID)
	require.NoError(t, err)
	require.NoError(t, repo.Cleanup(ctx))
	interrupted, err := repo.GetJob(ctx, owner.ID, "user", claimed2.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", interrupted.Status)
	next, err := repo.Claim(ctx, uuid.NewString())
	require.NoError(t, err)
	require.Nil(t, next, "uncertain running requests must not be replayed")
	// A fresh repository instance can pick up a queued job after an app restart.
	restartJob, err := repo.CreateUserJob(ctx, makeJob())
	require.NoError(t, err)
	fresh := NewModelTestRepository(integrationDB)
	restarted, err := fresh.Claim(ctx, worker)
	require.NoError(t, err)
	require.Equal(t, restartJob.ID, restarted.ID)
	restarted.Status = "interrupted"
	restarted.ErrorCode = "interrupted"
	require.NoError(t, fresh.Finish(ctx, worker, restarted))
	_ = second
	// Parallel scheduler ticks create one occurrence and advance by two hours.
	plan, err := repo.SavePlan(ctx, 0, owner.ID, service.ModelTestPlanInput{Label: "Plus", GroupID: group.ID, Model: "test-model", TestKind: "pelican", Enabled: true})
	require.NoError(t, err)
	errs = make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.EnqueueDue(ctx) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_test_jobs WHERE plan_id=$1`, plan.ID).Scan(&n))
	require.Equal(t, 1, n)
	plans, err := repo.ListPlans(ctx)
	require.NoError(t, err)
	for _, p := range plans {
		if p.ID == plan.ID {
			require.WithinDuration(t, time.Now().Add(2*time.Hour), *p.NextRunAt, 10*time.Second)
		}
	}
	paused, err := repo.SavePlan(ctx, plan.ID, owner.ID, service.ModelTestPlanInput{Label: plan.Label, GroupID: group.ID, Model: plan.Model, TestKind: plan.TestKind, Enabled: false})
	require.NoError(t, err)
	require.Nil(t, paused.NextRunAt)
	visible, err := repo.ListJobs(ctx, other.ID, "scheduled")
	require.NoError(t, err)
	var autoID string
	for _, j := range visible {
		if j.PlanID != nil && *j.PlanID == plan.ID {
			autoID = j.ID
			require.Equal(t, "interrupted", j.Status)
		}
	}
	require.NotEmpty(t, autoID)
	require.NoError(t, repo.DeletePlan(ctx, plan.ID))
	_, err = repo.GetJob(ctx, owner.ID, "scheduled", autoID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, owner.ID).Scan(&balance))
	require.Equal(t, 50.0, balance)
}

func TestModelTestsAdministratorResultsAndManualRuns(t *testing.T) {
	ctx := context.Background()
	admin := mustCreateUser(t, integrationEntClient, &service.User{Balance: 50})
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "admin-benchmark-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	repo := NewModelTestRepository(integrationDB)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM model_test_jobs WHERE group_id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM model_test_plans WHERE group_id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, admin.ID)
	})
	plan, err := repo.SavePlan(ctx, 0, admin.ID, service.ModelTestPlanInput{Label: "Pro", GroupID: group.ID, Model: "test-model", TestKind: "candy", Enabled: false})
	require.NoError(t, err)
	requestID := uuid.NewString()
	first, err := repo.CreateManualJob(ctx, admin.ID, plan.ID, "", requestID)
	require.NoError(t, err)
	require.True(t, first.Manual)
	require.Equal(t, "scheduled", first.Source)
	require.Nil(t, first.OwnerUserID)
	duplicate, err := repo.CreateManualJob(ctx, admin.ID, plan.ID, "", requestID)
	require.NoError(t, err)
	require.Equal(t, first.ID, duplicate.ID)
	_, err = repo.CreateManualJob(ctx, admin.ID, plan.ID, "", uuid.NewString())
	require.ErrorIs(t, err, service.ErrModelTestBusy)
	_, err = repo.CreateManualJob(ctx, admin.ID, 0, first.ID, uuid.NewString())
	require.ErrorIs(t, err, service.ErrModelTestBusy)
	worker := uuid.NewString()
	claimed, err := repo.Claim(ctx, worker)
	require.NoError(t, err)
	require.NotNil(t, claimed, "paused plans must permit an explicitly requested manual run")
	require.Equal(t, first.ID, claimed.ID)
	require.ErrorIs(t, repo.DeleteAdminJob(ctx, first.ID), service.ErrModelTestBusy)
	upstream := int64(777)
	claimed.Status, claimed.Content, claimed.Verdict = "succeeded", "FINAL_ANSWER: 21", "correct"
	claimed.UpstreamAccountID, claimed.UpstreamAccountName = &upstream, "private-upstream"
	claimed.UpstreamAttempts = []service.ModelTestUpstreamAttempt{
		{AccountID: 776, AccountName: "private-first", StatusCode: 503},
		{AccountID: 777, AccountName: "private-upstream", StatusCode: 200, Success: true},
	}
	require.NoError(t, repo.Finish(ctx, worker, claimed))
	adminDetail, err := repo.GetAdminJob(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, claimed.UpstreamAccountID, adminDetail.UpstreamAccountID)
	require.Equal(t, claimed.UpstreamAttempts, adminDetail.UpstreamAttempts)
	adminList, err := repo.ListAdminJobs(ctx)
	require.NoError(t, err)
	for _, j := range adminList {
		require.Empty(t, j.Content)
	}
	public, err := repo.GetJob(ctx, admin.ID, "scheduled", first.ID)
	require.NoError(t, err)
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-")
	require.NotContains(t, string(raw), "upstream_")
	manualRequest := uuid.NewString()
	retried, err := repo.CreateManualJob(ctx, admin.ID, 0, first.ID, manualRequest)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, retried.ID)
	require.Equal(t, first.ID, *retried.RetryOf)
	require.Equal(t, first.GroupID, retried.GroupID)
	require.Equal(t, first.Model, retried.Model)
	require.WithinDuration(t, retried.CreatedAt.Add(24*time.Hour), retried.ExpiresAt, time.Second)
	_, err = repo.GetJob(ctx, admin.ID, "scheduled", first.ID)
	require.NoError(t, err, "retesting must preserve the original result")
	require.NoError(t, repo.DeleteAdminJob(ctx, retried.ID))
	_, err = repo.GetJob(ctx, admin.ID, "scheduled", retried.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	_, err = repo.GetAdminJob(ctx, retried.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	_, err = repo.CreateManualJob(ctx, admin.ID, 0, first.ID, manualRequest)
	require.ErrorIs(t, err, service.ErrModelTestNotFound, "a lost HTTP response must not recreate a deleted manual task")
	next, err := repo.Claim(ctx, uuid.NewString())
	require.NoError(t, err)
	require.Nil(t, next, "deleted queued tasks must never reach inference")
	// A stale completion cannot restore hidden output or make a record visible.
	retried.Status, retried.Content = "succeeded", "must remain deleted"
	require.NoError(t, repo.Finish(ctx, worker, retried))
	var savedContent string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT content FROM model_test_jobs WHERE id=$1`, retried.ID).Scan(&savedContent))
	require.Empty(t, savedContent)
	_, err = repo.GetJob(ctx, admin.ID, "scheduled", retried.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	require.NoError(t, repo.DeleteAdminJob(ctx, first.ID))
	publicJobs, err := repo.ListJobs(ctx, admin.ID, "scheduled")
	require.NoError(t, err)
	for _, j := range publicJobs {
		require.NotEqual(t, first.ID, j.ID)
		require.NotEqual(t, retried.ID, j.ID)
	}
	// Client-owned paid jobs are not included in any administrator replay path.
	privateJob, err := repo.CreateUserJob(ctx, &service.ModelTestJob{ID: uuid.NewString(), OwnerUserID: &admin.ID, GroupID: group.ID,
		Label: "private", Model: "test-model", TestKind: "candy", ClientRequestID: uuid.NewString(), ClientIP: "198.51.100.20"})
	require.NoError(t, err)
	_, err = repo.GetAdminJob(ctx, privateJob.ID)
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	require.ErrorIs(t, repo.DeleteAdminJob(ctx, privateJob.ID), service.ErrModelTestNotFound)
	_, err = repo.CreateManualJob(ctx, admin.ID, 0, privateJob.ID, uuid.NewString())
	require.ErrorIs(t, err, service.ErrModelTestNotFound)
	var nextRun, lastRun *time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT next_run_at,last_run_at FROM model_test_plans WHERE id=$1`, plan.ID).Scan(&nextRun, &lastRun))
	require.Nil(t, nextRun)
	require.Nil(t, lastRun, "manual probes must not move the automatic cadence")
	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, admin.ID).Scan(&balance))
	require.Equal(t, 50.0, balance)
}
