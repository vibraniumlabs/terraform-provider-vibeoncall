package provider

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func (a *fakeAPI) checkPolicyTiers(id int, want []fakeTier) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		p := a.policies[id]
		if p == nil {
			return fmt.Errorf("policy %d not found", id)
		}
		got := make([]fakeTier, len(p.Tiers))
		for i, t := range p.Tiers {
			t.ID = 0
			got[i] = t
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("policy %d tiers = %+v, want %+v", id, got, want)
		}
		return nil
	}
}

func (a *fakeAPI) checkFirstTierID(policyID, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if got := a.policies[policyID].Tiers[0].ID; got != want {
			return fmt.Errorf("policy %d first tier ID = %d, want %d", policyID, got, want)
		}
		return nil
	}
}

func escalationPolicyConfig(baseURL, team, policy string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "platform" {
  name       = "Platform"
  depends_on = [vibeoncall_team.payments]
}

resource "vibeoncall_escalation_policy" "primary" {
  name    = "Primary"
  team_id = vibeoncall_team.%s.id
  %s
}
`, team, policy)
}

const scheduleThenUser = `
  tiers = [
    {
      acknowledgement_timeout_seconds = 300
      schedule_targets                = [{ schedule_id = "11" }]
    },
    {
      acknowledgement_timeout_seconds = 600
      user_ids                        = ["7"]
    },
  ]
`

const userThenRotationAndSchedule = `
  description = "Payments on-call"
  tiers = [
    {
      acknowledgement_timeout_seconds = 90
      user_ids                        = ["8", "7"]
    },
    {
      acknowledgement_timeout_seconds = 300
      rotation_targets                = [{ schedule_id = "11", rotation_id = "21", mode = "next" }]
      schedule_targets                = [{ schedule_id = "12", mode = "previous" }, { schedule_id = "11" }]
    },
  ]
`

func TestEscalationPolicyResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: escalationPolicyConfig(srv.URL, "payments", scheduleThenUser),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.primary", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.primary", "team_id", "1"),
					resource.TestCheckNoResourceAttr("vibeoncall_escalation_policy.primary", "description"),
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.primary", "tiers.0.schedule_targets.0.mode", "current"),
					api.checkPolicyTiers(1, []fakeTier{
						{AcknowledgementTimeThreshold: 300_000, ScheduleTargets: []fakeScheduleTarget{{ScheduleID: 11, Mode: "current"}}, RotationTargets: []fakeRotationTarget{}, UserIDs: []int{}},
						{AcknowledgementTimeThreshold: 600_000, ScheduleTargets: []fakeScheduleTarget{}, RotationTargets: []fakeRotationTarget{}, UserIDs: []int{7}},
					}),
				),
			},
			{
				Config: escalationPolicyConfig(srv.URL, "payments", userThenRotationAndSchedule),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_escalation_policy.primary", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.primary", "description", "Payments on-call"),
					api.checkPolicyTiers(1, []fakeTier{
						{AcknowledgementTimeThreshold: 90_000, ScheduleTargets: []fakeScheduleTarget{}, RotationTargets: []fakeRotationTarget{}, UserIDs: []int{8, 7}},
						{
							AcknowledgementTimeThreshold: 300_000,
							ScheduleTargets:              []fakeScheduleTarget{{ScheduleID: 12, Mode: "previous"}, {ScheduleID: 11, Mode: "current"}},
							RotationTargets:              []fakeRotationTarget{{ScheduleID: 11, RotationID: 21, Mode: "next"}},
							UserIDs:                      []int{},
						},
					}),
				),
			},
			{
				ResourceName:      "vibeoncall_escalation_policy.primary",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: escalationPolicyConfig(srv.URL, "payments", scheduleThenUser),
				Check:  resource.TestCheckNoResourceAttr("vibeoncall_escalation_policy.primary", "description"),
			},
			{
				Config: escalationPolicyConfig(srv.URL, "platform", scheduleThenUser),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_escalation_policy.primary", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.primary", "id", "2"),
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.primary", "team_id", "2"),
				),
			},
			{
				PreConfig:          func() { api.removePolicy(2) },
				Config:             escalationPolicyConfig(srv.URL, "platform", scheduleThenUser),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestEscalationPolicyResourceGlobalPolicy(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL) + `
resource "vibeoncall_escalation_policy" "global" {
  name  = "Global"
  tiers = [{ acknowledgement_timeout_seconds = 300, user_ids = ["7"] }]
}
`,
				Check: resource.TestCheckNoResourceAttr("vibeoncall_escalation_policy.global", "team_id"),
			},
			{
				ResourceName:      "vibeoncall_escalation_policy.global",
				ImportState:       true,
				ImportStateId:     "1",
				ImportStateVerify: true,
			},
		},
	})
}

func TestEscalationPolicyResourceDeleteInUseFails(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := escalationPolicyConfig(srv.URL, "payments", scheduleThenUser)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:   func() { api.setPolicyInUse(1, true) },
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Escalation policy is still in use.*routing rule or automation`),
			},
			{
				PreConfig: func() { api.setPolicyInUse(1, false) },
				Config:    config,
			},
		},
	})
}

func TestEscalationPolicyResourceRenameKeepsTiers(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := func(name string) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_escalation_policy" "p" {
  name  = %q
  tiers = [{ acknowledgement_timeout_seconds = 300, user_ids = ["7"] }]
}
`, name)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config("Before")},
			{
				Config: config("After"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_escalation_policy.p", "name", "After"),
					api.checkFirstTierID(1, 1),
				),
			},
		},
	})
}

func TestEscalationPolicyResourceRejectsTierWithoutTargets(t *testing.T) {
	_, srv := newFakeAPI(t)

	for name, tc := range map[string]struct {
		tier string
		want string
	}{
		"no target attributes": {`{ acknowledgement_timeout_seconds = 300 }`, `At least one attribute out of`},
		"empty target list":    {`{ acknowledgement_timeout_seconds = 300, user_ids = [] }`, `at least 1`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      providerConfig(srv.URL) + fmt.Sprintf("resource \"vibeoncall_escalation_policy\" \"x\" {\nname = \"X\"\ntiers = [%s]\n}", tc.tier),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}

func TestEscalationPolicyResourceRejectsMalformedImportID(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        providerConfig(srv.URL) + `resource "vibeoncall_escalation_policy" "x" {` + "\n" + `name = "X"` + "\n" + `tiers = [{ acknowledgement_timeout_seconds = 300, user_ids = ["7"] }]` + "\n}",
			ResourceName:  "vibeoncall_escalation_policy.x",
			ImportState:   true,
			ImportStateId: "Primary",
			ExpectError:   regexp.MustCompile(`Invalid import ID`),
		}},
	})
}
