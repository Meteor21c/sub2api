package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const fzyMarketplaceURL = "https://admin-aigc.fzyinghe.com/admin-api/router/aiModel/marketplace/byApiKey"

var fzyVideoMarkup = decimal.RequireFromString("1.15")

// FZYVideoPriceSnapshot fixes the key-specific tariff at task creation. All
// monetary fields are decimal strings in the provider's pricing currency.
// The site's video balance policy currently maps CNY 1 to USD balance 1.
type FZYVideoPriceSnapshot struct {
	Model              string `json:"model"`
	SceneCode          string `json:"scene_code"`
	SceneName          string `json:"scene_name"`
	Currency           string `json:"currency"`
	OfficialPerMillion string `json:"official_per_million"`
	DiscountRate       string `json:"discount_rate"`
	MarkupRate         string `json:"markup_rate"`
	SalePerMillion     string `json:"sale_per_million"`
}

func (p FZYVideoPriceSnapshot) CostForOutputTokens(outputTokens int) (float64, error) {
	if outputTokens <= 0 {
		return 0, errors.New("provider output token count is missing")
	}
	unit, err := decimal.NewFromString(p.SalePerMillion)
	if err != nil || !unit.IsPositive() {
		return 0, errors.New("video sale price is invalid")
	}
	return unit.Mul(decimal.NewFromInt(int64(outputTokens))).Div(decimal.NewFromInt(1000000)).Round(UsageBillingMonetaryScale).InexactFloat64(), nil
}

func (p FZYVideoPriceSnapshot) ProviderCostForOutputTokens(outputTokens int) (float64, error) {
	if outputTokens <= 0 {
		return 0, errors.New("provider output token count is missing")
	}
	official, err := decimal.NewFromString(p.OfficialPerMillion)
	if err != nil || !official.IsPositive() {
		return 0, errors.New("video official price is invalid")
	}
	discount, err := decimal.NewFromString(p.DiscountRate)
	if err != nil || !discount.IsPositive() {
		return 0, errors.New("video provider discount is invalid")
	}
	return official.Mul(discount).Mul(decimal.NewFromInt(int64(outputTokens))).Div(decimal.NewFromInt(1000000)).Round(UsageBillingMonetaryScale).InexactFloat64(), nil
}

