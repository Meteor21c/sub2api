package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

var businessLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// BusinessMonitorService only reads usage, account metadata and a fixed balance
// endpoint. Its writes are confined to its own monitoring tables.
type BusinessMonitorService struct {
	db            *sql.DB
	ownerID       int64
	snapshotURL   string
	snapshotToken string
	aliases       map[string]string
	client        *http.Client
	mu            sync.Mutex
	lastCollect   time.Time
	lastCleanup   time.Time
	startOnce     sync.Once
	stopOnce      sync.Once
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

func NewBusinessMonitorService(db *sql.DB) *BusinessMonitorService {
	ownerID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("BUSINESS_MONITOR_OWNER_ID")), 10, 64)
	ctx, cancel := context.WithCancel(context.Background())
	s := &BusinessMonitorService{db: db, ownerID: ownerID, snapshotURL: strings.TrimSpace(os.Getenv("BUSINESS_MONITOR_SNAPSHOT_URL")), snapshotToken: strings.TrimSpace(os.Getenv("BUSINESS_MONITOR_SNAPSHOT_TOKEN")), aliases: map[string]string{}, ctx: ctx, cancel: cancel,
		client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	var aliases map[string]string
	if err := json.Unmarshal([]byte(os.Getenv("BUSINESS_MONITOR_ALIASES_JSON")), &aliases); err == nil {
		for from, to := range aliases {
			if a, b := businessNormalizeURL(from), businessNormalizeURL(to); a != "" && b != "" {
				s.aliases[a] = b
			}
		}
	}
	return s
}

func (s *BusinessMonitorService) OwnerID() int64 { return s.ownerID }
func (s *BusinessMonitorService) AccessAllowed(id int64) bool {
	return s.ownerID > 0 && id == s.ownerID
}

func (s *BusinessMonitorService) Start() {
	if s.db == nil || s.ownerID <= 0 || s.snapshotURL == "" {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.cycle()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					s.cycle()
				}
			}
		}()
	})
}

func (s *BusinessMonitorService) Stop() {
	s.stopOnce.Do(s.cancel)
	s.wg.Wait()
}

func (s *BusinessMonitorService) cycle() {
	ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
	defer cancel()
	if err := s.collect(ctx, false); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("business_monitor_balance_collect_failed", "error", err)
	}
	if ctx.Err() == nil {
		if err := s.maintainDaily(ctx); err != nil {
			slog.Warn("business_monitor_daily_failed", "error", err)
		}
		if time.Since(s.lastCleanup) > time.Hour {
			if err := s.cleanupSamples(ctx); err != nil {
				slog.Warn("business_monitor_cleanup_failed", "error", err)
			} else {
				s.lastCleanup = time.Now()
			}
		}
	}
}

func businessNormalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String()
}

func (s *BusinessMonitorService) walletKey(raw string) string {
	key := businessNormalizeURL(raw)
	if target, ok := s.aliases[key]; ok {
		return target
	}
	return key
}

// BusinessMonitorBalanceSnapshot deliberately exposes only the fields needed by
// the owner balance panel. Unknown fields from the monitor never reach a browser.
type BusinessMonitorBalanceSnapshot struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Accounts    []struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		BaseURL   string   `json:"baseUrl"`
		Balance   *float64 `json:"balance"`
		Status    string   `json:"status"`
		Currency  string   `json:"currency"`
		PlanName  string   `json:"planName"`
		Threshold float64  `json:"threshold"`
	} `json:"accounts"`
}

// CurrentBalances uses the same fixed endpoint and service credential as the
// sampler. Authorization for interactive callers is enforced by the owner-only
// handler; the credential stays on this server.
func (s *BusinessMonitorService) CurrentBalances(ctx context.Context) (*BusinessMonitorBalanceSnapshot, error) {
	return s.fetchBalanceSnapshot(ctx)
}

