package service

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestAvailabilityDebugPlatformsFollowRegistry(t *testing.T) {
	for _, platform := range domain.Platforms() {
		t.Run(platform.ID, func(t *testing.T) {
			want := platform.Gateway == domain.PlatformGatewayOpenAI
			require.Equal(t, want, isOpenAICompatibleGroupPlatform(platform.ID))
			require.Equal(t, want, isOpenAICompatibleGroupPlatform(" "+strings.ToUpper(platform.ID)+" "))
		})
	}
	require.False(t, isOpenAICompatibleGroupPlatform(PlatformComposite))
	require.False(t, isOpenAICompatibleGroupPlatform("unregistered-platform"))
}

func TestAvailabilityDebugDefaultsUseProviderCatalog(t *testing.T) {
	for _, test := range []struct {
		platform string
		model    string
	}{
		{PlatformOpenCodeGo, DefaultOpenCodeGoTestModel},
		{PlatformCommandCode, DefaultCommandCodeTestModel},
		{PlatformCline, DefaultClineTestModel},
		{PlatformOpenAI, openai.DefaultTestModel},
		{PlatformKimi, openai.DefaultTestModel},
		{PlatformAnthropic, claude.DefaultTestModel},
	} {
		t.Run(test.platform, func(t *testing.T) {
			require.Equal(t, test.model, defaultGroupDebugModel(test.platform))
		})
	}
}

func TestAvailabilityModelsDoNotInventMultiProtocolCapabilities(t *testing.T) {
	for _, platform := range domain.Platforms() {
		if platform.Gateway != domain.PlatformGatewayOpenAI || platform.ID == PlatformOpenAI || platform.ID == PlatformGrok {
			continue
		}
		t.Run(platform.ID, func(t *testing.T) {
			account := Account{ID: 42, Platform: platform.ID, Type: AccountTypeAPIKey}
			group := &Group{ID: 12, Platform: platform.ID}
			require.Empty(t, availabilityDefaultModels(&account))
			require.Empty(t, buildAvailabilityModels(group, []Account{account}, nil))

			account.Credentials = map[string]any{
				"model_mapping": map[string]any{"public-model": "provider-model"},
			}
			models := buildAvailabilityModels(group, []Account{account}, nil)
			require.Len(t, models, 1)
			require.Equal(t, "public-model", models[0].ID)
			require.Equal(t, "provider-model", models[0].UpstreamModel)
		})
	}
	// Dedicated platform catalogs remain available without persisted metadata.
	require.NotEmpty(t, availabilityDefaultModels(&Account{Platform: PlatformOpenAI}))
	require.NotEmpty(t, availabilityDefaultModels(&Account{Platform: PlatformGrok}))
	require.NotEmpty(t, availabilityDefaultModels(&Account{Platform: PlatformAnthropic}))
}
