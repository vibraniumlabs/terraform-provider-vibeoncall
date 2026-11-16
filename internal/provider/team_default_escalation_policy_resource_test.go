package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func (a *fakeAPI) checkPaymentsDefault(want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if got := a.teamDefaults[1]; got != want {
			return fmt.Errorf("payments default policy = %d, want %d", got, want)
		}
		return nil
	}
}

func teamDefaultConfig(baseURL, defaultBlock string) string {
	return providerConfig(baseURL) + `
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_escalation_policy" "primary" {
  name    = "Primary"
  team_id = vibeoncall_team.payments.id
  tiers   = [{ acknowledgement_timeout_seconds = 300, user_ids = ["7"] }]
}

resource "vibeoncall_escalation_policy" "secondary" {
  name       = "Secondary"
  team_id    = vibeoncall_team.payments.id
  tiers      = [{ acknowledgement_timeout_seconds = 300, user_ids = ["7"] }]
  depends_on = [vibeoncall_escalation_policy.primary]
}
` + defaultBlock
}

func defaultPolicy(policy string) string {
	return fmt.Sprintf(`
resource "vibeoncall_team_default_escalation_policy" "payments" {
  team_id              = vibeoncall_team.payments.id
  escalation_policy_id = vibeoncall_escalation_policy.%s.id
}
`, policy)
}

func TestTeamDefaultEscalationPolicyResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: teamDefaultConfig(srv.URL, defaultPolicy("primary")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_team_default_escalation_policy.payments", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_team_default_escalation_policy.payments", "escalation_policy_id", "1"),
					api.checkPaymentsDefault(1),
				),
			},
			{
				Config: teamDefaultConfig(srv.URL, defaultPolicy("secondary")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_team_default_escalation_policy.payments", plancheck.ResourceActionUpdate),
					},
				},
				Check: api.checkPaymentsDefault(2),
			},
			{
				ResourceName:      "vibeoncall_team_default_escalation_policy.payments",
				ImportState:       true,
				ImportStateId:     "1",
				ImportStateVerify: true,
			},
			{
				PreConfig:          func() { api.setTeamDefault(1, 1) },
				Config:             teamDefaultConfig(srv.URL, defaultPolicy("secondary")),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: teamDefaultConfig(srv.URL, defaultPolicy("secondary")),
				Check:  api.checkPaymentsDefault(2),
			},
			{
				PreConfig: func() { api.setTeamDefault(1, 0) },
				Config:    teamDefaultConfig(srv.URL, defaultPolicy("secondary")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_team_default_escalation_policy.payments", plancheck.ResourceActionCreate),
					},
				},
				Check: api.checkPaymentsDefault(2),
			},
			{
				Config: teamDefaultConfig(srv.URL, ""),
				Check:  api.checkPaymentsDefault(0),
			},
		},
	})
}

func TestTeamDefaultEscalationPolicyResourceRejectsPolicyOutsideTeam(t *testing.T) {
	for name, tc := range map[string]struct {
		policy string
		want   string
	}{
		"other team": {
			policy: `team_id = vibeoncall_team.platform.id`,
			want:   `Escalation policy 1 belongs to team 2, not team 1`,
		},
		"global": {
			policy: ``,
			want:   `Escalation policy 1 has no team`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			api, srv := newFakeAPI(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				CheckDestroy:             api.checkNothingLeft,
				Steps: []resource.TestStep{{
					Config: providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "platform" {
  name       = "Platform"
  depends_on = [vibeoncall_team.payments]
}

resource "vibeoncall_escalation_policy" "p" {
  name  = "P"
  tiers = [{ acknowledgement_timeout_seconds = 300, user_ids = ["7"] }]
  %s
}

resource "vibeoncall_team_default_escalation_policy" "payments" {
  team_id              = vibeoncall_team.payments.id
  escalation_policy_id = vibeoncall_escalation_policy.p.id
}
`, tc.policy),
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}

func TestTeamDefaultEscalationPolicyResourceImportWithoutDefaultFails(t *testing.T) {
	_, srv := newFakeAPI(t)
	config := providerConfig(srv.URL) + `
resource "vibeoncall_team" "payments" {
  name = "Payments"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: config},
			{
				Config: config + `
resource "vibeoncall_team_default_escalation_policy" "payments" {
  team_id              = "1"
  escalation_policy_id = "1"
}
`,
				ResourceName:  "vibeoncall_team_default_escalation_policy.payments",
				ImportState:   true,
				ImportStateId: "1",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}
