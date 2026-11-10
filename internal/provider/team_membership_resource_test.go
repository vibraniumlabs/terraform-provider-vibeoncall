package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const adaID = 7

func (a *fakeAPI) checkAdaRole(teamID int, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if got := a.memberships[teamID][adaID]; got != want {
			return fmt.Errorf("team %d role = %q, want %q", teamID, got, want)
		}
		return nil
	}
}

func membershipConfig(baseURL, team, role string) string {
	roleLine := ""
	if role != "" {
		roleLine = fmt.Sprintf("role = %q", role)
	}
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "platform" {
  name       = "Platform"
  depends_on = [vibeoncall_team.payments]
}

data "vibeoncall_member" "ada" {
  email = "ADA@example.com"
}

resource "vibeoncall_team_membership" "ada" {
  team_id = vibeoncall_team.%s.id
  user_id = data.vibeoncall_member.ada.id
  %s
}
`, team, roleLine)
}

func TestTeamMembershipResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t, fakeUser{ID: adaID, Email: "ada@example.com", FullName: strPtr("Ada Lovelace"), Status: "active"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: membershipConfig(srv.URL, "payments", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team_membership.ada", "id", "1:7"),
					resource.TestCheckResourceAttr("vibeoncall_team_membership.ada", "role", "member"),
					api.checkAdaRole(1, "member"),
				),
			},
			{
				Config: membershipConfig(srv.URL, "payments", "admin"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_team_membership.ada", plancheck.ResourceActionUpdate),
					},
				},
				Check: api.checkAdaRole(1, "admin"),
			},
			{
				ResourceName:      "vibeoncall_team_membership.ada",
				ImportState:       true,
				ImportStateId:     "1:7",
				ImportStateVerify: true,
			},
			{
				Config: membershipConfig(srv.URL, "platform", "admin"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_team_membership.ada", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team_membership.ada", "id", "2:7"),
					api.checkAdaRole(1, ""),
					api.checkAdaRole(2, "admin"),
				),
			},
			{
				PreConfig:          func() { api.removeMembership(2, adaID) },
				Config:             membershipConfig(srv.URL, "platform", "admin"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestTeamMembershipResourceRejectsMalformedImportID(t *testing.T) {
	_, srv := newFakeAPI(t)

	for _, id := range []string{"7", "1:", "VP-TEAM-1:7", "1:ada@example.com"} {
		t.Run(id, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:        providerConfig(srv.URL) + `resource "vibeoncall_team_membership" "x" {` + "\n" + `team_id = "1"` + "\n" + `user_id = "7"` + "\n}",
					ResourceName:  "vibeoncall_team_membership.x",
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid import ID`),
				}},
			})
		})
	}
}
