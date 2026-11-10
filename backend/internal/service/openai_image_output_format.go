package service

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Special formats are opt-in per actual upstream model, including on fallbacks.
const AccountExtraOpenAIImageOutputFormats = "openai_image_output_formats"

func (a *Account) SupportsOpenAIImageOutputFormat(model, format string) bool {
	if !strings.EqualFold(strings.TrimSpace(format), "gif") {
		return true
	}
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	upstream := a.GetMappedModel(model)
	if upstream != "gpt-image-2.6" {
		return false
	}
	formats, _ := a.Extra[AccountExtraOpenAIImageOutputFormats].(map[string]any)
	switch values := formats[upstream].(type) {
	case []any:
		for _, value := range values {
			if value == "gif" {
				return true
			}
		}
	case []string:
		return slices.Contains(values, "gif")
	}
	return false
}

type openAIImageOutputContextKey struct{}
type openAIImageOutputRouting struct{ model, format string }

func WithOpenAIImageOutputRouting(ctx context.Context, model, format string) context.Context {
	return context.WithValue(ctx, openAIImageOutputContextKey{}, openAIImageOutputRouting{model, strings.ToLower(strings.TrimSpace(format))})
}

func openAIImageOutputFormatVeto(ctx context.Context, account *Account) bool {
	value, _ := ctx.Value(openAIImageOutputContextKey{}).(openAIImageOutputRouting)
	return value.format == "gif" && !account.SupportsOpenAIImageOutputFormat(value.model, value.format)
}

// Frame controls were ignored in provider tests; offer the verified combination.
func ValidateOpenAIImageGIFRequest(req *OpenAIImagesRequest) error {
	if !strings.EqualFold(strings.TrimSpace(req.OutputFormat), "gif") {
		return nil
	}
	if req.N != 1 || req.IsEdits() || len(req.InputImageURLs) > 0 || len(req.Uploads) > 0 || req.HasMask || req.MarkupImageURL != "" || req.MarkupUpload != nil || req.Stream || (req.Size != "" && req.Size != "auto") || req.FrameCount != nil || req.FrameDurationMS != nil {
		return errors.New("GIF supports one text-generated image, size auto, without references, streaming or frame controls")
	}
	return nil
}

// Discovery is group scoped, does not acquire slots and never calls providers.
func (s *OpenAIGatewayService) ImageOutputFormats(ctx context.Context, groupID *int64, model string) ([]string, error) {
	queryGroupID, includeGrouped := groupID, false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID, includeGrouped = nil, true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, queryGroupID, []string{PlatformOpenAI}, includeGrouped)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		a := &accounts[i]
		if !a.IsModelSupported(model) || !a.SupportsOpenAIImageCapability(OpenAIImagesCapabilityNative) {
			continue
		}
		if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) && s.isUpstreamModelRestrictedByChannel(ctx, *groupID, a, model, false) {
			continue
		}
		if a.SupportsOpenAIImageOutputFormat(model, "gif") {
			return []string{"gif"}, nil
		}
	}
	return []string{}, nil
}
