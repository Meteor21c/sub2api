//go:build embed

package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTMLCache_RegistrationHoursExpireAtBoundaries(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*3600)
	for _, tc := range []struct {
		name, start, end       string
		hour, minute, nextHour int
		nextDay                bool
	}{
		{"day opening", "09:00", "22:00", 8, 59, 9, false},
		{"day closing", "09:00", "22:00", 21, 59, 22, false},
		{"overnight closing", "22:00", "06:00", 5, 59, 6, false},
		{"overnight opening", "22:00", "06:00", 21, 59, 22, false},
		{"next day opening", "09:00", "22:00", 23, 59, 9, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 28, tc.hour, tc.minute, 0, 0, location)
			cache := NewHTMLCache()
			cache.now = func() time.Time { return now }
			settings := []byte(`{"registration_schedule_enabled":true,"registration_schedule_start":"` + tc.start + `","registration_schedule_end":"` + tc.end + `"}`)
			cache.Set([]byte("<html>test</html>"), settings)
			require.NotNil(t, cache.Get())
			day := 28
			if tc.nextDay {
				day++
			}
			boundary := time.Date(2026, 10, day, tc.nextHour, 0, 0, 0, location)
			now = boundary.Add(-time.Nanosecond)
			require.NotNil(t, cache.Get())
			now = boundary
			require.Nil(t, cache.Get(), "boundary itself must invalidate effective registration flag")
		})
	}
}

func TestHTMLCache_RegistrationDisabledScheduleKeepsCache(t *testing.T) {
	now := time.Date(2026, 10, 28, 21, 59, 0, 0, time.UTC)
	cache := NewHTMLCache()
	cache.now = func() time.Time { return now }
	cache.Set([]byte("<html>test</html>"), []byte(`{"registration_schedule_enabled":false}`))
	now = now.Add(48 * time.Hour)
	require.NotNil(t, cache.Get())
}

func TestHTMLCache_SettingsRenderSpanningBoundaryExpiresImmediately(t *testing.T) {
	now := time.Date(2026, 10, 28, 13, 59, 59, 0, time.UTC)
	cache := NewHTMLCache()
	cache.now = func() time.Time { return now }
	started := now
	now = now.Add(2 * time.Second)
	cache.SetAt([]byte("<html>test</html>"), []byte(`{"registration_schedule_enabled":true,"registration_schedule_start":"09:00","registration_schedule_end":"22:00"}`), started)
	require.Nil(t, cache.Get(), "old settings fetched across closing must never become a fresh cache")
}