func (s *BusinessMonitorService) fetchBalanceSnapshot(ctx context.Context) (*BusinessMonitorBalanceSnapshot, error) {
	u, err := url.Parse(s.snapshotURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return nil, errors.New("invalid business monitor snapshot endpoint")
	}
	// Fixed operator endpoint; no query credentials and no redirect forwarding.
	host := strings.ToLower(u.Hostname())
	local := host == "127.0.0.1" || host == "::1" || host == "localhost" || host == "meteor-monitor"
	if (!local && (host != "monitor.meteor21c.fun" || u.Scheme != "https" || u.Port() != "" && u.Port() != "443")) || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("business monitor endpoint is not allowlisted")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) // #nosec G704 -- Operator endpoint validated by the host allowlist above.
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if s.snapshotToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.snapshotToken)
	}
	resp, err := s.client.Do(request) // #nosec G704 -- Allowlisted endpoint with redirects disabled.
	if err != nil {
		return nil, errors.New("balance snapshot request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("balance snapshot returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024+1))
	if err != nil || len(body) > 256*1024 {
		return nil, errors.New("balance snapshot response unavailable or too large")
	}
	var snapshot BusinessMonitorBalanceSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || len(snapshot.Accounts) == 0 || len(snapshot.Accounts) > 100 {
		return nil, errors.New("invalid balance snapshot response")
	}
	return &snapshot, nil
}

func (s *BusinessMonitorService) collect(ctx context.Context, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	minimum := 5 * time.Minute
	if force {
		minimum = 20 * time.Second
	}
	if time.Since(s.lastCollect) < minimum {
		return nil
	}
	// A transaction-scoped lock prevents duplicate sampling across replicas.
	lock, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Rollback() }()
	var acquired bool
	if err := lock.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(24720261027)`).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	snapshot, err := s.fetchBalanceSnapshot(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	observed := snapshot.GeneratedAt
	if observed.IsZero() || observed.After(now.Add(time.Minute)) || observed.Before(now.Add(-2*time.Minute)) {
		observed = now
	}
	for _, account := range snapshot.Accounts {
		key := s.walletKey(account.BaseURL)
		if key == "" {
			continue
		}
		balance := account.Balance
		if balance != nil && (math.IsNaN(*balance) || math.IsInf(*balance, 0)) {
			balance = nil
		}
		if account.Status != "ready" && account.Status != "low" {
			balance = nil
		}
		var previous sql.NullFloat64
		var previousAt time.Time
		err := lock.QueryRowContext(ctx, `SELECT balance,observed_at FROM business_monitor_samples WHERE wallet_key=$1 AND balance IS NOT NULL ORDER BY observed_at DESC LIMIT 1`, key).Scan(&previous, &previousAt)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		var id int64
		if err := lock.QueryRowContext(ctx, `INSERT INTO business_monitor_samples(wallet_key,base_url,name,balance,status,observed_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, key, key, account.Name, balance, account.Status, observed).Scan(&id); err != nil {
			return err
		}
		if previous.Valid && balance != nil && observed.After(previousAt) {
			increase := *balance - previous.Float64
			if amount, kind, ok := businessCreditCandidate(increase, strings.EqualFold(account.ID, "viapi"), observed.Sub(previousAt)); ok {
				if _, err := lock.ExecContext(ctx, `INSERT INTO business_monitor_credits(sample_id,wallet_key,amount,observed_increase,kind,observed_at) VALUES($1,$2,$3,$4,$5,$6)`, id, key, amount, increase, kind, observed); err != nil {
					return err
				}
			}
		}
	}
	if err := lock.Commit(); err != nil {
		return err
	}
	s.lastCollect = now
	if err := s.observeRates(ctx); err != nil {
		slog.Warn("business_monitor_rate_observation_failed", "error", err)
	}
	return nil
}

// Positive movements are candidates only. We never use estimated consumption
// to manufacture a recharge that forces the observed cost to match the estimate.
func businessCreditCandidate(increase float64, viapi bool, gap time.Duration) (float64, string, bool) {
	if increase <= 0.000001 || math.IsNaN(increase) || math.IsInf(increase, 0) {
		return 0, "", false
	}
	if viapi && increase <= 0.100001 {
		return increase, "checkin_candidate", true
	}
	if gap <= 15*time.Minute {
		nearest := math.Round(increase)
		if nearest >= 1 && math.Abs(nearest-increase) <= 0.25 {
			return nearest, "integer_recharge_candidate", true
		}
	}
	return increase, "credit_candidate", true
}

type businessAccount struct {
	ID         int64
	Name       string
	Key        string
	RateStatus string
	ProbeRate  *float64
}

