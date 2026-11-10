package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ModelTestHandler struct{ service *service.ModelTestService }

func NewModelTestHandler(s *service.ModelTestService) *ModelTestHandler {
	return &ModelTestHandler{service: s}
}
func (h *ModelTestHandler) Start(gateway http.Handler) { h.service.Start(gateway) }

func modelTestOwner(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Error(c, 401, "请先登录")
		return 0, false
	}
	return subject.UserID, true
}

func modelTestReply(c *gin.Context, data any, err error) {
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

func (h *ModelTestHandler) Overview(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	plans, err := h.service.ListPlans(c.Request.Context())
	if err != nil {
		modelTestReply(c, nil, err)
		return
	}
	jobs, err := h.service.ListJobs(c.Request.Context(), owner, "scheduled")
	if err != nil {
		modelTestReply(c, nil, err)
		return
	}
	response.Success(c, gin.H{"plans": plans, "jobs": jobs, "interval_hours": 2, "retention_hours": 24})
}

func (h *ModelTestHandler) Keys(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	data, err := h.service.KeyOptions(c.Request.Context(), owner)
	modelTestReply(c, data, err)
}

func (h *ModelTestHandler) Models(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Query("api_key_id"), 10, 64)
	if err != nil || id <= 0 {
		modelTestReply(c, nil, service.ErrModelTestInvalid)
		return
	}
	data, err := h.service.Models(c.Request.Context(), owner, id, middleware.SecurityClientIP(c))
	modelTestReply(c, data, err)
}

func (h *ModelTestHandler) List(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	data, err := h.service.ListJobs(c.Request.Context(), owner, "user")
	modelTestReply(c, data, err)
}

func (h *ModelTestHandler) Detail(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	source := c.DefaultQuery("source", "user")
	data, err := h.service.GetJob(c.Request.Context(), owner, source, c.Param("id"))
	modelTestReply(c, data, err)
}

func (h *ModelTestHandler) Submit(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input service.ModelTestSubmitInput
	if err := c.ShouldBindJSON(&input); err != nil {
		modelTestReply(c, nil, service.ErrModelTestInvalid)
		return
	}
	data, err := h.service.Submit(c.Request.Context(), owner, middleware.SecurityClientIP(c), input)
	if err != nil {
		modelTestReply(c, nil, err)
		return
	}
	response.Accepted(c, data)
}

func (h *ModelTestHandler) AdminPlans(c *gin.Context) {
	data, err := h.service.ListPlans(c.Request.Context())
	modelTestReply(c, data, err)
}
func (h *ModelTestHandler) AdminGroups(c *gin.Context) {
	data, err := h.service.GroupOptions(c.Request.Context())
	modelTestReply(c, data, err)
}
func (h *ModelTestHandler) SavePlan(c *gin.Context) {
	owner, ok := modelTestOwner(c)
	if !ok {
		return
	}
	var id int64
	if raw := c.Param("id"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			modelTestReply(c, nil, service.ErrModelTestInvalid)
			return
		}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input service.ModelTestPlanInput
	if err := c.ShouldBindJSON(&input); err != nil {
		modelTestReply(c, nil, service.ErrModelTestInvalid)
		return
	}
	data, err := h.service.SavePlan(c.Request.Context(), owner, id, input)
	modelTestReply(c, data, err)
}
func (h *ModelTestHandler) DeletePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		modelTestReply(c, nil, service.ErrModelTestInvalid)
		return
	}
	err = h.service.DeletePlan(c.Request.Context(), id)
	modelTestReply(c, gin.H{"deleted": err == nil}, err)
}
