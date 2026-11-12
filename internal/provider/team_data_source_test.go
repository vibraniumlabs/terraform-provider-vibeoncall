package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func teamDataSourceConfig(baseURL, name string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
data "vibeoncall_team" "test" {
  name = %q
}
`, name)
}

func TestTeamDataSourceFindsExactNameAcrossPages(t *testing.T) {
	api, srv := newFakeAPI(t)
	for i := 1; i <= 150; i++ {
		api.teams[i] = &fakeTeam{ID: i, ShortID: fmt.Sprintf("VP-TEAM-%d", i), Name: fmt.Sprintf("Platform %d", i)}
	}
	api.teams[200] = &fakeTeam{ID: 200, ShortID: "VP-TEAM-200", Name: "platform"}
	api.teams[201] = &fakeTeam{ID: 201, ShortID: "VP-TEAM-201", Name: "Platform", Description: strPtr("Core infra")}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: teamDataSourceConfig(srv.URL, "Platform"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.vibeoncall_team.test", "id", "201"),
				resource.TestCheckResourceAttr("data.vibeoncall_team.test", "short_id", "VP-TEAM-201"),
				resource.TestCheckResourceAttr("data.vibeoncall_team.test", "description", "Core infra"),
			),
		}},
	})
}

func TestTeamDataSourceCountsATeamSeenOnTwoPagesOnce(t *testing.T) {
	api, srv := newFakeAPI(t)
	for i := 2; i <= 100; i++ {
		api.teams[i] = &fakeTeam{ID: i, ShortID: fmt.Sprintf("VP-TEAM-%d", i), Name: fmt.Sprintf("Team %d", i)}
	}
	api.teams[101] = &fakeTeam{ID: 101, ShortID: "VP-TEAM-101", Name: "Platform"}
	api.teams[102] = &fakeTeam{ID: 102, ShortID: "VP-TEAM-102", Name: "Team 102"}
	api.afterTeams = func() {
		api.teams[1] = &fakeTeam{ID: 1, ShortID: "VP-TEAM-1", Name: "Created mid-plan"}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: teamDataSourceConfig(srv.URL, "Platform"),
			Check:  resource.TestCheckResourceAttr("data.vibeoncall_team.test", "id", "101"),
		}},
	})
}

func TestTeamDataSourceErrorsWhenNotExactlyOneMatch(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.teams[1] = &fakeTeam{ID: 1, ShortID: "VP-TEAM-1", Name: "Platform Ops"}
	api.teams[2] = &fakeTeam{ID: 2, ShortID: "VP-TEAM-2", Name: "Payments"}
	api.teams[3] = &fakeTeam{ID: 3, ShortID: "VP-TEAM-3", Name: "Payments"}

	for name, want := range map[string]string{
		"Platform": `Team not found`,
		"Payments": `Multiple teams found`,
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      teamDataSourceConfig(srv.URL, name),
					ExpectError: regexp.MustCompile(want),
				}},
			})
		})
	}
}
