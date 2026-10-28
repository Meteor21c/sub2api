package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func closedOAuthRegistrationScheduleSettings() map[string]string {
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	return map[string]string{
		service.SettingKeyRegistrationEnabled:         "true",
		service.SettingKeyRegistrationScheduleEnabled: "true",
		service.SettingKeyRegistrationScheduleStart:   now.Add(12 * time.Hour).Format("15:04"),
		service.SettingKeyRegistrationScheduleEnd:     now.Add(13 * time.Hour).Format("15:04"),
	}
}

func TestDingTalkSignupBypassStillBlocksClosedSchedule(t *testing.T) {
	handler, _ := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: closedOAuthRegistrationScheduleSettings(),
	})
	require.True(t, handler.isDingTalkSignupBlocked(context.Background(), config.DingTalkConnectConfig{
		BypassRegistration:    true,
		CorpRestrictionPolicy: "internal_only",
	}))
}

func TestPendingOAuthAccountCreationScheduleClosed(t *testing.T) {
	for _, closingBeforeCommit := range []bool{false, true} {
		name := "closed_before_request"
		if closingBeforeCommit {
			name = "closes_before_commit"
		}
		t.Run(name, func(t *testing.T) {
			settings := closedOAuthRegistrationScheduleSettings()
			if closingBeforeCommit {
				settings[service.SettingKeyRegistrationScheduleEnabled] = "false"
			}
			handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
				settingValues:      settings,
				emailVerifyEnabled: true,
				emailCache: &oauthPendingFlowEmailCacheStub{
					verificationCodes: map[string]*service.VerificationCodeData{
						"fresh@example.com": {
							Code:      "246810",
							CreatedAt: time.Now().UTC(),
							ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
						},
					},
				},
			})
			ctx := context.Background()
			session, err := client.PendingAuthSession.Create().
				SetSessionToken("schedule-account-session").
				SetIntent("login").
				SetProviderType("oidc").
				SetProviderKey("https://issuer.example").
				SetProviderSubject("schedule-subject").
				SetBrowserSessionKey("schedule-browser-key").
				SetRedirectTo("/dashboard").
				SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
				Save(ctx)
			require.NoError(t, err)
			if closingBeforeCommit {
				pendingOAuthCreateAccountPreCommitHook = func(context.Context, *dbent.PendingAuthSession) error {
					handler.settingSvc = service.NewSettingService(&oauthPendingFlowSettingRepoStub{
						values: closedOAuthRegistrationScheduleSettings(),
					}, &config.Config{})
					return nil
				}
				t.Cleanup(func() { pendingOAuthCreateAccountPreCommitHook = nil })
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/create-account", bytes.NewBufferString(`{"email":"fresh@example.com","verify_code":"246810","password":"secret-123"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(session.SessionToken)})
			c.Request.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("schedule-browser-key")})
			handler.CreateOIDCOAuthAccount(c)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, "REGISTRATION_SCHEDULE_CLOSED", decodeJSONBody(t, recorder)["reason"])
			userCount, err := client.User.Query().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, userCount)
			identityCount, err := client.AuthIdentity.Query().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, identityCount)
			storedSession, err := client.PendingAuthSession.Get(ctx, session.ID)
			require.NoError(t, err)
			require.Nil(t, storedSession.ConsumedAt)
		})
	}
}