func (s *BusinessMonitorService) accountCatalog(ctx context.Context) (map[int64]businessAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,COALESCE(credentials->>'base_url',''),COALESCE(extra->'upstream_billing_probe'->>'status','unknown'),COALESCE(extra->'upstream_billing_probe'->>'fresh_until',''),COALESCE(extra->'upstream_billing_probe'->'data','{}'::jsonb) FROM accounts WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[int64]businessAccount{}
	for rows.Next() {
		var a businessAccount
		var base, fresh string
		var probeJSON []byte
		if err := rows.Scan(&a.ID, &a.Name, &base, &a.RateStatus, &fresh, &probeJSON); err != nil {
			return nil, err
		}
		a.Key = s.walletKey(base)
		if a.Key == "" {
			a.Key = fmt.Sprintf("unmapped:account:%d", a.ID)
		}
		if a.RateStatus == "ok" {
			deadline, parseErr := time.Parse(time.RFC3339Nano, fresh)
			if parseErr != nil || time.Now().After(deadline) {
				a.RateStatus = "stale"
			}
			var data map[string]any
			if a.RateStatus == "ok" && json.Unmarshal(probeJSON, &data) == nil {
				if rate, ok := upstreamBillingRateAt(data, time.Now()); ok {
					a.ProbeRate = &rate
				}
			}
			if a.RateStatus == "ok" && a.ProbeRate == nil {
				a.RateStatus = "invalid"
			}
		}
		result[a.ID] = a
	}
	return result, rows.Err()
}

