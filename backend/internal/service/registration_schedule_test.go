//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestValidateRegistrationSchedule(t *testing.T) {
	for _, tc := range []struct {
		start, end string
		valid      bool
	}{
		{"09:00", "22:00", true}, {"22:00", "06:00", true}, {"00:00", "23:59", true},
		{"09:00", "09:00", false}, {"", "22:00", false}, {"9:00", "22:00", false},
		{"09:00", "24:00", false}, {"09:00", "22:60", false}, {" 09:00", "22:00", false}, {"09:00", "22:00:00", false},
	} {
		t.Run(tc.start+"-"+tc.end, func(t *testing.T) {
			err := ValidateRegistrationSchedule(tc.start, tc.end)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Equal(t, "INVALID_REGISTRATION_SCHEDULE", infraerrors.Reason(err))
			}
		})
	}
}

func TestRegistrationScheduleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, start, end, utc string
		want                  bool
	}{
		{"before opening", "09:00", "22:00", "2026-10-28T00:59:59Z", false},
		{"opening inclusive", "09:00", "22:00", "2026-10-28T01:00:00Z", true},
		{"before closing", "09:00", "22:00", "2026-10-28T13:59:59Z", true},
		{"closing exclusive", "09:00", "22:00", "2026-10-28T14:00:00Z", false},
		{"overnight opening", "22:00", "06:00", "2026-10-28T14:00:00Z", true},
		{"overnight midnight", "22:00", "06:00", "2026-10-28T16:00:00Z", true},
		{"overnight before closing", "22:00", "06:00", "2026-10-28T21:59:59Z", true},
		{"overnight closing", "22:00", "06:00", "2026-10-28T22:00:00Z", false},
		{"overnight midday", "22:00", "06:00", "2026-10-28T04:00:00Z", false},
		{"equal fail closed", "09:00", "09:00", "2026-10-28T04:00:00Z", false},
		{"invalid fail closed", "", "22:00", "2026-10-28T04:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.utc)
			require.NoError(t, err)
			p := registrationPolicy{scheduleEnabled: true, start: tc.start, end: tc.end}
			require.Equal(t, tc.want, p.contains(now))
			require.Equal(t, tc.want, p.contains(now.In(time.FixedZone("server", -7*3600))))
		})
	}
}

func TestSettingService_RegistrationPolicyDefaultsAndFailure(t *testing.T) {
	ctx := context.Background()
	night := time.Date(2026, 10, 28, 23, 0, 0, 0, registrationScheduleLocation)
	repo := &settingRepoStub{values: map[string]string{SettingKeyRegistrationEnabled: "true"}}
	svc := NewSettingService(repo, &config.Config{})
	svc.registrationScheduleNow = func() time.Time { return night }
	require.NoError(t, svc.CheckRegistrationAllowed(ctx), "an absent schedule must remain disabled")
	require.True(t, svc.IsRegistrationEnabled(ctx))
	repo.values[SettingKeyRegistrationScheduleEnabled] = "true"
	require.ErrorIs(t, svc.CheckRegistrationAllowed(ctx), ErrRegistrationScheduleClosed)
	require.False(t, svc.IsRegistrationEnabled(ctx))
	repo.values[SettingKeyRegistrationEnabled] = "false"
	require.ErrorIs(t, svc.CheckRegistrationAllowed(ctx), ErrRegistrationDisabled)
	require.ErrorIs(t, svc.CheckRegistrationSchedule(ctx), ErrRegistrationScheduleClosed, "OAuth master-switch bypass must not bypass hours")
	repo.err = errors.New("database unavailable")
	require.False(t, svc.IsRegistrationEnabled(ctx))
	require.ErrorIs(t, svc.CheckRegistrationSchedule(ctx), ErrServiceUnavailable)
}

func TestSettingService_RegistrationScheduleSettingsDefaultsAndValidation(t *testing.T) {
	svc := NewSettingService(&settingGetAllRepoStub{values: map[string]string{SettingKeyRegistrationEnabled: "true"}}, &config.Config{})
	settings, err := svc.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.RegistrationScheduleEnabled)
	require.Equal(t, DefaultRegistrationScheduleStart, settings.RegistrationScheduleStart)
	require.Equal(t, DefaultRegistrationScheduleEnd, settings.RegistrationScheduleEnd)
	repo := &settingUpdateRepoStub{}
	svc = NewSettingService(repo, &config.Config{})
	require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{RegistrationEnabled: true}))
	require.Equal(t, "false", repo.updates[SettingKeyRegistrationScheduleEnabled])
	require.Equal(t, "09:00", repo.updates[SettingKeyRegistrationScheduleStart])
	require.Equal(t, "22:00", repo.updates[SettingKeyRegistrationScheduleEnd])
	previous := repo.updates
	err = svc.UpdateSettings(context.Background(), &SystemSettings{RegistrationScheduleStart: "09:00", RegistrationScheduleEnd: "09:00"})
	require.Equal(t, "INVALID_REGISTRATION_SCHEDULE", infraerrors.Reason(err))
	require.Equal(t, previous, repo.updates, "invalid values must never persist")
}

