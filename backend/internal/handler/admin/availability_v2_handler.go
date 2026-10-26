package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AvailabilityV2Handler exposes the durable administrator channel-test API.
// Authentication and admin-role checks are installed by the parent /admin
// route group; the subject is still read here so every service call is scoped
// to the authenticated administrator.
type AvailabilityV2Handler struct {
	availabilityService *service.AvailabilityV2Service
}

func NewAvailabilityV2Handler(availabilityService *service.AvailabilityV2Service) *AvailabilityV2Handler {
	return &AvailabilityV2Handler{availabilityService: availabilityService}
}

type availabilityCreateConversationRequest struct {
	GroupID   int64  `json:"group_id"`
	AccountID *int64 `json:"account_id"`
	Model     string `json:"model"`
	Title     string `json:"title"`
}

type availabilityStreamTurnRequest struct {
	Prompt          string `json:"prompt"`
	ClientRequestID string `json:"client_request_id"`
}

func (h *AvailabilityV2Handler) Catalog(c *gin.Context) {
	ownerUserID, ok := availabilityAdminUserID(c)
	if !ok {
		return
	}
	groupID, ok := availabilityQueryID(c, "group_id", true)
	if !ok {
		return
	}
	accountID, ok := availabilityQueryID(c, "account_id", false)
	if !ok {
		return
	}
	catalog, err := h.availabilityService.Catalog(c.Request.Context(), ownerUserID, *groupID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, catalog)
}

func (h *AvailabilityV2Handler) ListConversations(c *gin.Context) {
	ownerUserID, ok := availabilityAdminUserID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	result, err := h.availabilityService.ListConversations(c.Request.Context(), ownerUserID, page, pageSize, c.Query("search"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AvailabilityV2Handler) CreateConversation(c *gin.Context) {
	ownerUserID, ok := availabilityAdminUserID(c)
	if !ok {
		return
	}
	var req availabilityCreateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	conversation, err := h.availabilityService.CreateConversation(c.Request.Context(), service.AvailabilityCreateConversationInput{
		OwnerUserID: ownerUserID,
		GroupID:     req.GroupID,
		AccountID:   req.AccountID,
		Model:       req.Model,
		Title:       req.Title,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, conversation)
}

func (h *AvailabilityV2Handler) GetConversation(c *gin.Context) {
	ownerUserID, ok := availabilityAdminUserID(c)
	if !ok {
		return
	}
	conversationID, ok := availabilityParamID(c, "id")
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	detail, err := h.availabilityService.GetConversation(c.Request.Context(), ownerUserID, conversationID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, detail)
}

// StreamTurn intentionally uses POST + fetch-compatible SSE.  It does not
// use EventSource because the request body carries the prompt and idempotency
// key and the existing admin auth mechanism may require request credentials.
func (h *AvailabilityV2Handler) StreamTurn(c *gin.Context) {
	ownerUserID, ok := availabilityAdminUserID(c)
	if !ok {
		return
	}
	conversationID, ok := availabilityParamID(c, "id")
	if !ok {
		return
	}
	var req availabilityStreamTurnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	streamStarted := false
	err := h.availabilityService.StreamTurn(c.Request.Context(), ownerUserID, conversationID, service.AvailabilityStreamTurnInput{
		Prompt:          req.Prompt,
		ClientRequestID: req.ClientRequestID,
	}, func(event service.AvailabilityEvent) error {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if !streamStarted {
			c.Header("Content-Type", "text/event-stream; charset=utf-8")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")
			streamStarted = true
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	})
	if err != nil && !streamStarted && !isAvailabilityClientDisconnect(err) {
		response.ErrorFrom(c, err)
	}
}

func (h *AvailabilityV2Handler) CancelTurn(c *gin.Context) {
	ownerUserID, ok := availabilityAdminUserID(c)
	if !ok {
		return
	}
	conversationID, ok := availabilityParamID(c, "id")
	if !ok {
		return
	}
	turnID, ok := availabilityParamID(c, "turn_id")
	if !ok {
		return
	}
	turn, err := h.availabilityService.CancelTurn(c.Request.Context(), ownerUserID, conversationID, turnID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, turn)
}

func availabilityAdminUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}

func availabilityQueryID(c *gin.Context, name string, required bool) (*int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		if required {
			response.BadRequest(c, name+" is required")
			return nil, false
		}
		return nil, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		response.BadRequest(c, "invalid "+name)
		return nil, false
	}
	return &value, true
}

func availabilityParamID(c *gin.Context, name string) (int64, bool) {
	value, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || value <= 0 {
		response.BadRequest(c, "invalid "+name)
		return 0, false
	}
	return value, true
}

func isAvailabilityClientDisconnect(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