func (s *BusinessMonitorService) observeRates(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT 'upstream:'||id::text,id,name,'upstream',extra->'upstream_billing_probe'->'data'->>'resolved_rate_multiplier' FROM accounts WHERE deleted_at IS NULL AND extra->'upstream_billing_probe'->>'status'='ok'
		UNION ALL SELECT 'site:'||id::text,id,name,'site',rate_multiplier::text FROM groups WHERE deleted_at IS NULL`)
	if err != nil {
		return err
	}
	type rateState struct {
		key        string
		id         int64
		name, kind string
		rate       float64
	}
	states := []rateState{}
	for rows.Next() {
		var state rateState
		var raw sql.NullString
		if err := rows.Scan(&state.key, &state.id, &state.name, &state.kind, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		value, parseErr := strconv.ParseFloat(raw.String, 64)
		if parseErr == nil && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			state.rate = value
			states = append(states, state)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	for _, state := range states {
		var previous float64
		err := tx.QueryRowContext(ctx, `SELECT rate FROM business_monitor_rate_state WHERE state_key=$1 FOR UPDATE`, state.key).Scan(&previous)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && math.Abs(previous-state.rate) > 0.00000001 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO business_monitor_rate_changes(account_id,account_name,kind,old_rate,new_rate,observed_at) VALUES($1,$2,$3,$4,$5,$6)`, state.id, state.name, state.kind, previous, state.rate, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO business_monitor_rate_state(state_key,account_id,account_name,kind,rate,observed_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(state_key) DO UPDATE SET rate=EXCLUDED.rate,account_name=EXCLUDED.account_name,observed_at=EXCLUDED.observed_at`, state.key, state.id, state.name, state.kind, state.rate, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func businessDay(t time.Time) time.Time {
	t = t.In(businessLocation)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, businessLocation)
}

func businessPeriod(period, date string, now time.Time) (time.Time, time.Time, error) {
	day := businessDay(now)
	if date != "" {
		var err error
		day, err = time.ParseInLocation("2006-01-02", date, businessLocation)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid business report date")
		}
	}
	switch period {
	case "day":
		return day, day.AddDate(0, 0, 1), nil
	case "week":
		offset := (int(day.Weekday()) + 6) % 7
		start := day.AddDate(0, 0, -offset)
		return start, start.AddDate(0, 0, 7), nil
	case "month":
		start := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, businessLocation)
		return start, start.AddDate(0, 1, 0), nil
	case "year":
		start := time.Date(day.Year(), 1, 1, 0, 0, 0, 0, businessLocation)
		return start, start.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, time.Time{}, errors.New("invalid business report period")
	}
}

func businessTolerance(revenue, expected float64) float64 {
	return math.Max(0.2, math.Min(math.Max(expected, 0)*0.03, math.Max(revenue-expected, 0)*0.25))
}

func businessFinalizeWallet(w *BusinessMonitorWallet) {
	w.GrossProfit, w.Tolerance = nil, nil
	if businessOnlyExcludedMedia(w) {
		w.ExpectedCost, w.SubscriptionExpectedCost = nil, nil
		w.Status = "excluded"
	}
	if w.ExpectedCost != nil && w.SubscriptionExpectedCost != nil {
		includedRevenue := w.Revenue - w.ExcludedMediaRevenue
		w.GrossProfit = businessAmount(includedRevenue - *w.ExpectedCost + *w.SubscriptionExpectedCost)
		w.Tolerance = businessAmount(businessTolerance(includedRevenue, *w.ExpectedCost))
	} else {
		w.Partial = true
	}
	if w.Messages == nil {
		w.Messages = []string{}
	}
	if w.AccountIDs == nil {
		w.AccountIDs = []int64{}
	}
	if w.Status == "" {
		w.Status = "ok"
	}
	if w.InferredCredits > 0 && w.CreditOverride == nil {
		w.Partial = true
	}
	if w.SubscriptionUsage > 0 {
		w.Partial = true
	}
	if w.ExcludedMediaRequests > 0 {
		w.Partial = true
		w.Deviation, w.ObservedProfit, w.Tolerance = nil, nil, nil
		w.Messages = append(w.Messages, fmt.Sprintf("待确认绘图/视频已暂不计入：%d次，客户消费%.4f，套餐计量%.4f；收入与成本均不参与预计毛利", w.ExcludedMediaRequests, w.ExcludedMediaRevenue, w.ExcludedMediaSubscriptionUsage))
		w.Messages = append(w.Messages, "钱包余额含已排除的绘图/视频消耗，暂不进行此钱包的余额偏差与余额毛利核验")
	}
	if w.Partial && w.Status == "ok" {
		w.Status = "partial"
	}
	if w.ExpectedCost == nil && !businessOnlyExcludedMedia(w) {
		w.Messages = append(w.Messages, "成本待确认：有用量的渠道缺少有效上游探测倍率，不使用本地记账倍率或默认1")
	}
	if w.GrossProfit != nil && *w.GrossProfit < -0.2 && w.Revenue > 0 {
		w.Status = "loss"
		w.Messages = append(w.Messages, "按上游探测倍率估算毛利为负，建议检查倍率与模型价格")
	}
	if w.BalanceStart == nil || w.BalanceEnd == nil {
		w.Partial = true
		if w.Status == "ok" {
			w.Status = "partial"
		}
		return
	}
	if w.Partial && !w.BalanceComplete {
		// A sampling gap or failed endpoint makes the consumption window
		// incomplete. Do not subtract full-day revenue from a partial balance.
		return
	}
	cost := *w.BalanceStart + w.Credits - *w.BalanceEnd
	w.BalanceCost = &cost
	if w.ExpectedCost != nil && w.ExcludedMediaRequests == 0 {
		w.Deviation = businessAmount(cost - *w.ExpectedCost)
	}
	// A subscription has usage value but no per-request cash realization.
	if w.SubscriptionUsage == 0 && w.ExcludedMediaRequests == 0 {
		profit := w.Revenue - cost
		w.ObservedProfit = &profit
	}
	if !w.Partial && w.SubscriptionUsage == 0 {
		if *w.ObservedProfit < -0.2 {
			w.Status = "loss"
		} else if w.Deviation != nil && w.Tolerance != nil && *w.Deviation > *w.Tolerance {
			w.Status = "overspend"
		}
	}
	if w.InferredCredits > 0 && w.CreditOverride == nil {
		w.Partial = true
		if w.Status == "ok" {
			w.Status = "partial"
		}
		w.Messages = append(w.Messages, "含自动推测的充值或赠送，金额可修正；差额不代表已确认多扣")
	}
	if w.SubscriptionUsage > 0 {
		w.Partial = true
		w.Messages = append(w.Messages, "含订阅套餐用量，未将套餐用量当作现金收入")
	}
}

func businessSortWallets(wallets []BusinessMonitorWallet) {
	sort.Slice(wallets, func(i, j int) bool { return wallets[i].Key < wallets[j].Key })
}

// Retention only touches this feature's own samples and their credit candidates.
// Daily financial summaries and rate changes are never deleted here.
func (s *BusinessMonitorService) cleanupSamples(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM business_monitor_samples WHERE observed_at < $1 ORDER BY observed_at LIMIT 1000 FOR UPDATE SKIP LOCKED`, time.Now().AddDate(0, 0, -90))
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM business_monitor_credits WHERE sample_id=ANY($1::bigint[])`, pq.Array(ids)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM business_monitor_samples WHERE id=ANY($1::bigint[])`, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}
