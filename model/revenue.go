package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const PurchasePricesOption = "RevenuePurchasePrices"

type PurchasePrice struct {
	ChannelID       int      `json:"channel_id"`
	Model           string   `json:"model"`
	Unit            string   `json:"unit"`
	UnitPrice       *float64 `json:"unit_price,omitempty"`
	InputPrice      *float64 `json:"input_price,omitempty"`
	OutputPrice     *float64 `json:"output_price,omitempty"`
	CachePrice      *float64 `json:"cache_price,omitempty"`
	CacheWritePrice *float64 `json:"cache_write_price,omitempty"`
	EffectiveAt     int64    `json:"effective_at"`
	Source          string   `json:"source"`
}

func ValidatePurchasePrices(rules []PurchasePrice) error {
	if len(rules) > 1000 {
		return errors.New("too many purchase price rules")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if r.ChannelID <= 0 || strings.TrimSpace(r.Model) == "" || strings.TrimSpace(r.Source) == "" || len(r.Model) > 255 || len(r.Source) > 500 || r.EffectiveAt < 0 {
			return errors.New("channel, exact model, effective time and source are required")
		}
		key := fmt.Sprintf("%d/%s/%d", r.ChannelID, r.Model, r.EffectiveAt)
		if seen[key] {
			return errors.New("duplicate purchase price rule")
		}
		seen[key] = true
		for _, v := range []*float64{r.UnitPrice, r.InputPrice, r.OutputPrice, r.CachePrice, r.CacheWritePrice} {
			if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0) || *v > 1e9) {
				return errors.New("purchase prices must be finite, non-negative numbers")
			}
		}
		switch r.Unit {
		case "tokens":
			if r.InputPrice == nil || r.OutputPrice == nil {
				return errors.New("input/output prices per million tokens are required")
			}
		case "request", "image", "second":
			if r.UnitPrice == nil {
				return errors.New("unit price is required")
			}
		default:
			return errors.New("unsupported purchase price unit")
		}
	}
	return nil
}

func GetPurchasePriceRules(ctx context.Context) ([]PurchasePrice, error) {
	var option Option
	if err := DB.WithContext(ctx).Where(&Option{Key: PurchasePricesOption}).Limit(1).Find(&option).Error; err != nil {
		return nil, err
	}
	rules := []PurchasePrice{}
	if option.Value != "" {
		if err := common.UnmarshalJsonStr(option.Value, &rules); err != nil {
			return nil, err
		}
	}
	return rules, ValidatePurchasePrices(rules)
}

type RevenueRow struct {
	ChannelID        int      `json:"channel_id"`
	Model            string   `json:"model"`
	Entries          int64    `json:"entries"`
	Sales            float64  `json:"sales"`
	Refunds          float64  `json:"refunds"`
	NetSales         float64  `json:"net_sales"`
	KnownCost        float64  `json:"known_cost"`
	Cost             *float64 `json:"cost"`
	GrossProfit      *float64 `json:"gross_profit"`
	UncoveredEntries int64    `json:"uncovered_entries"`
	PendingEntries   int64    `json:"pending_entries"`
}
type RevenueReport struct {
	Rows             []RevenueRow `json:"rows"`
	Sales            float64      `json:"sales"`
	Refunds          float64      `json:"refunds"`
	NetSales         float64      `json:"net_sales"`
	KnownCost        float64      `json:"known_cost"`
	GrossProfit      *float64     `json:"gross_profit"`
	UncoveredEntries int64        `json:"uncovered_entries"`
	PendingEntries   int64        `json:"pending_entries"`
	LogEnabled       bool         `json:"log_enabled"`
}

type revenueMetadata struct {
	TaskID           string             `json:"task_id"`
	IsTask           bool               `json:"is_task"`
	Claude           bool               `json:"claude"`
	ImageCount       *float64           `json:"image_count"`
	CacheTokens      float64            `json:"cache_tokens"`
	CacheWriteTokens float64            `json:"cache_creation_tokens"`
	UsageFacts       map[string]float64 `json:"usage_facts"`
}

