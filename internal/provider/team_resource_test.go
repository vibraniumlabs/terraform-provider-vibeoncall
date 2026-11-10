package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func (a *fakeAPI) checkDescription(id string, want *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		team := a.findTeam(id)
		if team == nil {
			return fmt.Errorf("team %s not found", id)
		}
		if (team.Description == nil) != (want == nil) || (want != nil && *team.Description != *want) {
			return fmt.Errorf("description = %v, want %v", team.Description, want)
		}
		return nil
	}
}

func teamConfig(baseURL, attrs string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "test" {
%s
}
`, attrs)
}

func TestTeamResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)
	description := "Owns the payment stack"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: teamConfig(srv.URL, `
  name        = "Payments"
  description = "Owns the payment stack"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team.test", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_team.test", "short_id", "VP-TEAM-1"),
					resource.TestCheckResourceAttr("vibeoncall_team.test", "name", "Payments"),
					resource.TestCheckResourceAttr("vibeoncall_team.test", "description", description),
					api.checkDescription("1", &description),
				),
			},
			{
				Config: teamConfig(srv.URL, `
  name = "Payments Platform"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team.test", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_team.test", "name", "Payments Platform"),
					resource.TestCheckNoResourceAttr("vibeoncall_team.test", "description"),
					api.checkDescription("1", nil),
				),
			},
			{
				ResourceName:      "vibeoncall_team.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "vibeoncall_team.test",
				ImportState:       true,
				ImportStateId:     "VP-TEAM-1",
				ImportStateVerify: true,
			},
			{
				PreConfig:          func() { api.removeTeam("1") },
				Config:             teamConfig(srv.URL, `  name = "Payments Platform"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestTeamResourceRejectsValuesTheAPIWouldTrim(t *testing.T) {
	_, srv := newFakeAPI(t)

	for name, attrs := range map[string]string{
		"empty name":                 `name = ""`,
		"name with leading space":    `name = " Payments"`,
		"name with trailing newline": `name = "Payments\n"`,
		"name with trailing nbsp":    `name = "Payments "`,
		"name over 255 characters":   fmt.Sprintf(`name = %q`, strings.Repeat("a", 256)),
		"empty description":          `name = "Payments"` + "\n" + `description = ""`,
		"blank description":          `name = "Payments"` + "\n" + `description = "  "`,
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      teamConfig(srv.URL, attrs),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`Invalid Attribute Value`),
				}},
			})
		})
	}
}
