package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type CanvasPilotHandler struct{ svc *service.CanvasPilotService }

func NewCanvasPilotHandler(svc *service.CanvasPilotService) *CanvasPilotHandler {
	return &CanvasPilotHandler{svc: svc}
}

func (h *CanvasPilotHandler) allowed(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	role, roleOK := middleware.GetUserRoleFromContext(c)
	return h != nil && h.svc != nil && ok && roleOK && role == "admin" && c.GetString("auth_method") == "jwt" && h.svc.AccessAllowed(subject.UserID)
}

func (h *CanvasPilotHandler) authorize(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Authorization")
	if !h.allowed(c) {
		response.Forbidden(c, "创作画布试用仅限站点所有者本人访问")
		return 0, false
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	return subject.UserID, true
}

func (h *CanvasPilotHandler) Access(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Authorization")
	response.Success(c, gin.H{"enabled": h != nil && h.svc != nil && h.svc.Enabled(), "allowed": h.allowed(c), "limits": gin.H{"maxProjects": service.CanvasMaxProjects, "maxProjectBytes": service.CanvasMaxProjectBytes, "maxOwnerBytes": service.CanvasMaxOwnerBytes}})
}

func canvasError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCanvasConflict):
		response.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrCanvasQuota):
		response.Error(c, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, service.ErrCanvasInvalid):
		response.BadRequest(c, err.Error())
	default:
		response.Error(c, http.StatusServiceUnavailable, "画布暂时无法同步，请保留本地项目后重试")
	}
}

func (h *CanvasPilotHandler) List(c *gin.Context) {
	ownerID, ok := h.authorize(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	projects, err := h.svc.List(ctx, ownerID)
	if err != nil {
		canvasError(c, err)
		return
	}
	response.Success(c, projects)
}

func (h *CanvasPilotHandler) Save(c *gin.Context) {
	ownerID, ok := h.authorize(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.CanvasMaxProjectBytes+4096)
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if c.ShouldBindJSON(&body) != nil {
		response.BadRequest(c, "项目格式错误或超过 1 MiB")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	projects, err := h.svc.Save(ctx, ownerID, []json.RawMessage{body.Data})
	if err != nil {
		canvasError(c, err)
		return
	}
	response.Success(c, projects[0])
}

func (h *CanvasPilotHandler) Sync(c *gin.Context) {
	ownerID, ok := h.authorize(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.CanvasMaxOwnerBytes+4096)
	var body struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if c.ShouldBindJSON(&body) != nil {
		response.BadRequest(c, "项目列表格式错误或超过 10 MiB")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if len(body.Projects) > 0 {
		if _, err := h.svc.Save(ctx, ownerID, body.Projects); err != nil {
			canvasError(c, err)
			return
		}
	}
	projects, err := h.svc.List(ctx, ownerID)
	if err != nil {
		canvasError(c, err)
		return
	}
	response.Success(c, projects)
}

func (h *CanvasPilotHandler) Delete(c *gin.Context) {
	ownerID, ok := h.authorize(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var body struct {
		IDs []string `json:"ids"`
	}
	if c.ShouldBindJSON(&body) != nil {
		response.BadRequest(c, "请选择有效的项目")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.svc.Delete(ctx, ownerID, body.IDs); err != nil {
		canvasError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
