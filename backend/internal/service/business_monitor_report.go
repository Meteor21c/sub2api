package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func (s *BusinessMonitorService) Refresh(ctx context.Context) (*BusinessMonitorReport, error) {
	if err := s.collect(ctx, true); err != nil {
		return nil, err
	}
	return s.Report(ctx, "day", "")
}

func (s *BusinessMonitorService) SetCreditOverride(ctx context.Context, walletKey, date string, amount *float64, note string) error {
	day, err := time.ParseInLocation("2006-01-02", date, businessLocation)
	if err != nil || day.After(businessDay(time.Now())) {
		return errors.New("invalid credit override date")
	}
	key := s.walletKey(walletKey)
	if key == "" || len(note) > 500 {
		return errors.New("invalid credit override")
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM business_monitor_samples WHERE wallet_key=$1 UNION ALL SELECT 1 FROM business_monitor_daily WHERE wallet_key=$1)`, key).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("unknown wallet")
	}
	if amount == nil {
		_, err = s.db.ExecContext(ctx, `DELETE FROM business_monitor_credit_overrides WHERE wallet_key=$1 AND day=$2`, key, date)
	} else {
		if math.IsNaN(*amount) || math.IsInf(*amount, 0) || math.Abs(*amount) > 1000000000 {
			return errors.New("invalid credit amount")
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO business_monitor_credit_overrides(wallet_key,day,amount,note) VALUES($1,$2,$3,$4) ON CONFLICT(wallet_key,day) DO UPDATE SET amount=EXCLUDED.amount,note=EXCLUDED.note,updated_at=NOW()`, key, date, *amount, strings.TrimSpace(note))
	}
	if err != nil {
		return err
	}
	// Correct the saved wallet without re-reading expiring raw usage logs.
	// This preserves historical daily revenue after the raw-log retention ends.
	if day.Before(businessDay(time.Now())) {
		var payload []byte
		err := s.db.QueryRowContext(ctx, `SELECT payload FROM business_monitor_daily WHERE day=$1 AND wallet_key=$2`, date, key).Scan(&payload)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		var w BusinessMonitorWallet
		if err = json.Unmarshal(payload, &w); err != nil {
			return err
		}
		w.CreditOverride = amount
		w.Credits = w.InferredCredits
		w.CreditNote = strings.TrimSpace(note)
		if amount != nil {
			w.Credits = *amount
		}
		w.BalanceCost = nil
		w.ObservedProfit = nil
		w.Deviation = nil
		w.Status = ""
		w.Partial = !w.BalanceComplete
		messages := []string{}
		for _, m := range w.Messages {
			if !strings.Contains(m, "含自动推测") && !strings.Contains(m, "本站配置估算毛利为负") && !strings.Contains(m, "含订阅套餐") {
				messages = append(messages, m)
			}
		}
		w.Messages = messages
		businessFinalizeWallet(&w)
		updated, err := json.Marshal(w)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `UPDATE business_monitor_daily SET payload=$3,computed_at=NOW() WHERE day=$1 AND wallet_key=$2`, date, key, string(updated))
		return err
	}
	return nil
}

