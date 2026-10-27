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
const fzyPricingSource = "fzyinghe-marketplace-by-api-key"

var fzyVideoMarkup = decimal.RequireFromString("1.15")

// FZYVideoPriceSnapshot freezes the selected account's tariff before creation.
// The site's explicitly chosen currency policy maps CNY 1 to USD balance 1.
// Empty BillingMode remains token for reservations created before this change.
type FZYVideoPriceSnapshot struct {
	Model              string `json:"model"`
	SceneCode          string `json:"scene_code"`
	SceneName          string `json:"scene_name"`
	Currency           string `json:"currency"`
	BillingMode        string `json:"billing_mode,omitempty"`
	OfficialPerMillion string `json:"official_per_million"`
	DiscountRate       string `json:"discount_rate"`
	MarkupRate         string `json:"markup_rate"`
	SalePerMillion     string `json:"sale_per_million"`
	OfficialPerSecond  string `json:"official_per_second,omitempty"`
	SalePerSecond      string `json:"sale_per_second,omitempty"`
	Resolution         string `json:"resolution,omitempty"`
}

func (p FZYVideoPriceSnapshot) IsPerSecond() bool { return p.BillingMode == "second" }
func (p FZYVideoPriceSnapshot) priceUnits(outputTokens, durationSeconds int) (decimal.Decimal, decimal.Decimal, error) {
	officialValue, saleValue := p.OfficialPerMillion, p.SalePerMillion
	units := decimal.NewFromInt(int64(outputTokens)).Div(decimal.NewFromInt(1000000))
	if p.IsPerSecond() {
		if durationSeconds <= 0 || durationSeconds > 30 {
			return decimal.Zero, decimal.Zero, errors.New("provider video duration is missing or unsupported")
		}
		officialValue, saleValue = p.OfficialPerSecond, p.SalePerSecond
		units = decimal.NewFromInt(int64(durationSeconds))
	} else if outputTokens <= 0 {
		return decimal.Zero, decimal.Zero, errors.New("provider output token count is missing")
	}
	official, e1 := decimal.NewFromString(officialValue)
	sale, e2 := decimal.NewFromString(saleValue)
	if e1 != nil || e2 != nil || !official.IsPositive() || !sale.IsPositive() {
		return decimal.Zero, decimal.Zero, errors.New("video frozen price is invalid")
	}
	return official.Mul(units), sale.Mul(units), nil
}
func (p FZYVideoPriceSnapshot) CostForOutputTokens(outputTokens int) (float64, error) {
	return p.CostForUsage(outputTokens, 0)
}
func (p FZYVideoPriceSnapshot) CostForUsage(outputTokens, durationSeconds int) (float64, error) {
	_, sale, err := p.priceUnits(outputTokens, durationSeconds)
	return sale.Round(UsageBillingMonetaryScale).InexactFloat64(), err
}
func (p FZYVideoPriceSnapshot) ProviderCostForOutputTokens(outputTokens int) (float64, error) {
	return p.ProviderCostForUsage(outputTokens, 0)
}
func (p FZYVideoPriceSnapshot) ProviderCostForUsage(outputTokens, durationSeconds int) (float64, error) {
	official, _, err := p.priceUnits(outputTokens, durationSeconds)
	if err != nil {
		return 0, err
	}
	discount, err := decimal.NewFromString(p.DiscountRate)
	if err != nil || !discount.IsPositive() {
		return 0, errors.New("video provider discount is invalid")
	}
	return official.Mul(discount).Round(UsageBillingMonetaryScale).InexactFloat64(), nil
}

