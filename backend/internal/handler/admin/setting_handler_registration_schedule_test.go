package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsRegistrationScheduleDefaultsRemainDisabled(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyRegistrationEnabled: "true",
	})
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "Meteor"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyRegistrationEnabled])
	require.Equal(t, "false", repo.values[service.SettingKeyRegistrationScheduleEnabled])
	require.Equal(t, "09:00", repo.values[service.SettingKeyRegistrationScheduleStart])
	require.Equal(t, "22:00", repo.values[service.SettingKeyRegistrationScheduleEnd])
	var payload struct {
		Data struct {
			Enabled bool   `json:"registration_schedule_enabled"`
			Start   string `json:"registration_schedule_start"`
			End     string `json:"registration_schedule_end"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.False(t, payload.Data.Enabled)
	require.Equal(t, "09:00", payload.Data.Start)
	require.Equal(t, "22:00", payload.Data.End)
}

func TestUpdateSettingsRegistrationScheduleRoundTripAndOmission(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyRegistrationEnabled: "true",
	})
	rec := doUpdateSettings(t, h, map[string]any{
		"registration_schedule_enabled": true,
		"registration_schedule_start":   "22:00",
		"registration_schedule_end":     "06:30",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	stored, err := h.settingService.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.True(t, stored.RegistrationEnabled)
	require.True(t, stored.RegistrationScheduleEnabled)
	require.Equal(t, "22:00", stored.RegistrationScheduleStart)
	require.Equal(t, "06:30", stored.RegistrationScheduleEnd)

	// Saving an unrelated section with an older client must retain the schedule.
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "Meteor updated"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "true", repo.values[service.SettingKeyRegistrationScheduleEnabled])
	require.Equal(t, "22:00", repo.values[service.SettingKeyRegistrationScheduleStart])
	require.Equal(t, "06:30", repo.values[service.SettingKeyRegistrationScheduleEnd])
	require.Equal(t, "true", repo.values[service.SettingKeyRegistrationEnabled])

	rec = doUpdateSettings(t, h, map[string]any{"registration_schedule_enabled": false}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "false", repo.values[service.SettingKeyRegistrationScheduleEnabled])
	require.Equal(t, "22:00", repo.values[service.SettingKeyRegistrationScheduleStart])
	require.Equal(t, "06:30", repo.values[service.SettingKeyRegistrationScheduleEnd])
}

func TestUpdateSettingsRegistrationScheduleRejectsInvalidWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"empty_start", map[string]any{"registration_schedule_start": ""}},
		{"empty_end", map[string]any{"registration_schedule_end": ""}},
		{"equal", map[string]any{"registration_schedule_end": "09:00"}},
		{"short_hour", map[string]any{"registration_schedule_start": "9:00"}},
		{"out_of_range", map[string]any{"registration_schedule_end": "24:00"}},
		{"bad_minute", map[string]any{"registration_schedule_start": "08:60"}},
		{"whitespace", map[string]any{"registration_schedule_start": " 08:00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{
				service.SettingKeyRegistrationEnabled:         "true",
				service.SettingKeyRegistrationScheduleEnabled: "false",
				service.SettingKeyRegistrationScheduleStart:   "09:00",
				service.SettingKeyRegistrationScheduleEnd:     "22:00",
			})
			before := make(map[string]string, len(repo.values))
			for key, value := range repo.values {
				before[key] = value
			}
			rec := doUpdateSettings(t, h, tc.body, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, before, repo.values)
		})
	}
}

func TestRegistrationScheduleSettingsAuditKeys(t *testing.T) {
	before := &service.SystemSettings{}
	after := &service.SystemSettings{
		RegistrationScheduleEnabled: true,
		RegistrationScheduleStart:   "09:00",
		RegistrationScheduleEnd:     "22:00",
	}
	keys := diffSettings(before, after, nil, nil, UpdateSettingsRequest{})
	require.Contains(t, keys, "registration_schedule_enabled")
	require.Contains(t, keys, "registration_schedule_start")
	require.Contains(t, keys, "registration_schedule_end")
}