func ParseFZYVideoPriceSnapshot(body []byte, model string, requestBody []byte) (FZYVideoPriceSnapshot, error) {
	var empty FZYVideoPriceSnapshot
	var marketplace struct {
		Code int `json:"code"`
		Data []struct {
			InnerCode   string      `json:"innerCode"`
			Discount    json.Number `json:"discount"`
			BillingRule struct {
				DisplayMode     string `json:"displayMode"`
				PricingCurrency string `json:"pricingCurrency"`
				TokenUnitSize   int    `json:"tokenUnitSize"`
				ScenarioRules   []struct {
					SceneCode             string      `json:"sceneCode"`
					SceneName             string      `json:"sceneName"`
					InputMode             string      `json:"inputMode"`
					Resolution            string      `json:"resolution"`
					OutputPricePerMillion json.Number `json:"outputPricePerMillion"`
					PricePerSecond        json.Number `json:"pricePerSecond"`
					PricingCurrency       string      `json:"pricingCurrency"`
					TokenUnitSize         int         `json:"tokenUnitSize"`
				} `json:"scenarioRules"`
			} `json:"billingRule"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &marketplace); err != nil || marketplace.Code != 200 {
		return empty, errors.New("provider marketplace response is invalid")
	}
	for _, entry := range marketplace.Data {
		if entry.InnerCode != model {
			continue
		}
		if entry.BillingRule.DisplayMode != "VIDEO_SCENARIOS" {
			return empty, errors.New("provider video scenario pricing is unavailable")
		}
		discount, err := decimal.NewFromString(entry.Discount.String())
		if err != nil || !discount.IsPositive() {
			return empty, errors.New("provider key discount is unavailable")
		}
		resolution := strings.ToLower(strings.TrimSpace(gjson.GetBytes(requestBody, "resolution").String()))
		if resolution == "" {
			resolution = "720p"
		}
		inputVideo := fzyRequestHasInputVideo(requestBody)
		audio := !gjson.GetBytes(requestBody, "audio").Exists() || gjson.GetBytes(requestBody, "audio").Bool()
		for _, scene := range entry.BillingRule.ScenarioRules {
			matches := false
			if model == "doubao-seedance-1.5-pro" {
				matches = strings.Contains(scene.SceneName, "在线推理") && strings.Contains(scene.SceneName, map[bool]string{true: "有声", false: "无声"}[audio])
			} else {
				mode := map[bool]string{true: "含输入视频", false: "无输入视频"}[inputVideo]
				for _, part := range strings.Split(strings.ToLower(scene.Resolution), "/") {
					if strings.TrimSpace(part) == resolution && scene.InputMode == mode {
						matches = true
						break
					}
				}
			}
			if !matches {
				continue
			}
			if scene.PricePerSecond != "" {
				return empty, errors.New("provider scenario is priced per second, not by token")
			}
			unitSize := scene.TokenUnitSize
			if unitSize == 0 {
				unitSize = entry.BillingRule.TokenUnitSize
			}
			currency := firstNonEmpty(scene.PricingCurrency, entry.BillingRule.PricingCurrency)
			if unitSize != 1000000 || (currency != "CNY" && currency != "USD") {
				return empty, errors.New("provider scenario currency or token unit is unsupported")
			}
			official, err := decimal.NewFromString(scene.OutputPricePerMillion.String())
			if err != nil || !official.IsPositive() {
				return empty, errors.New("provider official token price is invalid")
			}
			return FZYVideoPriceSnapshot{
				Model: model, SceneCode: scene.SceneCode, SceneName: scene.SceneName,
				Currency: currency, OfficialPerMillion: official.String(),
				DiscountRate: discount.String(), MarkupRate: fzyVideoMarkup.String(),
				SalePerMillion: official.Mul(discount).Mul(fzyVideoMarkup).String(),
			}, nil
		}
		return empty, fmt.Errorf("no token-priced provider scenario for %s", model)
	}
	return empty, fmt.Errorf("provider model %s is not in the key-specific marketplace", model)
}

func fzyRequestHasInputVideo(body []byte) bool {
	for _, value := range gjson.GetBytes(body, "reference_images").Array() {
		raw := strings.TrimSpace(value.String())
		if i := strings.Index(raw, ":https://"); i >= 0 {
			raw = raw[i+1:]
		}
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		name := firstNonEmpty(u.Query().Get("name"), u.Query().Get("filename"), u.Path)
		if strings.EqualFold(path.Ext(name), ".mp4") {
			return true
		}
	}
	return false
}

func FetchFZYVideoPriceSnapshot(ctx context.Context, client *http.Client, marketplaceURL, apiKey, model string, requestBody []byte) (FZYVideoPriceSnapshot, error) {
	var empty FZYVideoPriceSnapshot
	if strings.TrimSpace(apiKey) == "" {
		return empty, errors.New("provider API key is missing")
	}
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	if marketplaceURL == "" {
		marketplaceURL = fzyMarketplaceURL
	}
	u, err := url.Parse(marketplaceURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		return empty, errors.New("provider marketplace URL is invalid")
	}
	query := u.Query()
	query.Set("apiKey", apiKey)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty, errors.New("provider marketplace request could not be created")
	}
	resp, err := client.Do(req)
	if err != nil {
		// net/http URL errors include the query string, so never wrap this error.
		return empty, errors.New("provider marketplace request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return empty, fmt.Errorf("provider marketplace returned HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return empty, errors.New("provider marketplace response could not be read")
	}
	return ParseFZYVideoPriceSnapshot(payload, model, requestBody)
}
