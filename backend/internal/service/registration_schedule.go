package service

import (
	"context"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	DefaultRegistrationScheduleStart = "09:00"
	DefaultRegistrationScheduleEnd   = "22:00"
	RegistrationScheduleTimezone     = "Asia/Shanghai"
)

var (
	ErrRegistrationScheduleClosed = infraerrors.Forbidden("REGISTRATION_SCHEDULE_CLOSED", "registration is outside the daily opening hours")
	// Beijing time has no daylight-saving transitions. A fixed zone avoids a dependency
	// on the host's timezone database and its local timezone configuration.
	registrationScheduleLocation = time.FixedZone(RegistrationScheduleTimezone, 8*60*60)
)

type registrationPolicy struct {
	enabled         bool
	scheduleEnabled bool
	start           string
	end             string
}

func registrationScheduleValue(values map[string]string, key, fallback string) string {
	if value, ok := values[key]; ok {
		return value
	}
	return fallback
}

func registrationPolicyFromSettings(values map[string]string) registrationPolicy {
	return registrationPolicy{
		enabled:         values[SettingKeyRegistrationEnabled] == "true",
		scheduleEnabled: values[SettingKeyRegistrationScheduleEnabled] == "true",
		start:           registrationScheduleValue(values, SettingKeyRegistrationScheduleStart, DefaultRegistrationScheduleStart),
		end:             registrationScheduleValue(values, SettingKeyRegistrationScheduleEnd, DefaultRegistrationScheduleEnd),
	}
}

func registrationMinute(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' || value[0] < '0' || value[0] > '2' || value[1] < '0' || value[1] > '9' || value[3] < '0' || value[3] > '5' || value[4] < '0' || value[4] > '9' {
		return 0, false
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	if hour > 23 {
		return 0, false
	}
	return hour*60 + int(value[3]-'0')*10 + int(value[4]-'0'), true
}

// ValidateRegistrationSchedule accepts exact HH:mm boundaries. The closing
// boundary is exclusive; equal boundaries are invalid rather than meaning 24h.
func ValidateRegistrationSchedule(start, end string) error {
	startMinute, startOK := registrationMinute(start)
	endMinute, endOK := registrationMinute(end)
	if !startOK || !endOK {
		return infraerrors.BadRequest("INVALID_REGISTRATION_SCHEDULE", "registration opening hours must use HH:mm (00:00–23:59)")
	}
	if startMinute == endMinute {
		return infraerrors.BadRequest("INVALID_REGISTRATION_SCHEDULE", "registration opening and closing times must be different")
	}
	return nil
}

func (p registrationPolicy) contains(now time.Time) bool {
	if !p.scheduleEnabled {
		return true
	}
	start, startOK := registrationMinute(p.start)
	end, endOK := registrationMinute(p.end)
	if !startOK || !endOK || start == end {
		return false // Invalid persisted enabled policies fail closed.
	}
	local := now.In(registrationScheduleLocation)
	minute := local.Hour()*60 + local.Minute()
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end // Across midnight.
}

func (s *SettingService) registrationTime() time.Time {
	if s != nil && s.registrationScheduleNow != nil {
		return s.registrationScheduleNow()
	}
	return time.Now()
}

func (s *SettingService) getRegistrationPolicy(ctx context.Context) (registrationPolicy, error) {
	if s == nil || s.settingRepo == nil {
		return registrationPolicy{}, ErrRegistrationDisabled
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyRegistrationEnabled,
		SettingKeyRegistrationScheduleEnabled,
		SettingKeyRegistrationScheduleStart,
		SettingKeyRegistrationScheduleEnd,
	})
	if err != nil {
		return registrationPolicy{}, fmt.Errorf("get registration policy: %w", err)
	}
	return registrationPolicyFromSettings(values), nil
}

func (p registrationPolicy) scheduleError(now time.Time) error {
	if p.contains(now) {
		return nil
	}
	return ErrRegistrationScheduleClosed.WithMetadata(map[string]string{
		"start": p.start, "end": p.end, "timezone": RegistrationScheduleTimezone,
	})
}

// CheckRegistrationAllowed evaluates the master switch and daily opening hours
// together. Every registration request is checked on the server at request time.
func (s *SettingService) CheckRegistrationAllowed(ctx context.Context) error {
	policy, err := s.getRegistrationPolicy(ctx)
	if err != nil || !policy.enabled {
		return ErrRegistrationDisabled
	}
	return policy.scheduleError(s.registrationTime())
}

// CheckRegistrationSchedule applies opening hours independently of the master
// switch, so OAuth providers allowed to bypass that switch cannot bypass hours.
func (s *SettingService) CheckRegistrationSchedule(ctx context.Context) error {
	policy, err := s.getRegistrationPolicy(ctx)
	if err != nil {
		return ErrServiceUnavailable
	}
	return policy.scheduleError(s.registrationTime())
}