func TestSettingService_PublicRegistrationScheduleEffectiveState(t *testing.T) {
	for _, tc := range []struct {
		name, master, schedule, start string
		hour                          int
		wantEnabled, wantClosed       bool
	}{
		{"default unrestricted", "true", "false", "09:00", 23, true, false},
		{"within hours", "true", "true", "09:00", 10, true, false},
		{"outside hours", "true", "true", "09:00", 23, false, true},
		{"master disabled", "false", "true", "09:00", 23, false, false},
		{"invalid persisted policy", "true", "true", "", 10, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &settingPublicRepoStub{values: map[string]string{
				SettingKeyRegistrationEnabled: tc.master, SettingKeyRegistrationScheduleEnabled: tc.schedule,
				SettingKeyRegistrationScheduleStart: tc.start, SettingKeyRegistrationScheduleEnd: "22:00",
			}}
			svc := NewSettingService(repo, &config.Config{})
			svc.registrationScheduleNow = func() time.Time { return time.Date(2026, 10, 28, tc.hour, 0, 0, 0, registrationScheduleLocation) }
			settings, err := svc.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.wantEnabled, settings.RegistrationEnabled)
			require.Equal(t, tc.wantClosed, settings.RegistrationScheduleClosed)
			require.Equal(t, RegistrationScheduleTimezone, settings.RegistrationScheduleTimezone)
		})
	}
}

func scheduleAuthTestService(repo *userRepoStub) *AuthService {
	return newAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled: "true", SettingKeyRegistrationScheduleEnabled: "true",
		SettingKeyRegistrationScheduleStart: "09:00", SettingKeyRegistrationScheduleEnd: "22:00",
	}, nil, nil)
}

func TestAuthService_RegistrationScheduleRejectsRegistrationAndVerification(t *testing.T) {
	repo := &userRepoStub{}
	svc := scheduleAuthTestService(repo)
	svc.settingService.registrationScheduleNow = func() time.Time { return time.Date(2026, 10, 28, 23, 0, 0, 0, registrationScheduleLocation) }
	_, _, err := svc.Register(context.Background(), "test@example.com", "password")
	require.ErrorIs(t, err, ErrRegistrationScheduleClosed)
	require.ErrorIs(t, svc.SendVerifyCode(context.Background(), "test@example.com"), ErrRegistrationScheduleClosed)
	_, err = svc.SendVerifyCodeAsync(context.Background(), "test@example.com")
	require.ErrorIs(t, err, ErrRegistrationScheduleClosed)
	require.Empty(t, repo.created)
}

func TestAuthService_RegistrationScheduleRecheckedBeforeCreate(t *testing.T) {
	for _, mode := range []string{"password", "oauth", "oauth_token_pair"} {
		t.Run(mode, func(t *testing.T) {
			repo := &userRepoStub{}
			svc := scheduleAuthTestService(repo)
			calls := 0
			svc.settingService.registrationScheduleNow = func() time.Time {
				calls++
				if calls == 1 {
					return time.Date(2026, 10, 28, 21, 59, 59, 0, registrationScheduleLocation)
				}
				return time.Date(2026, 10, 28, 22, 0, 0, 0, registrationScheduleLocation)
			}
			var err error
			switch mode {
			case "oauth":
				_, _, err = svc.LoginOrRegisterOAuth(context.Background(), "test@example.com", "test")
			case "oauth_token_pair":
				svc.refreshTokenCache = &refreshTokenCacheStub{}
				_, _, err = svc.LoginOrRegisterOAuthWithTokenPair(context.Background(), "test@example.com", "test", "", "", "linuxdo")
			default:
				_, _, err = svc.Register(context.Background(), "test@example.com", "password")
			}
			require.ErrorIs(t, err, ErrRegistrationScheduleClosed)
			require.Equal(t, 2, calls)
			require.Empty(t, repo.created)
		})
	}
}

func TestAuthService_RegistrationScheduleDoesNotBlockExistingOAuthLogin(t *testing.T) {
	repo := &userRepoStub{user: &User{ID: 10, Email: "test@example.com", Username: "test", Status: StatusActive}}
	svc := scheduleAuthTestService(repo)
	svc.settingService.registrationScheduleNow = func() time.Time { return time.Date(2026, 10, 28, 23, 0, 0, 0, registrationScheduleLocation) }
	_, user, err := svc.LoginOrRegisterOAuth(context.Background(), "test@example.com", "test")
	require.NoError(t, err)
	require.Equal(t, int64(10), user.ID)
	require.Empty(t, repo.created)
}

func TestAuthService_RegistrationScheduleCannotBeBypassedByOAuthProvider(t *testing.T) {
	svc := scheduleAuthTestService(&userRepoStub{})
	svc.settingService.settingRepo.(*settingRepoStub).values[SettingKeyRegistrationEnabled] = "false"
	svc.settingService.registrationScheduleNow = func() time.Time { return time.Date(2026, 10, 28, 23, 0, 0, 0, registrationScheduleLocation) }
	require.ErrorIs(t, svc.checkOAuthRegistrationAllowed(context.Background(), "dingtalk"), ErrRegistrationScheduleClosed)
}
