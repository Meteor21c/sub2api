//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestMagentBrowserSignIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	repo := &userHandlerRepoStub{user: &service.User{
		ID: 81, Email: "magent-qa@example.test", Role: service.RoleAdmin,
		Status: service.StatusActive, PasswordHash: "test-hash",
	}}
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret: "fixture-secret-for-magent-login", ExpireHour: 1, RefreshTokenExpireDays: 1,
	}}
	users := service.NewUserService(repo, nil, nil, nil)
	auth := service.NewAuthService(nil, repo, nil, repository.NewRefreshTokenCache(cache),
		cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	h := NewMagentLoginHandler(&AuthHandler{authService: auth, userService: users}, cache)
	router := gin.New()
	jwtAuth := gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, users, nil, nil))
	router.POST("/api/v1/auth/magent/start", h.Start)
	router.POST("/api/v1/auth/magent/token", h.Token)
	router.POST("/api/v1/auth/magent/decision", jwtAuth, h.Decide)
	router.GET("/api/v1/keys", jwtAuth, func(c *gin.Context) { c.Status(http.StatusOK) })
	router.POST("/api/v1/keys", jwtAuth, func(c *gin.Context) { c.Status(http.StatusOK) })
	webToken, err := auth.GenerateToken(context.Background(), repo.user)
	require.NoError(t, err)

	call := func(t *testing.T, method, path, token string, body any, status int) map[string]any {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		require.Equal(t, status, recorder.Code, recorder.Body.String())
		if status != http.StatusOK || recorder.Body.Len() == 0 {
			return nil
		}
		var envelope struct{ Data map[string]any }
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
		return envelope.Data
	}
	start := func(t *testing.T) map[string]any {
		data := call(t, http.MethodPost, "/api/v1/auth/magent/start", "", nil, http.StatusOK)
		return map[string]any{"request_id": data["request_id"], "device_code": data["device_code"]}
	}
	poll := func(t *testing.T, body map[string]any) map[string]any {
		return call(t, http.MethodPost, "/api/v1/auth/magent/token", "", body, http.StatusOK)
	}
	decide := func(t *testing.T, token string, requestID any, approve bool, status int) {
		call(t, http.MethodPost, "/api/v1/auth/magent/decision", token,
			map[string]any{"request_id": requestID, "approved": approve}, status)
	}

	t.Run("confirmed sessions are single use and read-only across refresh", func(t *testing.T) {
		request := start(t)
		require.Equal(t, "pending", poll(t, request)["status"])
		decide(t, "", request["request_id"], true, http.StatusUnauthorized)
		decide(t, webToken, request["request_id"], true, http.StatusOK)
		decide(t, webToken, request["request_id"], false, http.StatusNotFound)
		other := start(t)
		require.Equal(t, "expired", poll(t, map[string]any{
			"request_id": request["request_id"], "device_code": other["device_code"],
		})["status"])
		session := poll(t, request)
		access, ok := session["access_token"].(string)
		require.True(t, ok)
		require.Equal(t, "expired", poll(t, request)["status"])

		call(t, http.MethodGet, "/api/v1/keys", access, nil, http.StatusOK)
		call(t, http.MethodPost, "/api/v1/keys", access, nil, http.StatusUnauthorized)
		decide(t, access, other["request_id"], true, http.StatusUnauthorized)
		_, err := auth.ValidateToken(access)
		require.ErrorIs(t, err, service.ErrInvalidToken)
		_, err = jwt.Parse(access, func(_ *jwt.Token) (any, error) { return []byte(cfg.JWT.Secret), nil })
		require.ErrorIs(t, err, jwt.ErrTokenSignatureInvalid)

		refresh, ok := session["refresh_token"].(string)
		require.True(t, ok)
		require.False(t, strings.HasPrefix(refresh, "rt_"))
		rotated, err := auth.RefreshTokenPair(context.Background(), refresh)
		require.NoError(t, err)
		call(t, http.MethodGet, "/api/v1/keys", rotated.AccessToken, nil, http.StatusOK)
		call(t, http.MethodPost, "/api/v1/keys", rotated.AccessToken, nil, http.StatusUnauthorized)
	})

	t.Run("denial and expiry cannot issue credentials", func(t *testing.T) {
		request := start(t)
		decide(t, webToken, request["request_id"], false, http.StatusOK)
		require.Equal(t, map[string]any{"status": "denied"}, poll(t, request))
		request = start(t)
		redisServer.FastForward(magentLoginTTL)
		decide(t, webToken, request["request_id"], true, http.StatusNotFound)
		require.Equal(t, map[string]any{"status": "expired"}, poll(t, request))
	})

	t.Run("an account password change invalidates unredeemed approval", func(t *testing.T) {
		request := start(t)
		decide(t, webToken, request["request_id"], true, http.StatusOK)
		repo.user.PasswordHash = "changed-test-hash"
		require.Equal(t, map[string]any{"status": "expired"}, poll(t, request))
	})
}
