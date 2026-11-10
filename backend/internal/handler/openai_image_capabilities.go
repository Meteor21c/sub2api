package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) ImageCapabilities(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil || key.User == nil || key.GroupID == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if !service.GroupAllowsImageGeneration(key.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	model := strings.TrimSpace(c.Query("model"))
	if model == "" || len(model) > 256 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	ensureCompositeTargetPlatform(c, key, model)
	if !compositeTargetPlatformAllowed(c, key, model, service.PlatformOpenAI) {
		c.JSON(http.StatusOK, gin.H{"model": model, "output_formats": []string{}})
		return
	}
	routingModel := model
	if resolved, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok {
		routingModel = resolved
	}
	mapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), key.GroupID, routingModel)
	if mapping.MappedModel != "" {
		routingModel = mapping.MappedModel
	}
	formats, err := h.gatewayService.ImageOutputFormats(c.Request.Context(), key.GroupID, routingModel)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "capabilities_unavailable", "Image capabilities are temporarily unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"model": model, "output_formats": formats})
}
