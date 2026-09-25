package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// FZYVideoPricing exposes a sanitized key-specific preview. Task creation
// fetches and freezes the tariff again, because the selected account/discount
// may change between preview and the paid request.
func (h *OpenAIGatewayHandler) FZYVideoPricing(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil || apiKey.User == nil || apiKey.GroupID == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	model := strings.TrimSpace(c.Query("model"))
	if !strings.HasPrefix(model, "doubao-seedance-") {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Token pricing is available only for Doubao Seedance video models")
		return
	}
	resolution := strings.TrimSpace(c.DefaultQuery("resolution", "720p"))
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" && !strings.EqualFold(resolution, "4K") {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid video resolution")
		return
	}
	duration, err := strconv.Atoi(c.DefaultQuery("duration", "5"))
	if err != nil || duration < 3 || duration > 30 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid video duration")
		return
	}
	audio := c.DefaultQuery("audio", "true") != "false"
	inputVideo := c.Query("input_video") == "true"
	request := map[string]any{"model": model, "resolution": resolution, "duration": duration, "audio": audio}
	if inputVideo {
		request["reference_images"] = []string{"https://example.invalid/reference.mp4"}
	}
	body, _ := json.Marshal(request)
	selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
		c.Request.Context(), apiKey.GroupID, "", "", model, map[int64]struct{}{},
		service.OpenAIUpstreamTransportHTTPSSE, grokMediaRequiredCapability(service.GrokMediaEndpointVideosGenerations),
		false, false, false, service.PlatformGrok,
	)
	if err != nil || selection == nil || selection.Account == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "video_pricing_unavailable", "No eligible video pricing account")
		return
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	if !service.IsFZYTokenVideoAccount(selection.Account, model) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Selected video account does not use FZY token billing")
		return
	}
	job, err := h.gatewayService.PrepareFZYVideoBilling(c.Request.Context(), selection.Account, apiKey, apiKey.User, model, body)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "video_pricing_unavailable", "Provider video pricing is temporarily unavailable")
		return
	}
	c.JSON(http.StatusOK, job.PricingPreview())
}
