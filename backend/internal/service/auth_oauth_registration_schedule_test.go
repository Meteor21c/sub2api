//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func closedOAuthRegistrationScheduleSettings() map[string]string {
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	return map[string]string{
		SettingKeyRegistrationEnabled:         "true",
		SettingKeyRegistrationScheduleEnabled: "true",
		SettingKeyRegistrationScheduleStart:   now.Add(12 * time.Hour).Format("15:04"),
		SettingKeyRegistrationScheduleEnd:     now.Add(13 * time.Hour).Format("15:04"),
	}
}

func TestOAuthAccountCreationRejectsClosedRegistrationSchedule(t *testing.T) {
	cases := []struct {
		name string
		run  func(*AuthService) error
	}{
		{
			name: "pending_email",
			run: func(s *AuthService) error {
				_, _, err := s.RegisterOAuthEmailAccount(context.Background(), "fresh@example.com", "secret-123", "123456", "", "oidc")
				return err
			},
		},
		{
			name: "provider_verified_email",
			run: func(s *AuthService) error {
				_, _, err := s.RegisterVerifiedOAuthEmailAccount(context.Background(), "fresh@example.com", "secret-123", "", "github")
				return err
			},
		},
		{
			name: "automatic_verified_email",
			run: func(s *AuthService) error {
				_, err := s.createEmailOAuthUser(context.Background(), "fresh@example.com", "fresh", "google", "", "")
				return err
			},
		},
		{
			name: "pending_verification_email",
			run: func(s *AuthService) error {
				_, err := s.SendPendingOAuthVerifyCode(context.Background(), "fresh@example.com")
				return err
			},
		},
		{
			name: "pending_account_finalization",
			run: func(s *AuthService) error {
				return s.FinalizeOAuthEmailAccount(context.Background(), &User{ID: 42}, "", "oidc", "")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userRepoStub{nextID: 42}
			svc := newOAuthEmailFlowAuthService(repo, nil, &refreshTokenCacheStub{}, closedOAuthRegistrationScheduleSettings(), &emailCacheStub{}, nil)
			err := tc.run(svc)
			require.Equal(t, "REGISTRATION_SCHEDULE_CLOSED", infraerrors.Reason(err))
			require.Empty(t, repo.created)
		})
	}
}

func TestOAuthDingTalkRegistrationBypassCannotBypassSchedule(t *testing.T) {
	settings := closedOAuthRegistrationScheduleSettings()
	settings[SettingKeyRegistrationEnabled] = "false"
	settings[SettingKeyDingTalkConnectEnabled] = "true"
	settings[SettingKeyDingTalkConnectBypassRegistration] = "true"
	settings[SettingKeyDingTalkConnectCorpRestrictionPolicy] = "internal_only"
	svc := newAuthServiceWithDingTalkCfg(settings, minDingTalkURLs())
	require.True(t, svc.canBypassRegistrationDisabledForOAuth(context.Background(), "dingtalk"))

	_, _, err := svc.RegisterVerifiedOAuthEmailAccount(context.Background(), "fresh@example.com", "secret-123", "", "dingtalk")
	require.Equal(t, "REGISTRATION_SCHEDULE_CLOSED", infraerrors.Reason(err))
	_, _, err = svc.RegisterOAuthEmailAccount(context.Background(), "fresh@example.com", "secret-123", "123456", "", "dingtalk")
	require.Equal(t, "REGISTRATION_SCHEDULE_CLOSED", infraerrors.Reason(err))
}
