package admin

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BusinessMonitorHandler requires the configured owner's own authenticated session.
// A shared administrator API key deliberately cannot impersonate that session.
type BusinessMonitorHandler struct {
	svc *service.BusinessMonitorService
}

func NewBusinessMonitorHandler(svc *service.BusinessMonitorService) *BusinessMonitorHandler {
	return &BusinessMonitorHandler{svc: svc}
}

func (h *BusinessMonitorHandler) allowed(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	role, roleOK := middleware.GetUserRoleFromContext(c)
	return h != nil && h.svc != nil && ok && roleOK && role == "admin" &&
		c.GetString("auth_method") == "jwt" && h.svc.AccessAllowed(subject.UserID)
}

func (h *BusinessMonitorHandler) authorize(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Authorization")
	if !h.allowed(c) {
		response.Forbidden(c, "经营监测仅限站点所有者本人访问")
		return false
	}
	return true
}

func (h *BusinessMonitorHandler) Access(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"enabled": h != nil && h.svc != nil && h.svc.OwnerID() > 0, "allowed": h.allowed(c)})
}

func (h *BusinessMonitorHandler) Balances(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	snapshot, err := h.svc.CurrentBalances(ctx)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "上游余额暂时不可用，请稍后重试")
		return
	}
	response.Success(c, snapshot)
}

func (h *BusinessMonitorHandler) Report(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	period := strings.TrimSpace(c.DefaultQuery("period", "day"))
	switch period {
	case "day", "week", "month", "year":
	default:
		response.BadRequest(c, "请选择日、周、月或年")
		return
	}
	date := strings.TrimSpace(c.Query("date"))
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			response.BadRequest(c, "日期格式应为 YYYY-MM-DD")
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	report, err := h.svc.Report(ctx, period, date)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "经营报表暂时不可用，请稍后重试")
		return
	}
	response.Success(c, report)
}

func (h *BusinessMonitorHandler) Refresh(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	report, err := h.svc.Refresh(ctx)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "余额采集暂时失败，已保存的历史记录仍可查看")
		return
	}
	response.Success(c, report)
}

func (h *BusinessMonitorHandler) SetCreditOverride(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	var input struct {
		WalletKey string   `json:"wallet_key" binding:"required,max=500"`
		Date      string   `json:"date" binding:"required"`
		Amount    *float64 `json:"credit_amount"`
		Note      string   `json:"note" binding:"max=200"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请填写钱包、日期及正确的调整金额")
		return
	}
	if _, err := time.Parse("2006-01-02", input.Date); err != nil {
		response.BadRequest(c, "日期格式应为 YYYY-MM-DD")
		return
	}
	if input.Amount != nil && (math.IsNaN(*input.Amount) || math.IsInf(*input.Amount, 0) || math.Abs(*input.Amount) > 10_000_000) {
		response.BadRequest(c, "调整金额超出范围")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := h.svc.SetCreditOverride(ctx, input.WalletKey, input.Date, input.Amount, strings.TrimSpace(input.Note)); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "调整未保存，请核对钱包和日期后重试")
		return
	}
	response.Success(c, gin.H{"saved": true})
}
