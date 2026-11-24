package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const magentLoginTTL = 5 * time.Minute
const magentLoginInterval = 5

var magentRequestID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var magentDeviceCode = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

var decideMagentLogin = redis.NewScript(`
if redis.call("HGET", KEYS[1], "status") ~= "pending" then return 0 end
redis.call("HSET", KEYS[1], "status", ARGV[1], "user_id", ARGV[2], "token_version", ARGV[3])
return 1
`)

var redeemMagentLogin = redis.NewScript(`
if redis.call("HGET", KEYS[1], "proof") ~= ARGV[1] then return {"expired"} end
local status = redis.call("HGET", KEYS[1], "status")
if status ~= "approved" then return {status} end
local user = redis.call("HGET", KEYS[1], "user_id")
local version = redis.call("HGET", KEYS[1], "token_version")
redis.call("DEL", KEYS[1])
return {status, user, version}
`)

type MagentLoginHandler struct {
	auth  *AuthHandler
	cache *redis.Client
}

func NewMagentLoginHandler(auth *AuthHandler, cache *redis.Client) *MagentLoginHandler {
	return &MagentLoginHandler{auth: auth, cache: cache}
}

func magentLoginKey(requestID string) string {
	return "magent:device-login:" + requestID
}

func magentLoginProof(deviceCode string) string {
	digest := sha256.Sum256([]byte(deviceCode))
	return hex.EncodeToString(digest[:])
}

func (h *MagentLoginHandler) Start(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var id [16]byte
	var secret [32]byte
	rand.Read(id[:])
	rand.Read(secret[:])
	requestID := hex.EncodeToString(id[:])
	deviceCode := base64.RawURLEncoding.EncodeToString(secret[:])
	key := magentLoginKey(requestID)
	ctx := c.Request.Context()
	if _, err := h.cache.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key, "status", "pending", "proof", magentLoginProof(deviceCode))
		pipe.Expire(ctx, key, magentLoginTTL)
		return nil
	}); err != nil {
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
	applied, err := decideMagentLogin.Run(ctx, h.cache, []string{magentLoginKey(input.RequestID)},
		status, strconv.FormatInt(user.ID, 10), strconv.FormatInt(user.TokenVersion, 10)).Int()
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "MeteorAgent sign-in is temporarily unavailable")
		return
	}
	if applied != 1 {
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
	result, err := redeemMagentLogin.Run(ctx, h.cache, []string{magentLoginKey(input.RequestID)}, magentLoginProof(input.DeviceCode)).StringSlice()
	if err != nil || len(result) == 0 {
		response.Error(c, http.StatusServiceUnavailable, "MeteorAgent sign-in is temporarily unavailable")
		return
	}
	if result[0] != "approved" {
		response.Success(c, gin.H{"status": result[0]})
		return
	}
	if len(result) != 3 {
		response.InternalError(c, "Invalid MeteorAgent sign-in state")
		return
	}
	userID, err := strconv.ParseInt(result[1], 10, 64)
	if err != nil || userID <= 0 {
		response.InternalError(c, "Invalid MeteorAgent sign-in state")
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
	if strconv.FormatInt(user.TokenVersion, 10) != result[2] {
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
