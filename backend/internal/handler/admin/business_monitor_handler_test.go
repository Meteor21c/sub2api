package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestBusinessMonitorOwnerSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", "https://monitor.example.test/api/snapshot")
	h := NewBusinessMonitorHandler(service.NewBusinessMonitorService(nil))
	tests := []struct {
		name         string
		id           int64
		role, method string
		allowed      bool
	}{
		{"owner", 7, "admin", "jwt", true},
		{"other admin", 8, "admin", "jwt", false},
		{"ordinary user", 7, "user", "jwt", false},
		{"shared admin key", 7, "admin", "admin_api_key", false},
		{"no session", 0, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/business-monitor/report", nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tt.id})
			c.Set(string(middleware.ContextKeyUserRole), tt.role)
			c.Set("auth_method", tt.method)
			if got := h.allowed(c); got != tt.allowed {
				t.Fatalf("allowed=%v want=%v", got, tt.allowed)
			}
			if !tt.allowed {
				h.Report(c)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status=%d", w.Code)
				}
				if strings.Contains(w.Body.String(), "wallets") {
					t.Fatal("financial data leaked")
				}
			}
		})
	}
}

func TestBusinessMonitorAccessAndInputValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	h := NewBusinessMonitorHandler(service.NewBusinessMonitorService(nil))
	for _, query := range []string{"?period=all", "?date=bad-date"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/report"+query, nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set("auth_method", "jwt")
		h.Report(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("query=%s status=%d body=%s", query, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/access", nil)
	h.Access(c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allowed":false`) {
		t.Fatalf("access=%s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("access response must not be cached")
	}
}

func TestBusinessMonitorBalancesRequiresOwnerJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const token = "fixture-snapshot-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing service credential")
		}
		_, _ = w.Write([]byte(`{"generatedAt":"2026-10-28T00:00:00Z","accounts":[{"id":"test","name":"Test supplier","baseUrl":"https://example.test","balance":2,"status":"ready"}]}`))
	}))
	defer upstream.Close()
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", upstream.URL)
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_TOKEN", token)
	svc := service.NewBusinessMonitorService(nil)
	defer svc.Stop()
	h := NewBusinessMonitorHandler(svc)
	for _, tc := range []struct {
		name         string
		id           int64
		role, method string
		status       int
	}{
		{"owner session", 7, "admin", "jwt", http.StatusOK},
		{"other admin", 8, "admin", "jwt", http.StatusForbidden},
		{"ordinary user", 7, "user", "jwt", http.StatusForbidden},
		{"shared admin key", 7, "admin", "admin_api_key", http.StatusForbidden},
		{"anonymous", 0, "", "", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/business-monitor/balances", nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.id})
			c.Set(string(middleware.ContextKeyUserRole), tc.role)
			c.Set("auth_method", tc.method)
			h.Balances(c)
			if w.Code != tc.status {
				t.Fatalf("status %d want %d", w.Code, tc.status)
			}
			if strings.Contains(w.Body.String(), token) {
				t.Fatal("service token reached browser")
			}
			if tc.status != http.StatusOK && strings.Contains(w.Body.String(), "Test supplier") {
				t.Fatal("owner financial data leaked")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private balance response cacheable")
			}
		})
	}
}
