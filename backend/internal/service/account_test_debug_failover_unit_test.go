//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTestGroupDebugFailsOverToAnotherChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(4201)
	upstreamServer := httptest.NewServer(nil)
	t.Cleanup(upstreamServer.Close)

	accounts := []Account{
		{
			ID: 1, Name: "first channel", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "first-key", "base_url": upstreamServer.URL},
			Concurrency: 1, Priority: 1, Status: StatusActive, Schedulable: true,
			AccountGroups: []AccountGroup{{GroupID: groupID}},
		},
		{
			ID: 2, Name: "second channel", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "second-key", "base_url": upstreamServer.URL},
			Concurrency: 1, Priority: 2, Status: StatusActive, Schedulable: true,
			AccountGroups: []AccountGroup{{GroupID: groupID}},
		},
	}
	accountByID := map[int64]*Account{}
	for i := range accounts {
		accountByID[accounts[i].ID] = &accounts[i]
	}
	accountRepo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: accountByID}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{
		groupID: {ID: groupID, Name: "debug group", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true},
	}}
	cfg := &config.Config{
		Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{AllowInsecureHTTP: true}},
	}
	gateway := &GatewayService{accountRepo: accountRepo, groupRepo: groupRepo, cfg: cfg}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"first channel rejected the key"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"healthy\"}}\n\n" +
					"data: {\"type\":\"message_stop\"}\n\n",
			)),
		},
	}}
	service := &AccountTestService{
		accountRepo:         accountRepo,
		gatewayService:      gateway,
		httpUpstream:        upstream,
		cfg:                 cfg,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/groups/4201/debug-test", nil)

	result, err := service.TestGroupDebug(c, groupID, "claude-test", "hello")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success)
	require.Equal(t, int64(2), result.AccountID)
	require.Equal(t, "second channel", result.AccountName)
	require.True(t, result.FailoverAttempted)
	require.True(t, result.FailoverSucceeded)
	require.Len(t, result.Attempts, 2)
	require.Equal(t, int64(1), result.Attempts[0].AccountID)
	require.Equal(t, http.StatusUnauthorized, result.Attempts[0].StatusCode)
	require.Contains(t, result.Attempts[0].Error, "401")
	require.Equal(t, int64(2), result.Attempts[1].AccountID)
	require.True(t, result.Attempts[1].Success)
}
