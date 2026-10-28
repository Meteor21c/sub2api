package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestCanvasPilotRequiresOwnerJWTForEveryDataEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	h := NewCanvasPilotHandler(service.NewCanvasPilotService(nil, service.NewBusinessMonitorService(nil)))
	for _, test := range []struct {
		name         string
		id           int64
		role, method string
		allowed      bool
	}{
		{"owner", 7, "admin", "jwt", true}, {"other admin", 8, "admin", "jwt", false}, {"ordinary user", 7, "user", "jwt", false}, {"shared admin key", 7, "admin", "admin_api_key", false}, {"missing auth marker", 7, "admin", "", false}, {"anonymous", 0, "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, endpoint := range []gin.HandlerFunc{h.List, h.Save, h.Sync, h.Delete} {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/canvas/projects", strings.NewReader(`{}`))
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: test.id})
				c.Set(string(middleware.ContextKeyUserRole), test.role)
				c.Set("auth_method", test.method)
				if h.allowed(c) != test.allowed {
					t.Fatal("incorrect permission")
				}
				if !test.allowed {
					endpoint(c)
					if w.Code != http.StatusForbidden {
						t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
					}
					if w.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("private projects can be cached")
					}
				}
			}
		})
	}
}

func TestCanvasPilotAccessNeverExposesSecrets(t *testing.T) {
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	h := NewCanvasPilotHandler(service.NewCanvasPilotService(nil, service.NewBusinessMonitorService(nil)))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/canvas/access", nil)
	h.Access(c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allowed":false`) || strings.Contains(w.Body.String(), `owner`) {
		t.Fatalf("access=%s", w.Body.String())
	}
}
