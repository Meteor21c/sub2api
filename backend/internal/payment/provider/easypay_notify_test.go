package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func TestEasyPayVerifyNotificationConfirmsPaidOrderUpstream(t *testing.T) {
	t.Parallel()

	var queryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("act"); got != "order" {
			t.Errorf("act = %q, want order", got)
		}
		if got := r.PostForm.Get("out_trade_no"); got != "order-123" {
			t.Errorf("out_trade_no = %q, want order-123", got)
		}
		if got := r.PostForm.Get("pid"); got != "pid-1" {
			t.Errorf("pid = %q, want pid-1", got)
		}
		if got := r.PostForm.Get("key"); got != "pkey-1" {
			t.Errorf("query key was not the configured key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"trade_status":"TRADE_SUCCESS","money":"25.00","trade_no":"gateway-123"}`))
	}))
	defer server.Close()

	provider := newTestEasyPay(t, server.URL)
	notification, err := provider.VerifyNotification(context.Background(), signedEasyPayNotification(map[string]string{
		"pid":          "pid-1",
		"type":         "alipay",
		"out_trade_no": "order-123",
		"money":        "25.00",
		"trade_status": tradeStatusSuccess,
		"trade_no":     "gateway-123",
	}), nil)
	if err != nil {
		t.Fatalf("VerifyNotification returned error: %v", err)
	}
	if notification.Status != payment.ProviderStatusSuccess {
		t.Fatalf("status = %q, want success", notification.Status)
	}
	if notification.OrderID != "order-123" || notification.TradeNo != "gateway-123" {
		t.Fatalf("unexpected order/trade reference: %+v", notification)
	}
	if notification.Metadata["pid"] != "pid-1" {
		t.Fatalf("metadata pid = %q, want pid-1", notification.Metadata["pid"])
	}
	if queryCalls.Load() != 1 {
		t.Fatalf("upstream query calls = %d, want 1", queryCalls.Load())
	}
}

func TestEasyPayVerifyNotificationCanUseTradeNoFromUpstreamQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"trade_status":"TRADE_SUCCESS","money":"25.00","trade_no":"gateway-123"}`))
	}))
	defer server.Close()

	provider := newTestEasyPay(t, server.URL)
	notification, err := provider.VerifyNotification(context.Background(), signedEasyPayNotification(map[string]string{
		"pid":          "pid-1",
		"out_trade_no": "order-123",
		"money":        "25.00",
		"trade_status": tradeStatusSuccess,
	}), nil)
	if err != nil {
		t.Fatalf("VerifyNotification returned error: %v", err)
	}
	if notification.TradeNo != "gateway-123" {
		t.Fatalf("trade_no = %q, want upstream gateway-123", notification.TradeNo)
	}
}

func TestEasyPayVerifyNotificationRejectsInvalidOrUnconfirmedCallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		params      map[string]string
		queryBody   string
		wantQueries int
	}{
		{
			name: "wrong merchant pid",
			params: map[string]string{
				"pid": "other-pid", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			queryBody:   `{"code":1,"trade_status":"TRADE_SUCCESS","money":"25.00","trade_no":"gateway-123"}`,
			wantQueries: 0,
		},
		{
			name: "upstream says unpaid",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			queryBody:   `{"code":1,"trade_status":"WAITING","money":"25.00","trade_no":"gateway-123"}`,
			wantQueries: 1,
		},
		{
			name: "upstream amount mismatch",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			queryBody:   `{"code":1,"trade_status":"TRADE_SUCCESS","money":"24.99","trade_no":"gateway-123"}`,
			wantQueries: 1,
		},
		{
			name: "upstream non-finite amount",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			queryBody:   `{"code":1,"trade_status":"TRADE_SUCCESS","money":"NaN","trade_no":"gateway-123"}`,
			wantQueries: 1,
		},
		{
			name: "upstream trade number mismatch",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess, "trade_no": "forged-trade",
			},
			queryBody:   `{"code":1,"trade_status":"TRADE_SUCCESS","money":"25.00","trade_no":"gateway-123"}`,
			wantQueries: 1,
		},
		{
			name: "non-success query code cannot confirm paid status",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			queryBody:   `{"code":0,"status":1,"money":"25.00","trade_no":"gateway-123"}`,
			wantQueries: 1,
		},
		{
			name: "query response without a transaction ID",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			queryBody:   `{"code":1,"trade_status":"TRADE_SUCCESS","money":"25.00"}`,
			wantQueries: 1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var queryCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				queryCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.queryBody))
			}))
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			_, err := provider.VerifyNotification(context.Background(), signedEasyPayNotification(tt.params), nil)
			if err == nil {
				t.Fatal("VerifyNotification succeeded for an invalid or unconfirmed callback")
			}
			if int(queryCalls.Load()) != tt.wantQueries {
				t.Fatalf("upstream query calls = %d, want %d", queryCalls.Load(), tt.wantQueries)
			}
		})
	}
}

func TestEasyPayVerifyNotificationRejectsBadSignatureAndMalformedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params map[string]string
		mutate func(url.Values)
	}{
		{
			name: "invalid signature",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			mutate: func(values url.Values) { values.Set("sign", "invalid") },
		},
		{
			name: "missing sign type",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
			mutate: func(values url.Values) { values.Del("sign_type") },
		},
		{
			name: "missing order ID",
			params: map[string]string{
				"pid": "pid-1", "money": "25.00", "trade_status": tradeStatusSuccess,
			},
		},
		{
			name: "invalid amount",
			params: map[string]string{
				"pid": "pid-1", "out_trade_no": "order-123", "money": "NaN", "trade_status": tradeStatusSuccess,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var queryCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				queryCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":1,"trade_status":"TRADE_SUCCESS","money":"25.00","trade_no":"gateway-123"}`))
			}))
			defer server.Close()

			values, err := url.ParseQuery(signedEasyPayNotification(tt.params))
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			if tt.mutate != nil {
				tt.mutate(values)
			}
			provider := newTestEasyPay(t, server.URL)
			_, err = provider.VerifyNotification(context.Background(), values.Encode(), nil)
			if err == nil {
				t.Fatal("VerifyNotification succeeded for a malformed callback")
			}
			if queryCalls.Load() != 0 {
				t.Fatalf("upstream query calls = %d, want 0", queryCalls.Load())
			}
		})
	}
}

func TestEasyPayVerifyNotificationDoesNotQueryFailedPayment(t *testing.T) {
	t.Parallel()

	var queryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		queryCalls.Add(1)
	}))
	defer server.Close()

	provider := newTestEasyPay(t, server.URL)
	notification, err := provider.VerifyNotification(context.Background(), signedEasyPayNotification(map[string]string{
		"pid":          "pid-1",
		"out_trade_no": "order-123",
		"money":        "25.00",
		"trade_status": "TRADE_CLOSED",
	}), nil)
	if err != nil {
		t.Fatalf("VerifyNotification returned error: %v", err)
	}
	if notification.Status != payment.ProviderStatusFailed {
		t.Fatalf("status = %q, want failed", notification.Status)
	}
	if queryCalls.Load() != 0 {
		t.Fatalf("upstream query calls = %d, want 0", queryCalls.Load())
	}
}

func signedEasyPayNotification(params map[string]string) string {
	values := make(url.Values, len(params)+2)
	for key, value := range params {
		values.Set(key, value)
	}
	values.Set("sign", easyPaySign(params, "pkey-1"))
	values.Set("sign_type", signTypeMD5)
	return values.Encode()
}
