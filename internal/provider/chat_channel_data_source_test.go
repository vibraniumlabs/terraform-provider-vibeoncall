package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func chatChannelConfig(baseURL, name, microsoftTeamsTeamID string) string {
	teamArg := ""
	if microsoftTeamsTeamID != "" {
		teamArg = fmt.Sprintf("microsoft_teams_team_id = %q", microsoftTeamsTeamID)
	}
	return providerConfig(baseURL) + fmt.Sprintf(`
data "vibeoncall_chat_channel" "test" {
  name = %q
  %s
}
`, name, teamArg)
}

func TestChatChannelDataSourceFindsNameIgnoringCaseAcrossPages(t *testing.T) {
	api, srv := newFakeAPI(t)
	for i := 1; i <= 150; i++ {
		api.chatChannels = append(api.chatChannels, fakeChatChannel{ID: fmt.Sprintf("C%03d", i), Name: fmt.Sprintf("incidents-%d", i)})
	}
	api.chatChannels = append(api.chatChannels, fakeChatChannel{ID: "C999", Name: "incidents"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: chatChannelConfig(srv.URL, "Incidents", ""),
			Check:  resource.TestCheckResourceAttr("data.vibeoncall_chat_channel.test", "id", "C999"),
		}},
	})
}

func TestChatChannelDataSourceSendsMicrosoftTeamsTeamID(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.chatChannels = []fakeChatChannel{{ID: "19:a@thread.tacv2", Name: "General"}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: chatChannelConfig(srv.URL, "General", "team-a"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.vibeoncall_chat_channel.test", "id", "19:a@thread.tacv2"),
				func(*terraform.State) error {
					api.mu.Lock()
					defer api.mu.Unlock()
					if got := api.chatQuery.Get("microsoftTeamsTeamId"); got != "team-a" {
						return fmt.Errorf("microsoftTeamsTeamId = %q, want %q", got, "team-a")
					}
					return nil
				},
			),
		}},
	})
}

func TestChatChannelDataSourceErrorsWhenNotExactlyOneMatch(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.chatChannels = []fakeChatChannel{
		{ID: "C1", Name: "incidents-eu"},
		{ID: "C2", Name: "alerts"},
		{ID: "C3", Name: "Alerts"},
	}

	for name, want := range map[string]string{
		"incidents": `Chat channel not found`,
		"alerts":    `Multiple chat channels found`,
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      chatChannelConfig(srv.URL, name, ""),
					ExpectError: regexp.MustCompile(want),
				}},
			})
		})
	}
}

func TestChatChannelDataSourceRejectsEmptyOrPaddedArguments(t *testing.T) {
	_, srv := newFakeAPI(t)

	for args, want := range map[string]string{
		`name = ""`:            `must not be empty or start or end with whitespace`,
		`name = " incidents"`:  `must not be empty or start or end with whitespace`,
		`name = "incidents\n"`: `must not be empty or start or end with whitespace`,
		`name = "General"
  microsoft_teams_team_id = ""`: `string length must be at least 1`,
	} {
		t.Run(args, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      providerConfig(srv.URL) + "data \"vibeoncall_chat_channel\" \"test\" {\n  " + args + "\n}\n",
					ExpectError: regexp.MustCompile(want),
				}},
			})
		})
	}
}
