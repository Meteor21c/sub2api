//go:build unit

package repository

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFZYCostHistoryQueryIsOwnerScopedAndBounded(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	columns := []string{"model", "scene_code", "currency", "billing_mode", "resolution", "duration_seconds", "actual_duration_seconds", "output_tokens", "actual_amount", "created_at", "price_snapshot"}
	for _, owner := range []int64{42, 84} {
		mock.ExpectQuery(`SELECT model,scene_code,currency,billing_mode,resolution,.*WHERE user_id=\$1 AND model=\$2 AND status='settled'.*INTERVAL '90 days'.*LIMIT \$3`).WithArgs(owner, "doubao-seedance-2.0", 200).WillReturnRows(sqlmock.NewRows(columns).AddRow("doubao-seedance-2.0", "no-video", "CNY", "token", "720p", 10, 9.5, 500000, "12", time.Now(), "{}"))
		rows, err := repo.ListFZYVideoCostSamples(context.Background(), owner, "doubao-seedance-2.0", 9999)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, 9.5, rows[0].ActualSeconds)
		require.Equal(t, "720p", rows[0].Price.Resolution)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFZYReserveRejectsOverCapAndNonFiniteBeforeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db}
	for _, hold := range []float64{5.01, math.NaN(), math.Inf(1), -1, 0} {
		err := repo.ReserveFZYVideo(context.Background(), &service.FZYVideoBillingJob{ID: "cap", HoldAmount: hold})
		require.ErrorIs(t, err, service.ErrFZYVideoBillingUnavailable)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
