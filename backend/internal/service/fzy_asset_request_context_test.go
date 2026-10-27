//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func fzyAssetTestAccount() *Account {
	account := grokMediaContentTestAccount()
	account.Credentials["base_url"] = "https://relay.example/provider/fzyinghe/v1"
	account.Credentials["header_override_enabled"] = true
	account.Credentials["header_overrides"] = map[string]any{
		"x-meteor-fzy-owner":       "user-999",
		"x-meteor-fzy-reservation": "overridden-reservation",
	}
	return account
}

func requireFZYAssetHeader(t *testing.T, header http.Header, name, want string) {
	t.Helper()
	var values []string
	for key, value := range header {
		if strings.EqualFold(key, name) {
			values = append(values, value...)
		}
	}
	if want == "" {
		require.Empty(t, values, name)
	} else {
		require.Equal(t, []string{want}, values, name)
	}
}

func TestForwardGrokMediaFZYAssetIdentityUsesAuthenticatedContext(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoint  GrokMediaEndpoint
		trusted   bool
		fzy       bool
		wantOwner string
		wantHold  string
	}{
		{"create", GrokMediaEndpointVideosGenerations, true, true, "user-11", "reservation-123"},
		{"status", GrokMediaEndpointVideoStatus, true, true, "user-11", ""},
		{"client_headers_without_context", GrokMediaEndpointVideoStatus, false, true, "", ""},
		{"non_fzy_does_not_leak_identity", GrokMediaEndpointVideoStatus, true, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &grokMediaContentUpstreamStub{
				response: grokMediaContentStatusResponse(`{"id":"meteorasset-task","status":"pending","stage":"asset_reviewing"}`),
			}
			c, _ := grokMediaContentTestContext(tc.endpoint.httpMethod(), "https://api.example/v1/videos/meteorasset-task", map[string]string{
				fzyAssetOwnerHeader:       "user-456",
				fzyAssetReservationHeader: "client-reservation",
			})
			if tc.trusted {
				SetFZYAssetOwner(c, 11)
				if tc.endpoint == GrokMediaEndpointVideosGenerations {
					SetFZYAssetReservation(c, "reservation-123")
				}
			}
			account := fzyAssetTestAccount()
			if !tc.fzy {
				account.Credentials["base_url"] = "https://relay.example/v1"
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			var body []byte
			if tc.endpoint.RequiresRequestBody() {
				body = []byte(`{"model":"doubao-seedance-2.0-mini","prompt":"test","duration":5,"resolution":"720p"}`)
			}
			result, err := svc.ForwardGrokMedia(context.Background(), c, account, tc.endpoint, "meteorasset-task", body, "application/json")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			requireFZYAssetHeader(t, upstream.request.Header, fzyAssetOwnerHeader, tc.wantOwner)
			requireFZYAssetHeader(t, upstream.request.Header, fzyAssetReservationHeader, tc.wantHold)
			require.Zero(t, result.VideoCount, "asset preparation is not completed video usage")
			require.False(t, result.VideoTaskFailed)
		})
	}
}

func TestForwardGrokMediaFZYAssetContentUsesOwnerForBothInternalRequests(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(`{"id":"meteorasset-task","status":"done"}`),
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"video/mp4"}},
			Body:       io.NopCloser(strings.NewReader("video-content")),
		},
	}}
	c, _ := grokMediaContentTestContext(http.MethodGet, "https://api.example/v1/videos/meteorasset-task/content", map[string]string{
		fzyAssetOwnerHeader: "user-999",
	})
	SetFZYAssetOwner(c, 11)
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	_, err := svc.ForwardGrokMedia(context.Background(), c, fzyAssetTestAccount(), GrokMediaEndpointVideoContent, "meteorasset-task", nil, "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	for _, request := range upstream.requests {
		requireFZYAssetHeader(t, request.Header, fzyAssetOwnerHeader, "user-11")
		requireFZYAssetHeader(t, request.Header, fzyAssetReservationHeader, "")
	}
}

type fzyAssetReconcileAccountRepoStub struct {
	AccountRepository
	account *Account
}

func (r *fzyAssetReconcileAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

type fzyAssetReconcileUserRepoStub struct {
	UserRepository
	loads int
}

func (r *fzyAssetReconcileUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	r.loads++
	return nil, errors.New("pending or failed assets must not enter final billing")
}

type fzyAssetReconcileBillingRepoStub struct {
	UsageBillingRepository
	FZYVideoBillingRepository
	releases []string
}

func (r *fzyAssetReconcileBillingRepoStub) ReleaseFZYVideo(_ context.Context, id string) (bool, error) {
	r.releases = append(r.releases, id)
	return true, nil
}

func TestReconcileFZYAssetPreparationKeepsReservationAndFailureReleases(t *testing.T) {
	for _, tc := range []struct {
		stage   string
		status  string
		release bool
	}{
		{"asset_pending", "pending", false},
		{"asset_reviewing", "pending", false},
		{"submission_unknown", "pending", false},
		{"failed", "failed", true},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			seen := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				seen <- req.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"meteorasset-task","status":"`+tc.status+`","stage":"`+tc.stage+`","provider_token_usage":{"completion_tokens":123}}`)
			}))
			defer server.Close()
			account := fzyAssetTestAccount()
			account.Credentials["base_url"] = server.URL + "/provider/fzyinghe/v1"
			repo := &fzyAssetReconcileBillingRepoStub{}
			users := &fzyAssetReconcileUserRepoStub{}
			svc := &OpenAIGatewayService{
				cfg:              &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{AllowInsecureHTTP: true}}},
				accountRepo:      &fzyAssetReconcileAccountRepoStub{account: account},
				userRepo:         users,
				usageBillingRepo: repo,
			}
			job := &FZYVideoBillingJob{ID: "reservation-123", TaskID: "meteorasset-task", UserID: 11, APIKeyID: 22, AccountID: account.ID, Status: "pending"}
			err := svc.reconcileFZYVideoJob(context.Background(), repo, job)
			require.NoError(t, err)
			header := <-seen
			require.Equal(t, "Bearer upstream-key", header.Get("Authorization"))
			require.Equal(t, "1", header.Get("X-Meteor-Billing-Only"))
			requireFZYAssetHeader(t, header, fzyAssetOwnerHeader, "user-11")
			requireFZYAssetHeader(t, header, fzyAssetReservationHeader, "")
			require.Zero(t, users.loads)
			if tc.release {
				require.Equal(t, []string{job.ID}, repo.releases)
			} else {
				require.Empty(t, repo.releases)
			}
		})
	}
}