type fzyMarketplaceScenario struct {
	SceneCode             string            `json:"sceneCode"`
	SceneName             string            `json:"sceneName"`
	InputMode             string            `json:"inputMode"`
	Resolution            string            `json:"resolution"`
	OutputPricePerMillion json.Number       `json:"outputPricePerMillion"`
	InputPricePerMillion  json.Number       `json:"inputPricePerMillion"`
	MinPrice              json.Number       `json:"minPrice"`
	Surcharges            []json.RawMessage `json:"surcharges"`
	PricePerSecond        json.Number       `json:"pricePerSecond"`
	PricingCurrency       string            `json:"pricingCurrency"`
	TokenUnitSize         int               `json:"tokenUnitSize"`
}
type fzyMarketplaceEntry struct {
	InnerCode   string      `json:"innerCode"`
	Discount    json.Number `json:"discount"`
	BillingRule struct {
		DisplayMode     string                   `json:"displayMode"`
		PricingCurrency string                   `json:"pricingCurrency"`
		TokenUnitSize   int                      `json:"tokenUnitSize"`
		ScenarioRules   []fzyMarketplaceScenario `json:"scenarioRules"`
	} `json:"billingRule"`
}

func fzyMarketplaceModel(body []byte, model string) (*fzyMarketplaceEntry, error) {
	var marketplace struct {
		Code int                   `json:"code"`
		Data []fzyMarketplaceEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &marketplace); err != nil || marketplace.Code != 200 {
		return nil, errors.New("provider marketplace response is invalid")
	}
	for _, entry := range marketplace.Data {
		if entry.InnerCode == model {
			if entry.BillingRule.DisplayMode != "VIDEO_SCENARIOS" {
				return nil, errors.New("provider video scenario pricing is unavailable")
			}
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("provider model %s is not in the key-specific marketplace", model)
}
func fzyScenarioResolutions(value string) []string {
	var values []string
	for _, part := range strings.Split(strings.ToLower(value), "/") {
		part = strings.TrimSpace(part)
		if part == "4k" {
			values = append(values, "4K")
		} else if part == "480p" || part == "720p" || part == "1080p" {
			values = append(values, part)
		}
	}
	return values
}
func fzyDocumentedResolutions(model string) []string {
	switch model {
	case "doubao-seedance-1.5-pro", "doubao-seedance-2.5":
		return []string{"480p", "720p", "1080p"}
	case "doubao-seedance-2.0":
		return []string{"480p", "720p", "1080p", "4K"}
	case "doubao-seedance-2.0-fast", "doubao-seedance-2.0-mini":
		return []string{"480p", "720p"}
	case "kling-v3", "kling-v3-omni":
		return []string{"720p", "1080p", "4K"}
	}
	return nil
}
func fzySupportedSceneResolutions(model, raw string) []string {
	prices := fzyScenarioResolutions(raw)
	documented := fzyDocumentedResolutions(model)
	if model == "doubao-seedance-1.5-pro" && len(prices) == 0 {
		return documented
	}
	allowed := []string{}
	for _, p := range prices {
		for _, d := range documented {
			if p == d {
				allowed = append(allowed, p)
				break
			}
		}
	}
	return allowed
}
func fzySceneValidInputs(model string, scene fzyMarketplaceScenario) bool {
	if model == "kling-v3-omni" {
		if !strings.Contains(scene.InputMode, "无参考视频") && !strings.Contains(scene.InputMode, "有参考视频") {
			return false
		}
		// The task API's documented restriction takes precedence over tariff rows.
		if strings.Contains(scene.InputMode, "有参考视频") && strings.Contains(scene.SceneName, "有声") {
			return false
		}
	}
	if model == "kling-v3" || model == "kling-v3-omni" || model == "doubao-seedance-1.5-pro" {
		return strings.Contains(scene.SceneName, "有声") || strings.Contains(scene.SceneName, "无声")
	}
	return scene.InputMode == "含输入视频" || scene.InputMode == "无输入视频"
}
func fzyScenarioInputs(model string, scene fzyMarketplaceScenario) (inputVideo, audio *bool) {
	boolPtr := func(v bool) *bool { return &v }
	if model == "kling-v3" || model == "kling-v3-omni" || model == "doubao-seedance-1.5-pro" {
		audio = boolPtr(strings.Contains(scene.SceneName, "有声"))
	}
	if model == "kling-v3-omni" {
		inputVideo = boolPtr(strings.Contains(scene.InputMode, "有参考视频"))
	} else if model != "kling-v3" && model != "doubao-seedance-1.5-pro" {
		inputVideo = boolPtr(scene.InputMode == "含输入视频")
	}
	return
}
func fzyScenarioSnapshot(entry *fzyMarketplaceEntry, scene fzyMarketplaceScenario) (FZYVideoPriceSnapshot, error) {
	var empty FZYVideoPriceSnapshot
	if !fzySceneValidInputs(entry.InnerCode, scene) {
		return empty, errors.New("provider video scenario inputs are unsupported")
	}
	discount, err := decimal.NewFromString(entry.Discount.String())
	if err != nil || !discount.IsPositive() {
		return empty, errors.New("provider key discount is unavailable")
	}
	if scene.InputPricePerMillion != "" || scene.MinPrice != "" || len(scene.Surcharges) != 0 {
		return empty, errors.New("provider scenario has additional charges that are not supported")
	}
	currency := firstNonEmpty(scene.PricingCurrency, entry.BillingRule.PricingCurrency)
	if currency != "CNY" && currency != "USD" {
		return empty, errors.New("provider scenario currency is unsupported")
	}
	p := FZYVideoPriceSnapshot{Model: entry.InnerCode, SceneCode: scene.SceneCode, SceneName: scene.SceneName, Currency: currency, DiscountRate: discount.String(), MarkupRate: fzyVideoMarkup.String(), BillingMode: "token"}
	if scene.PricePerSecond != "" {
		if entry.InnerCode != "kling-v3" && entry.InnerCode != "kling-v3-omni" {
			return empty, errors.New("provider model has an unsupported per-second tariff")
		}
		official, err := decimal.NewFromString(scene.PricePerSecond.String())
		if err != nil || !official.IsPositive() {
			return empty, errors.New("provider official per-second price is invalid")
		}
		p.BillingMode = "second"
		p.OfficialPerMillion, p.SalePerMillion = "0", "0"
		p.OfficialPerSecond, p.SalePerSecond = official.String(), official.Mul(discount).Mul(fzyVideoMarkup).String()
		return p, nil
	}
	if entry.InnerCode == "kling-v3" || entry.InnerCode == "kling-v3-omni" {
		return empty, errors.New("provider per-second scenario is unavailable")
	}
	unitSize := scene.TokenUnitSize
	if unitSize == 0 {
		unitSize = entry.BillingRule.TokenUnitSize
	}
	if unitSize != 1000000 {
		return empty, errors.New("provider token unit is unsupported")
	}
	official, err := decimal.NewFromString(scene.OutputPricePerMillion.String())
	if err != nil || !official.IsPositive() {
		return empty, errors.New("provider official token price is invalid")
	}
	p.OfficialPerMillion, p.SalePerMillion = official.String(), official.Mul(discount).Mul(fzyVideoMarkup).String()
	return p, nil
}
func ParseFZYVideoPriceSnapshot(body []byte, model string, requestBody []byte) (FZYVideoPriceSnapshot, error) {
	var empty FZYVideoPriceSnapshot
	entry, err := fzyMarketplaceModel(body, model)
	if err != nil {
		return empty, err
	}
	resolution := strings.ToLower(strings.TrimSpace(gjson.GetBytes(requestBody, "resolution").String()))
	if resolution == "" {
		resolution = "720p"
		if model == "kling-v3" || model == "kling-v3-omni" {
			resolution = "1080p"
		}
	}
	audioValue := gjson.GetBytes(requestBody, "audio")
	audio := audioValue.Bool()
	if !audioValue.Exists() {
		audio = model != "kling-v3" && model != "kling-v3-omni"
	}
	inputVideo := fzyRequestHasInputVideo(requestBody)
	if model == "kling-v3-omni" && inputVideo && audio {
		return empty, errors.New("Kling Omni reference video does not support audio generation")
	}
	for _, scene := range entry.BillingRule.ScenarioRules {
		if model == "doubao-seedance-1.5-pro" {
			if !strings.Contains(scene.SceneName, "在线推理") {
				continue
			}
			validResolution := false
			for _, r := range fzySupportedSceneResolutions(model, scene.Resolution) {
				if strings.EqualFold(r, resolution) {
					validResolution = true
				}
			}
			if !validResolution {
				continue
			}
		} else {
			matches := false
			for _, part := range fzySupportedSceneResolutions(model, scene.Resolution) {
				if strings.EqualFold(part, resolution) {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
		}
		hasVideo, hasAudio := fzyScenarioInputs(model, scene)
		if hasVideo != nil && *hasVideo != inputVideo || hasAudio != nil && *hasAudio != audio {
			continue
		}
		price, err := fzyScenarioSnapshot(entry, scene)
		if err != nil {
			return empty, err
		}
		price.Resolution = resolution
		if resolution == "4k" {
			price.Resolution = "4K"
		}
		return price, nil
	}
	return empty, fmt.Errorf("no provider-priced scenario for %s", model)
}

// ParseFZYVideoCatalog publishes only sanitized, supported billing scenarios.
// A marketplace entry is a price catalog, so fields absent upstream are never invented.
func ParseFZYVideoCatalog(body []byte, model string) (map[string]any, error) {
	entry, err := fzyMarketplaceModel(body, model)
	if err != nil {
		return nil, err
	}
	var scenarios []map[string]any
	resolutions := map[string]bool{}
	modes := map[string]bool{}
	for _, scene := range entry.BillingRule.ScenarioRules {
		if model == "doubao-seedance-1.5-pro" && !strings.Contains(scene.SceneName, "在线推理") {
			continue
		}
		price, err := fzyScenarioSnapshot(entry, scene)
		if err != nil {
			continue
		}
		sceneResolutions := fzySupportedSceneResolutions(model, scene.Resolution)
		if len(sceneResolutions) == 0 {
			continue
		}
		for _, resolution := range sceneResolutions {
			resolutions[resolution] = true
		}
		modes[price.BillingMode] = true
		inputVideo, audio := fzyScenarioInputs(model, scene)
		row := map[string]any{"scene_code": price.SceneCode, "scene_name": price.SceneName, "resolutions": sceneResolutions, "billing_mode": price.BillingMode, "currency": price.Currency, "input_video": inputVideo, "audio": audio, "provider_discount_rate": price.DiscountRate, "markup_rate": price.MarkupRate}
		official, _ := decimal.NewFromString(price.OfficialPerMillion)
		if price.IsPerSecond() {
			official, _ = decimal.NewFromString(price.OfficialPerSecond)
			row["official_per_second"] = price.OfficialPerSecond
			row["upstream_per_second"] = official.Mul(decimal.RequireFromString(price.DiscountRate)).String()
			row["sale_per_second"] = price.SalePerSecond
		} else {
			row["official_per_million"] = price.OfficialPerMillion
			row["upstream_per_million"] = official.Mul(decimal.RequireFromString(price.DiscountRate)).String()
			row["sale_per_million"] = price.SalePerMillion
		}
		row["sale_rate_to_official"] = decimal.RequireFromString(price.DiscountRate).Mul(fzyVideoMarkup).String()
		scenarios = append(scenarios, row)
	}
	if len(scenarios) == 0 {
		return nil, errors.New("provider video scenarios are unsupported")
	}
	ordered := []string{}
	for _, resolution := range []string{"480p", "720p", "1080p", "4K"} {
		if resolutions[resolution] {
			ordered = append(ordered, resolution)
		}
	}
	billingModes := []string{}
	for _, mode := range []string{"token", "second"} {
		if modes[mode] {
			billingModes = append(billingModes, mode)
		}
	}
	return map[string]any{"source": fzyPricingSource, "fetched_at": time.Now().UTC().Format(time.RFC3339), "model": model, "resolutions": ordered, "billing_modes": billingModes, "scenarios": scenarios}, nil
}
func fzyRequestHasInputVideo(body []byte) bool {
	// Match the bridge's supported aliases and explicit media prefixes. An
	// opaque signed URL needs its typed prefix because it has no extension.
	for _, item := range gjson.GetBytes(body, "content").Array() {
		if item.Get("type").String() == "video_url" && fzyReferenceContainsVideo(item.Get("video_url"), "video") {
			return true
		}
	}
	if fzyReferenceContainsVideo(gjson.GetBytes(body, "video_list"), "video") {
		return true
	}
	for _, field := range []string{"reference_images", "images", "image", "start_image_url", "end_image_url"} {
		if fzyReferenceContainsVideo(gjson.GetBytes(body, field), "") {
			return true
		}
	}
	return false
}
func fzyReferenceContainsVideo(value gjson.Result, kind string) bool {
	if value.IsArray() {
		for _, item := range value.Array() {
			if fzyReferenceContainsVideo(item, kind) {
				return true
			}
		}
		return false
	}
	if value.IsObject() {
		for _, field := range []string{"url", "image_url", "video_url", "audio_url"} {
			if next := value.Get(field); next.Exists() {
				return fzyReferenceContainsVideo(next, kind)
			}
		}
		return false
	}
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return false
	}
	if kind == "video" {
		return true
	}
	if prefix, rest, ok := strings.Cut(raw, ":"); ok {
		switch strings.ToLower(prefix) {
		case "video":
			return strings.TrimSpace(rest) != ""
		case "image", "audio":
			return false
		case "reference", "start", "end":
			raw = strings.TrimSpace(rest)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	name := firstNonEmpty(u.Query().Get("name"), u.Query().Get("filename"), u.Path)
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mov", ".webm", ".m4v":
		return true
	}
	return false
}
func FetchFZYVideoMarketplace(ctx context.Context, client *http.Client, marketplaceURL, apiKey string) ([]byte, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("provider API key is missing")
	}
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	if marketplaceURL == "" {
		marketplaceURL = fzyMarketplaceURL
	}
	u, err := url.Parse(marketplaceURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("provider marketplace URL is invalid")
	}
	query := u.Query()
	query.Set("apiKey", apiKey)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("provider marketplace request could not be created")
	}
	resp, err := client.Do(req)
	// URL errors may include the API key query. Never wrap them.
	if err != nil {
		return nil, errors.New("provider marketplace request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider marketplace returned HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(payload) > 2<<20 {
		return nil, errors.New("provider marketplace response could not be read")
	}
	return payload, nil
}
func FetchFZYVideoPriceSnapshot(ctx context.Context, client *http.Client, marketplaceURL, apiKey, model string, requestBody []byte) (FZYVideoPriceSnapshot, error) {
	payload, err := FetchFZYVideoMarketplace(ctx, client, marketplaceURL, apiKey)
	if err != nil {
		return FZYVideoPriceSnapshot{}, err
	}
	return ParseFZYVideoPriceSnapshot(payload, model, requestBody)
}
func (s *OpenAIGatewayService) FZYVideoCatalog(ctx context.Context, account *Account, model string) (map[string]any, error) {
	if !IsFZYPricedVideoAccount(account, model) {
		return nil, ErrFZYVideoBillingUnavailable
	}
	credential, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	payload, err := FetchFZYVideoMarketplace(ctx, nil, "", credential.GetCredential("api_key"))
	if err != nil {
		return nil, err
	}
	return ParseFZYVideoCatalogBundle(payload, model)
}
func ParseFZYVideoCatalogBundle(payload []byte, selectedModel string) (map[string]any, error) {
	available := []string{}
	models := []map[string]any{}
	result := map[string]any{"source": fzyPricingSource, "fetched_at": time.Now().UTC().Format(time.RFC3339)}
	for _, model := range []string{"doubao-seedance-1.5-pro", "doubao-seedance-2.0", "doubao-seedance-2.0-fast", "doubao-seedance-2.0-mini", "doubao-seedance-2.5", "kling-v3", "kling-v3-omni"} {
		catalog, err := ParseFZYVideoCatalog(payload, model)
		if err != nil {
			continue
		}
		available = append(available, model)
		models = append(models, catalog)
		if model == selectedModel {
			for k, v := range catalog {
				result[k] = v
			}
		}
	}
	if len(models) == 0 {
		return nil, errors.New("provider has no supported video tariffs")
	}
	result["available_models"], result["models"] = available, models
	return result, nil
}
