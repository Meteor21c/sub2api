package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBusinessMonitorSnapshotCredentialStaysServerSide(t *testing.T) {
	const token = "fixture-private-service-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("service authorization mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"generatedAt":"2026-10-28T00:00:00Z","private_token":"must-not-reach-browser","accounts":[{"id":"test","name":"Supplier","baseUrl":"https://example.test","balance":10,"status":"ready","currency":"$","planName":"wallet","threshold":5,"api_key":"also-private"}]}`))
	}))
	defer server.Close()
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", server.URL)
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_TOKEN", token)
	svc := NewBusinessMonitorService(nil)
	defer svc.Stop()
	snapshot, err := svc.CurrentBalances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"balance":10`) {
		t.Fatal("balance missing")
	}
	for _, private := range []string{token, "private_token", "must-not-reach-browser", "api_key", "also-private"} {
		if strings.Contains(string(body), private) {
			t.Fatal("unexpected private field reached owner payload")
		}
	}
}

func TestBusinessMonitorSnapshotRedirectDoesNotForwardCredential(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", server.URL)
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_TOKEN", "fixture-private-token")
	svc := NewBusinessMonitorService(nil)
	defer svc.Stop()
	if _, err := svc.CurrentBalances(context.Background()); err == nil {
		t.Fatal("redirect accepted as snapshot")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("credential request followed redirect")
	}
}

func TestBusinessMonitorSnapshotRejectsUnsafeEndpointAndOversizedBody(t *testing.T) {
	for _, endpoint := range []string{"http://untrusted.example/snapshot", "https://monitor.meteor21c.fun/api/snapshot?token=private", "https://user:private@monitor.meteor21c.fun/api/snapshot", "file:///etc/passwd"} {
		t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", endpoint)
		svc := NewBusinessMonitorService(nil)
		if _, err := svc.CurrentBalances(context.Background()); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
		svc.Stop()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", 256*1024+1))) }))
	defer server.Close()
	t.Setenv("BUSINESS_MONITOR_SNAPSHOT_URL", server.URL)
	svc := NewBusinessMonitorService(nil)
	defer svc.Stop()
	if _, err := svc.CurrentBalances(context.Background()); err == nil {
		t.Fatal("oversized response accepted")
	}
}
