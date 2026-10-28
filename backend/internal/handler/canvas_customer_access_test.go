package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestCanvasCustomerAccessUsesOwnJWT(t *testing.T) {
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	h := NewCanvasPilotHandler(service.NewCanvasPilotService(nil, service.NewBusinessMonitorService(nil)))
	for _, tc := range []struct {
		id           int64
		role, method string
		allowed      bool
	}{
		{7, "admin", "jwt", true}, {8, "admin", "jwt", true},
		{19, "user", "jwt", true}, {0, "user", "jwt", false},
		{19, "guest", "jwt", false}, {19, "user", "admin_api_key", false},
		{19, "user", "", false},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/canvas/access", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.id})
		c.Set(string(middleware.ContextKeyUserRole), tc.role)
		c.Set("auth_method", tc.method)
		h.Access(c)
		var result struct {
			Data struct {
				Allowed bool `json:"allowed"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Data.Allowed != tc.allowed {
			t.Fatalf("access id=%d role=%s method=%s body=%s", tc.id, tc.role, tc.method, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("account access must not be cached")
		}
	}
}

func TestCanvasCreativePricingOmitsInternalFields(t *testing.T) {
	price := 0.25
	group := service.Group{ID: 12, ProfitControlEnabled: true, ProfitMinMargin: 0.2, ModelPricing: []service.ChannelModelPricing{{
		ID: 90, ChannelID: 91, Models: []string{"gpt-image-2"}, PerRequestPrice: &price,
		Intervals: []service.PricingInterval{{ID: 92, PricingID: 90, TierLabel: "2K", PerRequestPrice: &price}},
	}}}
	raw, err := json.Marshal(creativeGroupPricing(group))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"channel_id", "pricing_id", "profit", "model_routing", "account", "cost"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("internal field leaked: %s", forbidden)
		}
	}
	if !strings.Contains(string(raw), `"per_request_price":0.25`) || !strings.Contains(string(raw), `"tier_label":"2K"`) {
		t.Fatalf("sale-price rule missing: %s", raw)
	}
}
