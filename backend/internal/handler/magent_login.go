package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const magentLoginTTL = 5 * time.Minute
const magentLoginInterval = 5

var magentRequestID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var magentDeviceCode = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type MagentLoginStore interface {
	Create(ctx context.Context, requestID, proof string, ttl time.Duration) error
	Decide(ctx context.Context, requestID, status string, userID, tokenVersion int64) (bool, error)
	Redeem(ctx context.Context, requestID, proof string) (status string, userID, tokenVersion int64, err error)
}

type MagentLoginHandler struct {
	auth  *AuthHandler
	store MagentLoginStore
}

func NewMagentLoginHandler(auth *AuthHandler, store MagentLoginStore) *MagentLoginHandler {
	return &MagentLoginHandler{auth: auth, store: store}
}

func magentLoginProof(deviceCode string) string {
	digest := sha256.Sum256([]byte(deviceCode))
	return hex.EncodeToString(digest[:])
}

func (h *MagentLoginHandler) Start(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var id [16]byte
	var secret [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		response.InternalError(c, "Could not create a MeteorAgent sign-in request")
		return
	}
	if _, err := rand.Read(secret[:]); err != nil {
		response.InternalError(c, "Could not create a MeteorAgent sign-in request")
		return
	}
	requestID := hex.EncodeToString(id[:])
	deviceCode := base64.RawURLEncoding.EncodeToString(secret[:])
	ctx := c.Request.Context()
	if err := h.store.Create(ctx, requestID, magentLoginProof(deviceCode), magentLoginTTL); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "MeteorAgent sign-in is temporarily unavailable")
		return
	}
	response.Success(c, gin.H{
		"request_id":  requestID,
		"device_code": deviceCode,
		"interval":    magentLoginInterval,
		"expires_in":  int(magentLoginTTL.Seconds()),
	})
}

type magentDecisionRequest struct {
	RequestID string `json:"request_id"`
	Approved  *bool  `json:"approved"`
}

func (h *MagentLoginHandler) Decide(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var input magentDecisionRequest
	if c.ShouldBindJSON(&input) != nil || !magentRequestID.MatchString(input.RequestID) || input.Approved == nil {
		response.BadRequest(c, "Invalid MeteorAgent sign-in request")
		return
	}
	ctx := c.Request.Context()
	user, err := h.auth.userService.GetByID(ctx, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.auth.ensureBackendModeAllowsUser(ctx, user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	status := "denied"
	if *input.Approved {
		status = "approved"
	}
	applied, err := h.store.Decide(ctx, input.RequestID, status, user.ID, user.TokenVersion)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "MeteorAgent sign-in is temporarily unavailable")
		return
	}
	if !applied {
		response.NotFound(c, "MeteorAgent sign-in request expired or was already answered")
		return
	}
	response.Success(c, gin.H{"status": status})
}

type magentRedemptionRequest struct {
	RequestID  string `json:"request_id"`
	DeviceCode string `json:"device_code"`
}

func (h *MagentLoginHandler) Token(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var input magentRedemptionRequest
	if c.ShouldBindJSON(&input) != nil || !magentRequestID.MatchString(input.RequestID) || !magentDeviceCode.MatchString(input.DeviceCode) {
		response.BadRequest(c, "Invalid MeteorAgent sign-in request")
		return
	}
	ctx := c.Request.Context()
	status, userID, tokenVersion, err := h.store.Redeem(ctx, input.RequestID, magentLoginProof(input.DeviceCode))
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "MeteorAgent sign-in is temporarily unavailable")
		return
	}
	if status != "approved" {
		response.Success(c, gin.H{"status": status})
		return
	}
	user, err := h.auth.userService.GetByID(ctx, userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if user.TokenVersion != tokenVersion {
		response.Success(c, gin.H{"status": "expired"})
		return
	}
	if err := h.auth.ensureBackendModeAllowsUser(ctx, user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Mint a restricted session using the desktop client's request fingerprint.
	tokens, err := h.auth.authService.GenerateMagentTokenPair(ctx, user)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "MeteorAgent sign-in is temporarily unavailable")
		return
	}
	response.Success(c, AuthResponse{
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken,
		ExpiresIn: tokens.ExpiresIn, TokenType: "Bearer", User: dto.UserFromService(user),
	})
}