func (s *BusinessMonitorService) buildDay(ctx context.Context, day, until time.Time) ([]BusinessMonitorWallet, error) {
	catalog, err := s.accountCatalog(ctx)
	if err != nil {
		return nil, err
	}
	wallets := map[string]*BusinessMonitorWallet{}
	get := func(key, name string) *BusinessMonitorWallet {
		if w, ok := wallets[key]; ok {
			return w
		}
		base := key
		if strings.HasPrefix(base, "unmapped:") {
			base = ""
		}
		w := &BusinessMonitorWallet{Key: key, BaseURL: base, Name: name, AccountIDs: []int64{}, Messages: []string{}, RateStatus: "unknown"}
		wallets[key] = w
		return w
	}
	// Latest metadata is bounded by the number of wallets, not request volume.
	meta, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON(wallet_key) wallet_key,name FROM business_monitor_samples ORDER BY wallet_key,observed_at DESC`)
	if err != nil {
		return nil, err
	}
	for meta.Next() {
		var key, name string
		if err := meta.Scan(&key, &name); err != nil {
			_ = meta.Close()
			return nil, err
		}
		get(key, name)
	}
	err = meta.Err()
	_ = meta.Close()
	if err != nil {
		return nil, err
	}
	// Each live calculation reads at most one day, under a database-side limit.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT account_id,
		COALESCE(SUM(actual_cost) FILTER(WHERE COALESCE(billing_type,0)<>1),0),
		COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=1),0),
		COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0),
		COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)) FILTER(WHERE billing_type=1),0),
		COUNT(*),COALESCE(SUM(total_cost) FILTER(WHERE COALESCE(billing_type,0)<>1),0),
		COALESCE(SUM(COALESCE(account_stats_cost,total_cost)) FILTER(WHERE COALESCE(billing_type,0)<>1),0)
		FROM usage_logs WHERE created_at >= $1 AND created_at < $2 GROUP BY account_id`, day, until)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var accountID, requests int64
		var revenue, subscription, expected, subscriptionCost, standard, accountStandard float64
		if err = rows.Scan(&accountID, &revenue, &subscription, &expected, &subscriptionCost, &requests, &standard, &accountStandard); err != nil {
			_ = rows.Close()
			return nil, err
		}
		a, ok := catalog[accountID]
		if !ok {
			a = businessAccount{ID: accountID, Name: fmt.Sprintf("历史账号 #%d", accountID), Key: fmt.Sprintf("unmapped:account:%d", accountID), RateStatus: "unknown"}
		}
		w := get(a.Key, a.Name)
		w.AccountIDs = append(w.AccountIDs, accountID)
		w.Revenue += revenue
		w.SubscriptionUsage += subscription
		w.ExpectedCost += expected
		w.SubscriptionExpectedCost += subscriptionCost
		w.RequestCount += requests
		if w.RateStatus == "unknown" || a.RateStatus != "ok" {
			w.RateStatus = a.RateStatus
		}
		if strings.HasPrefix(a.Key, "unmapped:") {
			w.Status = "uncovered"
			w.Partial = true
			w.Messages = append(w.Messages, "渠道没有可匹配的 base_url 或余额接口")
		}
		if day.Equal(businessDay(time.Now())) && a.ProbeRate != nil && a.RateStatus == "ok" && standard > 0 && subscription == 0 {
			// Only compare pure multipliers when the local standard pricing bases
			// agree. Independent channel/model prices cannot be compared this way.
			if math.Abs(standard-accountStandard) <= math.Max(0.000001, standard*0.001) {
				userRate := revenue / standard
				if *a.ProbeRate > userRate+0.000001 {
					w.Messages = append(w.Messages, fmt.Sprintf("%s 当前上游声明倍率 %.4f 高于本日有效售价倍率 %.4f，疑似倍率倒挂", a.Name, *a.ProbeRate, userRate))
					w.Status = "loss"
				}
			}
			if accountStandard > 0 {
				recorded := (expected - subscriptionCost) / accountStandard
				if math.Abs(recorded-*a.ProbeRate) > 0.0001 {
					w.Messages = append(w.Messages, fmt.Sprintf("%s 最新上游声明倍率 %.4f 与本日记账平均倍率 %.4f 不同；请结合倍率变动记录判断", a.Name, *a.ProbeRate, recorded))
				}
			}
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// Include inactive channels belonging to a sampled wallet for transparency.
	for _, a := range catalog {
		if w, ok := wallets[a.Key]; ok {
			found := false
			for _, id := range w.AccountIDs {
				if id == a.ID {
					found = true
					break
				}
			}
			if !found {
				w.AccountIDs = append(w.AccountIDs, a.ID)
			}
		}
	}
	for _, w := range wallets {
		if err := s.applyBalances(ctx, w, day, until); err != nil {
			return nil, err
		}
		businessFinalizeWallet(w)
		sort.Slice(w.AccountIDs, func(i, j int) bool { return w.AccountIDs[i] < w.AccountIDs[j] })
	}
	result := make([]BusinessMonitorWallet, 0, len(wallets))
	for _, w := range wallets {
		result = append(result, *w)
	}
	businessSortWallets(result)
	return result, nil
}

func (s *BusinessMonitorService) boundarySample(ctx context.Context, key string, target time.Time) (*float64, *time.Time, error) {
	var balance float64
	var at time.Time
	err := s.db.QueryRowContext(ctx, `SELECT balance,observed_at FROM business_monitor_samples WHERE wallet_key=$1 AND balance IS NOT NULL AND observed_at >= $2 AND observed_at <= $3 ORDER BY ABS(EXTRACT(EPOCH FROM (observed_at-$4::timestamptz))) LIMIT 1`, key, target.Add(-8*time.Minute), target.Add(8*time.Minute), target).Scan(&balance, &at)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &balance, &at, nil
}

