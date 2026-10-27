//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFZYVideoReserveIsAtomicAndRequiresBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	job := &service.FZYVideoBillingJob{
		ID: "reservation-1", UserID: 42, APIKeyID: 7, AccountID: 114, HoldAmount: 0.2,
		Price: service.FZYVideoPriceSnapshot{Model: "doubao-seedance-2.0-mini", SceneCode: "mini-no-video", SceneName: "无输入视频", Currency: "CNY", OfficialPerMillion: "23", DiscountRate: "0.3", MarkupRate: "1.15", SalePerMillion: "7.935"},
	}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO fzy_video_billing_jobs`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`UPDATE users SET balance=balance-`).WithArgs(0.2, int64(42)).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	require.ErrorIs(t, repo.ReserveFZYVideo(ctx, job), service.ErrFZYVideoInsufficientBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFZYVideoSettlementRefundsDifferenceAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	cmd := &service.UsageBillingCommand{
		RequestID: "grok-video:fzy-task", UserID: 42, APIKeyID: 7, AccountID: 114,
		BalanceCost: 0.1, OutputTokens: 10000, Model: "doubao-seedance-2.0-mini",
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,api_key_id,account_id,hold_amount,status`).
		WithArgs("reservation-1").WillReturnRows(sqlmock.NewRows([]string{"user_id", "api_key_id", "account_id", "hold_amount", "status", "task_id", "billing_mode"}).AddRow(42, 7, 114, 0.2, "pending", "fzy-task", "token"))
	mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT request_fingerprint`).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`UPDATE users SET balance=balance\+`).WithArgs(0.2, 0.1, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(10.1))
	mock.ExpectExec(`UPDATE fzy_video_billing_jobs SET status='settled'`).WithArgs("reservation-1", 0.1, 10000).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := repo.SettleFZYVideo(ctx, "reservation-1", 10000, 0.1, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.InDelta(t, 10.1, *result.NewBalance, 1e-9)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,api_key_id,account_id,hold_amount,status`).
		WithArgs("reservation-1").WillReturnRows(sqlmock.NewRows([]string{"user_id", "api_key_id", "account_id", "hold_amount", "status", "task_id", "billing_mode"}).AddRow(42, 7, 114, 0.2, "settled", "fzy-task", "token"))
	mock.ExpectRollback()
	result, err = repo.SettleFZYVideo(ctx, "reservation-1", 10000, 0.1, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFZYVideoFailedTaskReleasesHoldOnce(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,hold_amount,status`).WithArgs("reservation-2").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "hold_amount", "status"}).AddRow(42, 0.2, "pending"))
	mock.ExpectExec(`UPDATE users SET balance=balance\+`).WithArgs(0.2, int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE fzy_video_billing_jobs SET status='released'`).WithArgs("reservation-2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	released, err := repo.ReleaseFZYVideo(ctx, "reservation-2")
	require.NoError(t, err)
	require.True(t, released)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,hold_amount,status`).WithArgs("reservation-2").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "hold_amount", "status"}).AddRow(42, 0.2, "released"))
	mock.ExpectRollback()
	released, err = repo.ReleaseFZYVideo(ctx, "reservation-2")
	require.NoError(t, err)
	require.False(t, released)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFZYVideoSecondSettlementAcceptsZeroTokensAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	cmd := &service.UsageBillingCommand{RequestID: "grok-video:kling-task", UserID: 42, APIKeyID: 7, AccountID: 114, BalanceCost: 2.2425, Model: "kling-v3-omni"}
	columns := []string{"user_id", "api_key_id", "account_id", "hold_amount", "status", "task_id", "billing_mode"}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,api_key_id,account_id,hold_amount,status`).WithArgs("second-1").WillReturnRows(sqlmock.NewRows(columns).AddRow(42, 7, 114, 2.2425, "pending", "kling-task", "second"))
	mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT request_fingerprint`).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`UPDATE users SET balance=balance\+`).WithArgs(2.2425, 2.2425, int64(42)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(7.7575))
	mock.ExpectExec(`UPDATE fzy_video_billing_jobs SET status='settled'`).WithArgs("second-1", 2.2425, 0).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := repo.SettleFZYVideo(ctx, "second-1", 0, 2.2425, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,api_key_id,account_id,hold_amount,status`).WithArgs("second-1").WillReturnRows(sqlmock.NewRows(columns).AddRow(42, 7, 114, 2.2425, "settled", "kling-task", "second"))
	mock.ExpectRollback()
	result, err = repo.SettleFZYVideo(ctx, "second-1", 0, 2.2425, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFZYVideoTokenSettlementStillRequiresOutputTokens(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id,api_key_id,account_id,hold_amount,status`).WithArgs("token-1").WillReturnRows(sqlmock.NewRows([]string{"user_id", "api_key_id", "account_id", "hold_amount", "status", "task_id", "billing_mode"}).AddRow(42, 7, 114, 0.2, "pending", "mini-task", "token"))
	mock.ExpectRollback()
	_, err = repo.SettleFZYVideo(context.Background(), "token-1", 0, 0.1, &service.UsageBillingCommand{RequestID: "task", UserID: 42, APIKeyID: 7, AccountID: 114})
	require.ErrorIs(t, err, service.ErrFZYVideoBillingUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}
