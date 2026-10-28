//go:build embed

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// HTMLCache manages the cached index.html with injected settings
type HTMLCache struct {
	mu              sync.RWMutex
	cachedHTML      []byte
	etag            string
	baseHTMLHash    string // Hash of the original index.html (immutable after build)
	settingsVersion uint64 // Incremented when settings change
	expiresAt       time.Time
	now             func() time.Time // optional clock for deterministic cache tests
}

// CachedHTML represents the cache state
type CachedHTML struct {
	Content []byte
	ETag    string
}

// NewHTMLCache creates a new HTML cache instance
func NewHTMLCache() *HTMLCache {
	return &HTMLCache{}
}

// SetBaseHTML initializes the cache with the base HTML template
func (c *HTMLCache) SetBaseHTML(baseHTML []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hash := sha256.Sum256(baseHTML)
	c.baseHTMLHash = hex.EncodeToString(hash[:8]) // First 8 bytes for brevity
}

// Invalidate marks the cache as stale
func (c *HTMLCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.settingsVersion++
	c.cachedHTML = nil
	c.etag = ""
	c.expiresAt = time.Time{}
}

// Get returns the cached HTML or nil if cache is stale
func (c *HTMLCache) Get() *CachedHTML {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.cachedHTML == nil || (!c.expiresAt.IsZero() && !c.currentTime().Before(c.expiresAt)) {
		return nil
	}
	return &CachedHTML{
		Content: c.cachedHTML,
		ETag:    c.etag,
	}
}

// Set updates the cache with new rendered HTML
func (c *HTMLCache) Set(html []byte, settingsJSON []byte) {
	c.SetAt(html, settingsJSON, c.currentTime())
}

// SetAt uses the start of settings rendering so a fetch spanning the next
// opening-hours boundary cannot preserve the old effective flag for a day.
func (c *HTMLCache) SetAt(html []byte, settingsJSON []byte, renderedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedHTML = html
	c.etag = c.generateETag(settingsJSON)
	c.expiresAt = registrationSettingsExpiry(settingsJSON, renderedAt)
}

func (c *HTMLCache) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func registrationSettingsExpiry(settingsJSON []byte, renderedAt time.Time) time.Time {
	var settings struct {
		Enabled bool   `json:"registration_schedule_enabled"`
		Start   string `json:"registration_schedule_start"`
		End     string `json:"registration_schedule_end"`
	}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil || !settings.Enabled {
		return time.Time{}
	}
	location := time.FixedZone("Asia/Shanghai", 8*3600)
	now := renderedAt.In(location)
	var next time.Time
	for _, value := range []string{settings.Start, settings.End} {
		clock, err := time.Parse("15:04", value)
		if err != nil || len(value) != 5 || settings.Start == settings.End {
			return renderedAt.Add(time.Minute)
		}
		boundary := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
		if !boundary.After(now) {
			boundary = boundary.AddDate(0, 0, 1)
		}
		if next.IsZero() || boundary.Before(next) {
			next = boundary
		}
	}
	return next
}

// generateETag creates an ETag from base HTML hash + settings hash
func (c *HTMLCache) generateETag(settingsJSON []byte) string {
	settingsHash := sha256.Sum256(settingsJSON)
	return `"` + c.baseHTMLHash + "-" + hex.EncodeToString(settingsHash[:8]) + `"`
}