func (s *BusinessMonitorService) applyBalances(ctx context.Context, w *BusinessMonitorWallet, day, until time.Time) error {
	var err error
	w.BalanceStart, w.StartSampleAt, err = s.boundarySample(ctx, w.Key, day)
	if err != nil {
		return err
	}
	w.BalanceEnd, w.LastSampleAt, err = s.boundarySample(ctx, w.Key, until)
	if err != nil {
		return err
	}
	// A service first started just after midnight has no prior-day baseline,
	// even if its first point happens to fall in the boundary tolerance window.
	if w.StartSampleAt != nil && w.StartSampleAt.After(day) {
		var existed bool
		if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM business_monitor_samples WHERE wallet_key=$1 AND balance IS NOT NULL AND observed_at <= $2)`, w.Key, day).Scan(&existed); err != nil {
			return err
		}
		if !existed {
			w.BalanceStart = nil
			w.StartSampleAt = nil
		}
	}
	if w.BalanceStart == nil || w.BalanceEnd == nil {
		w.Partial = true
		w.Messages = append(w.Messages, "缺少日界余额基线，余额消耗暂不可计算")
	}
	var inferred float64
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM business_monitor_credits WHERE wallet_key=$1 AND observed_at >= $2 AND observed_at < $3`, w.Key, day, until).Scan(&inferred); err != nil {
		return err
	}
	w.InferredCredits = inferred
	w.Credits = inferred
	var override float64
	var note string
	err = s.db.QueryRowContext(ctx, `SELECT amount,note FROM business_monitor_credit_overrides WHERE wallet_key=$1 AND day=$2`, w.Key, day.Format("2006-01-02")).Scan(&override, &note)
	if err == nil {
		w.CreditOverride = &override
		w.Credits = override
		w.CreditNote = note
	} else if err != sql.ErrNoRows {
		return err
	}
	if w.StartSampleAt != nil && w.LastSampleAt != nil {
		var largestGap float64
		err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(EXTRACT(EPOCH FROM observed_at-prev)),0) FROM (SELECT observed_at,LAG(observed_at) OVER(ORDER BY observed_at) AS prev FROM business_monitor_samples WHERE wallet_key=$1 AND balance IS NOT NULL AND observed_at >= $2 AND observed_at <= $3) x`, w.Key, *w.StartSampleAt, *w.LastSampleAt).Scan(&largestGap)
		if err != nil {
			return err
		}
		if largestGap > 15*60 {
			w.Partial = true
			w.Messages = append(w.Messages, "余额采样存在超过15分钟的缺口，可能遗漏充值")
		}
		var latestStatus string
		err = s.db.QueryRowContext(ctx, `SELECT status FROM business_monitor_samples WHERE wallet_key=$1 AND observed_at <= $2 ORDER BY observed_at DESC LIMIT 1`, w.Key, until.Add(time.Minute)).Scan(&latestStatus)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if latestStatus != "ready" && latestStatus != "low" {
			w.Partial = true
			w.Messages = append(w.Messages, "最近一次上游余额查询未成功")
		}
	}
	w.BalanceComplete = w.BalanceStart != nil && w.BalanceEnd != nil && !w.Partial
	if w.RateStatus != "ok" {
		w.Messages = append(w.Messages, "上游倍率为手工配置、过期或不支持探测；成本仍按本地历史快照估算")
	}
	return nil
}

func (s *BusinessMonitorService) saveDay(ctx context.Context, day time.Time) error {
	wallets, err := s.buildDay(ctx, day, day.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, w := range wallets {
		payload, err := json.Marshal(w)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO business_monitor_daily(day,wallet_key,payload) VALUES($1,$2,$3) ON CONFLICT(day,wallet_key) DO UPDATE SET payload=EXCLUDED.payload,computed_at=NOW()`, day.Format("2006-01-02"), w.Key, string(payload)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO business_monitor_state(state_key,value) VALUES($1,'complete') ON CONFLICT(state_key) DO UPDATE SET value=EXCLUDED.value`, "day:"+day.Format("2006-01-02"))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *BusinessMonitorService) maintainDaily(ctx context.Context) error {
	today := businessDay(time.Now())
	yesterday := today.AddDate(0, 0, -1)
	var done bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM business_monitor_state WHERE state_key=$1)`, "day:"+yesterday.Format("2006-01-02")).Scan(&done); err != nil {
		return err
	}
	// Wait briefly after midnight for in-flight local usage writes to settle.
	if !done && time.Since(today) > 2*time.Minute {
		if err := s.saveDay(ctx, yesterday); err != nil {
			return err
		}
	}
	// Backfill a single historical day per minute. No yearly raw-log scan.
	var cursorText string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM business_monitor_state WHERE state_key='backfill_cursor'`).Scan(&cursorText)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	cursor := yesterday.AddDate(0, 0, -1)
	if cursorText != "" {
		if parsed, e := time.ParseInLocation("2006-01-02", cursorText, businessLocation); e == nil {
			cursor = parsed
		}
	}
	var earliest sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(created_at) FROM usage_logs`).Scan(&earliest); err != nil {
		return err
	}
	if earliest.Valid && !cursor.Before(businessDay(earliest.Time)) {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM business_monitor_state WHERE state_key=$1)`, "day:"+cursor.Format("2006-01-02")).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err := s.saveDay(ctx, cursor); err != nil {
				return err
			}
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO business_monitor_state(state_key,value) VALUES('backfill_cursor',$1) ON CONFLICT(state_key) DO UPDATE SET value=EXCLUDED.value`, cursor.AddDate(0, 0, -1).Format("2006-01-02"))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *BusinessMonitorService) Report(ctx context.Context, period, date string) (*BusinessMonitorReport, error) {
	now := time.Now()
	start, end, err := businessPeriod(period, date, now)
	if err != nil {
		return nil, err
	}
	today := businessDay(now)
	report := &BusinessMonitorReport{Period: period, Date: start.Format("2006-01-02"), Start: start, End: end, AsOf: now, Finalized: !end.After(today), Wallets: []BusinessMonitorWallet{}, Daily: []BusinessMonitorDaily{}, RateChanges: []BusinessMonitorRateChange{}, Credits: []BusinessMonitorCredit{}}
	var earliest sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(created_at) FROM usage_logs`).Scan(&earliest); err != nil {
		return nil, err
	}
	if earliest.Valid {
		coverage := earliest.Time
		report.CoverageStart = &coverage
	}
	byDay := map[string][]BusinessMonitorWallet{}
	rows, err := s.db.QueryContext(ctx, `SELECT day::text,payload FROM business_monitor_daily WHERE day >= $1 AND day < $2 ORDER BY day,wallet_key`, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var payload []byte
		if err := rows.Scan(&day, &payload); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var w BusinessMonitorWallet
		if err := json.Unmarshal(payload, &w); err != nil {
			_ = rows.Close()
			return nil, err
		}
		byDay[day] = append(byDay[day], w)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if !today.Before(start) && today.Before(end) {
		wallets, err := s.buildDay(ctx, today, now)
		if err != nil {
			return nil, err
		}
		byDay[today.Format("2006-01-02")] = wallets
	}
	walletSums := map[string]*BusinessMonitorWallet{}
	complete := true
	for day := start; day.Before(end) && !day.After(today); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		wallets, ok := byDay[key]
		if !ok {
			complete = false
			continue
		}
		summary := businessSummarize(wallets)
		report.Daily = append(report.Daily, BusinessMonitorDaily{Date: key, Revenue: summary.Revenue, SubscriptionUsage: summary.SubscriptionUsage, ExpectedCost: summary.ExpectedCost, GrossProfit: summary.GrossProfit, BalanceCost: summary.BalanceCost, ObservedProfit: summary.ObservedProfit, Partial: summary.Partial})
		for _, w := range wallets {
			if existing, ok := walletSums[w.Key]; ok {
				businessMergeWallet(existing, w)
			} else {
				copy := w
				walletSums[w.Key] = &copy
			}
		}
	}
	for _, w := range walletSums {
		report.Wallets = append(report.Wallets, *w)
	}
	businessSortWallets(report.Wallets)
	report.Summary = businessSummarize(report.Wallets)
	if !complete || start.After(today) {
		report.Summary.Partial = true
		report.Finalized = false
	}
	limitEnd := end
	if now.Before(limitEnd) {
		limitEnd = now
	}
	changes, err := s.db.QueryContext(ctx, `SELECT id,account_id,account_name,kind,old_rate,new_rate,observed_at FROM business_monitor_rate_changes WHERE observed_at >= $1 AND observed_at < $2 ORDER BY observed_at DESC LIMIT 300`, start, limitEnd)
	if err != nil {
		return nil, err
	}
	for changes.Next() {
		var change BusinessMonitorRateChange
		if err := changes.Scan(&change.ID, &change.AccountID, &change.AccountName, &change.Kind, &change.OldRate, &change.NewRate, &change.ObservedAt); err != nil {
			_ = changes.Close()
			return nil, err
		}
		report.RateChanges = append(report.RateChanges, change)
	}
	err = changes.Err()
	_ = changes.Close()
	if err != nil {
		return nil, err
	}
	credits, err := s.db.QueryContext(ctx, `SELECT c.id,c.wallet_key,c.amount,c.observed_increase,c.kind,c.observed_at,EXISTS(SELECT 1 FROM business_monitor_credit_overrides o WHERE o.wallet_key=c.wallet_key AND o.day=(c.observed_at AT TIME ZONE 'Asia/Shanghai')::date) FROM business_monitor_credits c WHERE c.observed_at >= $1 AND c.observed_at < $2 ORDER BY c.observed_at DESC LIMIT 300`, start, limitEnd)
	if err != nil {
		return nil, err
	}
	for credits.Next() {
		var credit BusinessMonitorCredit
		if err := credits.Scan(&credit.ID, &credit.WalletKey, &credit.Amount, &credit.ObservedIncrease, &credit.Kind, &credit.ObservedAt, &credit.Confirmed); err != nil {
			_ = credits.Close()
			return nil, err
		}
		report.Credits = append(report.Credits, credit)
	}
	err = credits.Err()
	_ = credits.Close()
	return report, err
}

func businessSummarize(wallets []BusinessMonitorWallet) BusinessMonitorSummary {
	s := BusinessMonitorSummary{}
	balance, observed, deviation := 0.0, 0.0, 0.0
	knownBalance, knownProfit := 0, 0
	for _, w := range wallets {
		s.Revenue += w.Revenue
		s.SubscriptionUsage += w.SubscriptionUsage
		s.ExpectedCost += w.ExpectedCost
		s.GrossProfit += w.GrossProfit
		if w.BalanceCost != nil {
			balance += *w.BalanceCost
			knownBalance++
		}
		if w.ObservedProfit != nil {
			observed += *w.ObservedProfit
			knownProfit++
		}
		if w.Deviation != nil {
			deviation += *w.Deviation
		}
		if w.Partial {
			s.Partial = true
		}
		if w.Status == "loss" || w.Status == "overspend" {
			s.AnomalyCount++
		}
	}
	if knownBalance > 0 {
		s.BalanceCost = &balance
		s.Deviation = &deviation
	}
	if knownBalance < len(wallets) || len(wallets) == 0 {
		s.Partial = true
	}
	if knownProfit > 0 {
		s.ObservedProfit = &observed
	}
	return s
}

func businessMergeWallet(to *BusinessMonitorWallet, from BusinessMonitorWallet) {
	to.Revenue += from.Revenue
	to.SubscriptionUsage += from.SubscriptionUsage
	to.SubscriptionExpectedCost += from.SubscriptionExpectedCost
	to.ExpectedCost += from.ExpectedCost
	to.GrossProfit += from.GrossProfit
	to.RequestCount += from.RequestCount
	to.Credits += from.Credits
	to.InferredCredits += from.InferredCredits
	to.Tolerance += from.Tolerance
	to.BalanceEnd = from.BalanceEnd
	to.LastSampleAt = from.LastSampleAt
	// Preserve sums for covered days without pretending missing days cost zero.
	// Partial remains true and the UI explicitly labels these as covered totals.
	addKnown := func(a, b *float64) *float64 {
		if b == nil {
			return a
		}
		v := *b
		if a != nil {
			v += *a
		}
		return &v
	}
	to.BalanceCost = addKnown(to.BalanceCost, from.BalanceCost)
	to.ObservedProfit = addKnown(to.ObservedProfit, from.ObservedProfit)
	to.Deviation = addKnown(to.Deviation, from.Deviation)
	to.Partial = to.Partial || from.Partial
	to.BalanceComplete = to.BalanceComplete && from.BalanceComplete
	if to.CreditOverride != nil && from.CreditOverride != nil {
		v := *to.CreditOverride + *from.CreditOverride
		to.CreditOverride = &v
	} else {
		to.CreditOverride = nil
	}
	if from.Status == "loss" || from.Status == "overspend" || to.Status == "ok" {
		to.Status = from.Status
	}
	for _, message := range from.Messages {
		seen := false
		for _, existing := range to.Messages {
			if existing == message {
				seen = true
				break
			}
		}
		if !seen && len(to.Messages) < 20 {
			to.Messages = append(to.Messages, message)
		}
	}
}
