package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

const businessCostVersion = 3

func businessAmount(v float64) *float64 { return &v }

// A missing component makes a full-period estimate unknown, not a zero cost.
func businessAddComplete(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	return businessAmount(*a + *b)
}

func businessOnlyExcludedMedia(w *BusinessMonitorWallet) bool {
	return w.ExcludedMediaRequests > 0 && w.ExcludedMediaRequests == w.RequestCount
}

func businessApplyProbeCost(w *BusinessMonitorWallet, a businessAccount, usage BusinessMonitorCostBasis) {
	rate := a.ProbeRate
	if a.RateStatus != "ok" || rate == nil || *rate < 0 || math.IsNaN(*rate) || math.IsInf(*rate, 0) {
		rate = nil
	}
	usage.AccountID, usage.AccountName, usage.ProbeRate, usage.RateStatus = a.ID, a.Name, rate, a.RateStatus
	usage.Excluded = rate == nil && (usage.UsageKind == "image" || usage.UsageKind == "video")
	w.CostBasis = append(w.CostBasis, usage)
	if usage.Excluded {
		w.ExcludedMediaRevenue += usage.Revenue
		w.ExcludedMediaSubscriptionUsage += usage.SubscriptionUsage
		w.ExcludedMediaRequests += usage.RequestCount
		return
	}
	if rate == nil {
		if usage.StandardCost != 0 {
			w.ExpectedCost = nil
		}
		if usage.SubscriptionStandardCost != 0 {
			w.SubscriptionExpectedCost = nil
		}
		return
	}
	w.ExpectedCost = businessAddComplete(w.ExpectedCost, businessAmount(usage.StandardCost**rate))
	w.SubscriptionExpectedCost = businessAddComplete(w.SubscriptionExpectedCost, businessAmount(usage.SubscriptionStandardCost**rate))
}

func businessResetCost(w *BusinessMonitorWallet) {
	w.ExpectedCost, w.SubscriptionExpectedCost, w.GrossProfit, w.Tolerance, w.Deviation = nil, nil, nil, nil, nil
	w.CostBasis = []BusinessMonitorCostBasis{}
	w.ExcludedMediaRevenue, w.ExcludedMediaSubscriptionUsage, w.ExcludedMediaRequests = 0, 0, 0
	w.Status = ""
	if strings.HasPrefix(w.Key, "unmapped:") {
		w.Status = "uncovered"
	}
	w.Partial = !w.BalanceComplete
	messages := []string{}
	for _, m := range w.Messages {
		costMessage := false
		for _, text := range []string{"本日记账平均倍率", "本站配置估算毛利", "上游倍率为手工配置", "当前上游声明倍率", "当前上游探测倍率", "成本待确认", "按上游探测倍率估算毛利", "旧日报成本", "待确认绘图/视频", "钱包余额含已排除"} {
			if strings.Contains(m, text) {
				costMessage = true
				break
			}
		}
		if !costMessage {
			messages = append(messages, m)
		}
	}
	w.Messages = messages
}

// Hide v1's incorrect costs immediately, including while the bounded background
// migration is still working through older days. Revenue is never changed.
func businessInvalidateLegacyCost(w *BusinessMonitorWallet) {
	// v2 known costs already have full probe coverage, so none of their media
	// usage needs excluding. Keep these usable while older days migrate.
	if w.CostVersion == 2 && w.ExpectedCost != nil {
		w.CostVersion = businessCostVersion
		return
	}
	businessResetCost(w)
	w.Messages = append(w.Messages, "旧日报成本正在按上游探测倍率修正；客户消费额保持不变")
	businessFinalizeWallet(w)
}

func businessRepairCost(saved *BusinessMonitorWallet, recalculated *BusinessMonitorWallet) {
	businessResetCost(saved)
	saved.CostVersion = businessCostVersion
	// Matching totals/counts guard against expired or partially retained raw
	// logs. In that case preserve all saved income but do not invent a cost.
	if recalculated != nil && saved.RequestCount == recalculated.RequestCount &&
		math.Abs(saved.Revenue-recalculated.Revenue) < 0.000001 &&
		math.Abs(saved.SubscriptionUsage-recalculated.SubscriptionUsage) < 0.000001 {
		saved.ExpectedCost = recalculated.ExpectedCost
		saved.SubscriptionExpectedCost = recalculated.SubscriptionExpectedCost
		saved.CostBasis = recalculated.CostBasis
		saved.RateStatus = recalculated.RateStatus
		saved.ExcludedMediaRevenue = recalculated.ExcludedMediaRevenue
		saved.ExcludedMediaSubscriptionUsage = recalculated.ExcludedMediaSubscriptionUsage
		saved.ExcludedMediaRequests = recalculated.ExcludedMediaRequests
		saved.Messages = append(saved.Messages, "历史日报已按当前有效上游探测倍率重估；非当时精确结算倍率，客户消费额未改变")
	} else {
		saved.RateStatus = "unknown"
		saved.Messages = append(saved.Messages, "旧日报成本无法重估：原始用量已过期或不完整；已保留原客户消费额")
	}
	businessFinalizeWallet(saved)
}

func (s *BusinessMonitorService) repairLegacyCostDay(ctx context.Context) (bool, error) {
	var date string
	err := s.db.QueryRowContext(ctx, `SELECT day::text FROM business_monitor_daily WHERE COALESCE((payload->>'cost_version')::int,0)<$1 ORDER BY day DESC LIMIT 1`, businessCostVersion).Scan(&date)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	day, err := time.ParseInLocation("2006-01-02", date, businessLocation)
	if err != nil {
		return false, fmt.Errorf("invalid saved business date: %w", err)
	}
	recalculated, err := s.buildDay(ctx, day, day.AddDate(0, 0, 1))
	if err != nil {
		return false, err
	}
	byKey := map[string]*BusinessMonitorWallet{}
	for i := range recalculated {
		byKey[recalculated[i].Key] = &recalculated[i]
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT wallet_key,payload FROM business_monitor_daily WHERE day=$1 AND COALESCE((payload->>'cost_version')::int,0)<$2 FOR UPDATE`, date, businessCostVersion)
	if err != nil {
		return false, err
	}
	wallets := []BusinessMonitorWallet{}
	for rows.Next() {
		var key string
		var payload []byte
		if err := rows.Scan(&key, &payload); err != nil {
			_ = rows.Close()
			return false, err
		}
		var w BusinessMonitorWallet
		if err := json.Unmarshal(payload, &w); err != nil {
			_ = rows.Close()
			return false, err
		}
		businessRepairCost(&w, byKey[key])
		wallets = append(wallets, w)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return false, err
	}
	for _, w := range wallets {
		payload, err := json.Marshal(w)
		if err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE business_monitor_daily SET payload=$3,computed_at=NOW() WHERE day=$1 AND wallet_key=$2`, date, w.Key, string(payload)); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