// CalculateRevenue reports ledger movements, not cash receipts. A refund does
// not prove the provider refunded its cost; incomplete costs remain unknown.
func CalculateRevenue(logs []Log, rules []PurchasePrice, tasks map[string]Task, quotaPerUnit float64) RevenueReport {
	report := RevenueReport{Rows: []RevenueRow{}, LogEnabled: common.LogConsumeEnabled}
	if quotaPerUnit <= 0 || math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) {
		return report
	}
	rows := map[string]*RevenueRow{}
	taskSeen := map[string]bool{}
	for _, l := range logs {
		if l.Type != LogTypeConsume && l.Type != LogTypeRefund {
			continue
		}
		key := fmt.Sprintf("%d/%s", l.ChannelId, l.ModelName)
		row := rows[key]
		if row == nil {
			row = &RevenueRow{ChannelID: l.ChannelId, Model: l.ModelName}
			rows[key] = row
		}
		amount := float64(l.Quota) / quotaPerUnit
		if l.Type == LogTypeRefund {
			row.Refunds += amount
			continue
		}
		row.Sales += amount
		var meta revenueMetadata
		metadataValid := l.Other == "" || common.UnmarshalJsonStr(l.Other, &meta) == nil
		if !metadataValid {
			row.Entries++
			row.UncoveredEntries++
			continue
		}
		// Adjustment logs are monetary deltas, not a second provider request.
		if meta.TaskID != "" && !meta.IsTask {
			continue
		}
		if meta.TaskID != "" {
			taskKey := fmt.Sprintf("%d/%d/%s", l.ChannelId, l.UserId, meta.TaskID)
			if taskSeen[taskKey] {
				continue
			}
			taskSeen[taskKey] = true
			task, ok := tasks[taskKey]
			if !ok || task.Status != TaskStatusSuccess {
				row.Entries++
				row.UncoveredEntries++
				if !ok || task.Status != TaskStatusFailure {
					row.PendingEntries++
				}
				continue
			}
			if bc := task.PrivateData.BillingContext; bc != nil && bc.TieredSnapshot != nil {
				meta.UsageFacts = make(map[string]float64)
				for key, value := range bc.TieredSnapshot.UsageFacts {
					switch v := value.(type) {
					case float64:
						meta.UsageFacts[key] = v
					case int:
						meta.UsageFacts[key] = float64(v)
					case string:
						if parsed, parseErr := strconv.ParseFloat(v, 64); parseErr == nil {
							meta.UsageFacts[key] = parsed
						}
					}
				}
			}
		}
		row.Entries++
		var rule *PurchasePrice
		for i := range rules {
			r := &rules[i]
			if r.ChannelID == l.ChannelId && r.Model == l.ModelName && r.EffectiveAt <= l.CreatedAt && (rule == nil || r.EffectiveAt > rule.EffectiveAt) {
				rule = r
			}
		}
		cost, ok := purchaseCost(l, meta, rule)
		if !ok {
			row.UncoveredEntries++
		} else {
			row.KnownCost += cost
		}
	}
	for _, row := range rows {
		row.NetSales = row.Sales - row.Refunds
		if row.UncoveredEntries == 0 && row.Entries > 0 {
			cost := row.KnownCost
			profit := row.NetSales - cost
			row.Cost = &cost
			row.GrossProfit = &profit
		}
		report.Rows = append(report.Rows, *row)
		report.Sales += row.Sales
		report.Refunds += row.Refunds
		report.KnownCost += row.KnownCost
		report.UncoveredEntries += row.UncoveredEntries
		report.PendingEntries += row.PendingEntries
	}
	report.NetSales = report.Sales - report.Refunds
	if report.UncoveredEntries == 0 {
		profit := report.NetSales - report.KnownCost
		report.GrossProfit = &profit
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].ChannelID != report.Rows[j].ChannelID {
			return report.Rows[i].ChannelID < report.Rows[j].ChannelID
		}
		return report.Rows[i].Model < report.Rows[j].Model
	})
	return report
}

func purchaseCost(l Log, m revenueMetadata, r *PurchasePrice) (float64, bool) {
	if r == nil {
		return 0, false
	}
	var cost float64
	switch r.Unit {
	case "request":
		cost = *r.UnitPrice
	case "image", "second":
		name := "image_count"
		if r.Unit == "second" {
			name = "seconds"
		}
		count, ok := m.UsageFacts[name]
		if !ok && r.Unit == "image" && m.ImageCount != nil {
			count = *m.ImageCount
			ok = true
		}
		if !ok || count <= 0 || math.IsNaN(count) || math.IsInf(count, 0) {
			return 0, false
		}
		cost = count * *r.UnitPrice
	case "tokens":
		input := float64(l.PromptTokens)
		output := float64(l.CompletionTokens)
		if input < 0 || output < 0 || input+output == 0 {
			return 0, false
		}
		if m.CacheTokens > 0 {
			if r.CachePrice == nil {
				return 0, false
			}
			cost += m.CacheTokens * *r.CachePrice / 1e6
			if !m.Claude {
				input -= m.CacheTokens
			}
		}
		if m.CacheWriteTokens > 0 {
			if r.CacheWritePrice == nil {
				return 0, false
			}
			cost += m.CacheWriteTokens * *r.CacheWritePrice / 1e6
			if !m.Claude {
				input -= m.CacheWriteTokens
			}
		}
		if input < 0 || m.CacheTokens < 0 || m.CacheWriteTokens < 0 {
			return 0, false
		}
		cost += (input**r.InputPrice + output**r.OutputPrice) / 1e6
	default:
		return 0, false
	}
	return cost, !math.IsNaN(cost) && !math.IsInf(cost, 0) && cost >= 0
}

func GetRevenue(ctx context.Context, start, end int64) (RevenueReport, error) {
	rules, err := GetPurchasePriceRules(ctx)
	if err != nil {
		return RevenueReport{}, err
	}
	var logs []Log
	err = LOG_DB.WithContext(ctx).Select("created_at,type,quota,prompt_tokens,completion_tokens,channel_id,model_name,user_id,other").Where("created_at >= ? AND created_at <= ? AND type IN ?", start, end, []int{LogTypeConsume, LogTypeRefund}).Order("created_at ASC").Limit(50001).Find(&logs).Error
	if err != nil {
		return RevenueReport{}, err
	}
	if len(logs) > 50000 {
		return RevenueReport{}, errors.New("too many ledger entries; select a shorter time range")
	}
	taskIDs := []string{}
	for _, l := range logs {
		var m revenueMetadata
		if common.UnmarshalJsonStr(l.Other, &m) == nil && m.IsTask && m.TaskID != "" {
			taskIDs = append(taskIDs, m.TaskID)
		}
	}
	tasks := map[string]Task{}
	for offset := 0; offset < len(taskIDs); offset += 500 {
		var batch []Task
		if err := DB.WithContext(ctx).Select("task_id,channel_id,user_id,status,private_data").Where("task_id IN ?", taskIDs[offset:min(offset+500, len(taskIDs))]).Find(&batch).Error; err != nil {
			return RevenueReport{}, err
		}
		for _, t := range batch {
			tasks[fmt.Sprintf("%d/%d/%s", t.ChannelId, t.UserId, t.TaskID)] = t
		}
	}
	return CalculateRevenue(logs, rules, tasks, common.QuotaPerUnit), nil
}
