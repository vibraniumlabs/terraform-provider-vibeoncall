package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	bobID   = 8
	carolID = 9
	ghostID = 99
)

var teamMemberUsers = []fakeUser{
	{ID: adaID, Email: "ada@example.com", Status: "active"},
	{ID: bobID, Email: "bob@example.com", Status: "active"},
	{ID: carolID, Email: "carol@example.com", Status: "active"},
}

func (a *fakeAPI) checkMembers(teamID int, want map[int]string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		got := a.memberships[teamID]
		if len(got) != len(want) {
			return fmt.Errorf("team %d members = %v, want %v", teamID, got, want)
		}
		for id, role := range want {
			if got[id] != role {
				return fmt.Errorf("team %d members = %v, want %v", teamID, got, want)
			}
		}
		return nil
	}
}

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

func membersConfig(baseURL, members string) string {
	return teamConfig(baseURL, `
  name    = "Payments"
  members = [`+members+`]`)
}

func TestTeamResourceMembersAreAuthoritative(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)
	adaAdminBob := `{ user_id = "7", role = "admin" }, { user_id = "8" }`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: membersConfig(srv.URL, adaAdminBob),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team.test", "members.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("vibeoncall_team.test", "members.*", map[string]string{"user_id": "7", "role": "admin"}),
					resource.TestCheckTypeSetElemNestedAttrs("vibeoncall_team.test", "members.*", map[string]string{"user_id": "8", "role": "member"}),
					api.checkMembers(1, map[int]string{adaID: "admin", bobID: "member"}),
				),
			},
			{
				Config: membersConfig(srv.URL, `{ user_id = "8" }, { user_id = "7", role = "admin" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				PreConfig: func() { api.addMembership(1, carolID, "member") },
				Config:    membersConfig(srv.URL, adaAdminBob),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("vibeoncall_team.test", plancheck.ResourceActionUpdate)},
				},
				Check: api.checkMembers(1, map[int]string{adaID: "admin", bobID: "member"}),
			},
			{
				PreConfig: func() { api.removeMembership(1, bobID) },
				Config:    membersConfig(srv.URL, adaAdminBob),
				Check:     api.checkMembers(1, map[int]string{adaID: "admin", bobID: "member"}),
			},
			{
				Config: membersConfig(srv.URL, `{ user_id = "7", role = "admin" }, { user_id = "8", role = "admin" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("vibeoncall_team.test", plancheck.ResourceActionUpdate)},
				},
				Check: api.checkMembers(1, map[int]string{adaID: "admin", bobID: "admin"}),
			},
			{
				Config: membersConfig(srv.URL, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team.test", "members.#", "0"),
					api.checkMembers(1, map[int]string{}),
				),
			},
			{
				Config: membersConfig(srv.URL, adaAdminBob),
				Check: resource.ComposeAggregateTestCheckFunc(
					api.checkMembers(1, map[int]string{adaID: "admin", bobID: "member"}),
					func(*terraform.State) error {
						api.mu.Lock()
						defer api.mu.Unlock()
						if api.teamPatches != 0 {
							return fmt.Errorf("team PATCHed %d times for member-only changes", api.teamPatches)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestTeamResourceWithoutMembersLeavesMembershipAlone(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: membersConfig(srv.URL, `{ user_id = "7", role = "admin" }`),
				Check:  api.checkMembers(1, map[int]string{adaID: "admin"}),
			},
			{
				Config: teamConfig(srv.URL, `  name = "Payments"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: api.checkMembers(1, map[int]string{adaID: "admin"}),
			},
			{
				PreConfig: func() { api.addMembership(1, bobID, "member") },
				Config:    teamConfig(srv.URL, `  name = "Payments Platform"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team.test", "members.#", "2"),
					api.checkMembers(1, map[int]string{adaID: "admin", bobID: "member"}),
				),
			},
		},
	})
}

func TestTeamResourceImportBringsCurrentMembers(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)
	teamID := api.seedTeam("Payments")
	api.addMembership(teamID, adaID, "admin")
	api.addMembership(teamID, bobID, "member")
	config := membersConfig(srv.URL, `{ user_id = "7", role = "admin" }, { user_id = "8" }`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "vibeoncall_team.test",
				ImportState:        true,
				ImportStateId:      "VP-TEAM-1",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if got := states[0].Attributes["members.#"]; got != "2" {
						return fmt.Errorf("imported members.# = %s, want 2", got)
					}
					return nil
				},
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestTeamResourceKeepsAppliedMembersWhenAnAssignFails(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: membersConfig(srv.URL, `{ user_id = "7", role = "admin" }`),
			},
			{
				Config:      membersConfig(srv.URL, fmt.Sprintf(`{ user_id = "8" }, { user_id = "%d" }`, ghostID)),
				ExpectError: regexp.MustCompile(`Assign team member failed`),
			},
			{
				Config: membersConfig(srv.URL, `{ user_id = "7", role = "admin" }, { user_id = "8" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: api.checkMembers(1, map[int]string{adaID: "admin", bobID: "member"}),
			},
		},
	})
}

func TestTeamResourceKeepsTheTeamWhenCreatingItsMembersFails(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{{
			Config:      membersConfig(srv.URL, fmt.Sprintf(`{ user_id = "7", role = "admin" }, { user_id = "%d" }`, ghostID)),
			ExpectError: regexp.MustCompile(`Assign team member failed`),
		}},
	})
}

func TestTeamResourceRejectsDuplicateMembers(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      membersConfig(srv.URL, `{ user_id = "7" }, { user_id = "7", role = "admin" }`),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`Duplicate team member`),
		}},
	})
}

func TestTeamResourcePlansRemovalOfUnlistedMembersWhenMembersIsFirstSet(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: teamConfig(srv.URL, `  name = "Payments"`),
				Check:  resource.TestCheckResourceAttr("vibeoncall_team.test", "members.#", "0"),
			},
			{
				PreConfig: func() { api.addMembership(1, carolID, "admin") },
				Config:    teamConfig(srv.URL, `  name = "Payments"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckTypeSetElemNestedAttrs("vibeoncall_team.test", "members.*", map[string]string{"user_id": "9", "role": "admin"}),
			},
			{
				Config: membersConfig(srv.URL, `{ user_id = "7", role = "admin" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("vibeoncall_team.test", plancheck.ResourceActionUpdate)},
				},
				Check: api.checkMembers(1, map[int]string{adaID: "admin"}),
			},
		},
	})
}

func TestTeamResourceWithoutMembersCoexistsWithTeamMemberships(t *testing.T) {
	api, srv := newFakeAPI(t, teamMemberUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: teamConfig(srv.URL, `  name = "Payments"`) + `
resource "vibeoncall_team_membership" "ada" {
  team_id = vibeoncall_team.test.id
  user_id = "7"
  role    = "admin"
}
`,
				Check: api.checkMembers(1, map[int]string{adaID: "admin"}),
			},
		},
	})
}
