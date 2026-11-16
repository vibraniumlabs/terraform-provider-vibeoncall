package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

const route = "vibeoncall_alert_route.payments"

func alertRouteConfig(baseURL, routeBody string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_alert_route" "payments" {
  team_id = vibeoncall_team.payments.id
  name    = "Payments"
  %s
}

resource "vibeoncall_alert_source" "datadog" {
  team_id          = vibeoncall_team.payments.id
  name             = "Datadog"
  type             = "datadog"
  alert_routing_id = vibeoncall_alert_route.payments.id
}
`, routeBody)
}

const threeRules = `
  description = "Pages for payment alerts"
  rules = [
    {
      name = "Critical"
      condition_groups = [{
        operator   = "AND"
        conditions = [
          { severity = { operator = "greater_than_or_equal", severity = "SEV1" } },
          { field = { field_path = "details.env", operator = "exact_match", value = "prod", case_sensitive = false } },
        ]
      }]
      escalate = {
        escalation_policy_ids = ["7"]
        urgency               = "HIGH"
      }
    },
    {
      name = "Flapping"
      condition_groups = [
        { operator = "AND", conditions = [{ count = { threshold = 5, timeframe_minutes = 10 } }] },
        { operator = "OR", conditions = [{ provider = { providers = ["sentry"] } }] },
      ]
      escalate = {
        schedule_ids  = ["9", "3"]
        user_ids      = ["4"]
        delay_minutes = 15
      }
    },
    {
      name       = "Everything else"
      do_nothing = true
    },
  ]
`

func TestAlertRouteResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: alertRouteConfig(srv.URL, threeRules),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(route, "id", "1"),
					resource.TestCheckResourceAttr(route, "team_id", "1"),
					resource.TestCheckResourceAttr(route, "enabled", "true"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "alert_routing_id", "1"),
					resource.TestCheckResourceAttr(route, "rules.#", "3"),
					resource.TestCheckResourceAttr(route, "rules.0.name", "Critical"),
					resource.TestCheckResourceAttr(route, "rules.0.condition_groups.0.conditions.1.field.case_sensitive", "false"),
					resource.TestCheckResourceAttr(route, "rules.0.escalate.urgency", "HIGH"),
					resource.TestCheckNoResourceAttr(route, "rules.0.escalate.delay_minutes"),
					resource.TestCheckResourceAttr(route, "rules.1.condition_groups.#", "2"),
					resource.TestCheckResourceAttr(route, "rules.1.condition_groups.0.conditions.0.count.timeframe_minutes", "10"),
					resource.TestCheckResourceAttr(route, "rules.1.escalate.schedule_ids.#", "2"),
					resource.TestCheckResourceAttr(route, "rules.1.escalate.delay_minutes", "15"),
					resource.TestCheckResourceAttr(route, "rules.2.do_nothing", "true"),
					resource.TestCheckNoResourceAttr(route, "rules.2.condition_groups"),
					api.checkRoutingRules(1, `[
						{"name":"Critical","logicalOperator":"AND","conditions":[
							{"type":"severity","rule":{"operator":"greater_than_or_equal","severity":"SEV1"}},
							{"type":"field","rule":{"fieldPath":"details.env","operator":"exact_match","conditionValue":"prod","isCaseSensitive":false}}
						],"action":{"type":"ESCALATE","value":{"escalation":[{"type":"escalationPolicy","value":7}],"notificationUrgency":"HIGH"}}},
						{"name":"Flapping","logicalOperator":"OR","conditions":[
							{"type":"group","operator":"AND","conditions":[{"type":"count","rule":{"countThreshold":5,"timeframe":600000}}]},
							{"type":"group","operator":"OR","conditions":[{"type":"provider","rule":{"operator":"one_of","providers":["sentry"]}}]}
						],"action":{"type":"ESCALATE","value":{"escalation":[{"type":"schedule","value":3},{"type":"schedule","value":9},{"type":"user","value":4}],"delayMs":900000}}},
						{"name":"Everything else","logicalOperator":"AND","conditions":[],"action":{"type":"DO_NOTHING"}}
					]`),
				),
			},
			{
				Config: alertRouteConfig(srv.URL, `
  enabled = false
  rules = [
    {
      do_nothing = true
      condition_groups = [{ operator = "OR", conditions = [{ severity = { operator = "equal_to", severity = "SIGNAL" } }] }]
    },
    {
      name     = "Critical"
      escalate = { escalation_policy_ids = ["8"] }
    },
  ]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(route, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(route, "id", "1"),
					resource.TestCheckResourceAttr(route, "enabled", "false"),
					resource.TestCheckNoResourceAttr(route, "description"),
					resource.TestCheckResourceAttr(route, "rules.#", "2"),
					resource.TestCheckNoResourceAttr(route, "rules.0.name"),
					resource.TestCheckResourceAttr(route, "rules.0.do_nothing", "true"),
					resource.TestCheckResourceAttr(route, "rules.1.escalate.escalation_policy_ids.0", "8"),
				),
			},
			{
				ResourceName:      route,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: alertRouteConfig(srv.URL, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(route, "rules"),
					resource.TestCheckResourceAttr(route, "enabled", "true"),
					api.checkRoutingRules(1, `[]`),
				),
			},
		},
	})
}

func TestAlertRouteResourceRulesChangedInAppAreRestored(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := alertRouteConfig(srv.URL, threeRules)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					api.setRoutingRules(1, `[{"name":null,"logicalOperator":"AND","conditions":[],"action":{"type":"DO_NOTHING"}}]`)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(route, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(route, "rules.#", "3"),
			},
			{
				PreConfig: func() {
					api.setRoutingRules(1, `[{"name":"Nested","logicalOperator":"OR","conditions":`+nestedGroupConditions+`,"action":{"type":"DO_NOTHING"}}]`)
				},
				Config: config,
				Check:  resource.TestCheckResourceAttr(route, "rules.0.name", "Critical"),
			},
		},
	})
}

func TestAlertRouteResourceImportRejectsRulesItCannotExpress(t *testing.T) {
	for name, tc := range map[string]struct{ rules, want string }{
		"nested groups": {
			`[{"name":"Nested","logicalOperator":"OR","conditions":` + nestedGroupConditions + `,"action":{"type":"DO_NOTHING"}}]`,
			`condition\s+shapes\s+the\s+app\s+builds`,
		},
		"no conditions joined by OR": {
			`[{"name":"Never","logicalOperator":"OR","conditions":[],"action":{"type":"DO_NOTHING"}}]`,
			`never\s+matches`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			api, srv := newFakeAPI(t)
			api.seedRouting(5, nil)
			api.setRoutingRules(5, tc.rules)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:        providerConfig(srv.URL) + `resource "vibeoncall_alert_route" "x" { name = "Seeded 5" }`,
					ResourceName:  "vibeoncall_alert_route.x",
					ImportState:   true,
					ImportStateId: "5",
					ExpectError:   regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}

func TestAlertRouteResourceDeleteWhileInUseFails(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := providerConfig(srv.URL) + `resource "vibeoncall_alert_route" "shared" { name = "Shared" }`
	var sourceID int

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:   func() { sourceID = api.addSourceUsing(1) },
				Config:      providerConfig(srv.URL),
				ExpectError: regexp.MustCompile(`still sends alerts to routing 1`),
			},
			{
				PreConfig: func() { api.removeSource(sourceID) },
				Config:    providerConfig(srv.URL),
			},
		},
	})
}

func TestAlertRouteResourceRemovedOutsideTerraform(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := providerConfig(srv.URL) + `resource "vibeoncall_alert_route" "shared" { name = "Shared" }`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { api.removeRouting(1) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAlertRouteResourceValidation(t *testing.T) {
	_, srv := newFakeAPI(t)
	severity := `condition_groups = [{ operator = "AND", conditions = [{ severity = { operator = "equal_to", severity = %q } }] }]`

	for name, tc := range map[string]struct {
		rule string
		want string
	}{
		"no action":                  {`name = "X"`, `No attribute specified when one \(and only one\)`},
		"both actions":               {`do_nothing = true` + "\n" + `escalate = { user_ids = ["1"] }`, `2 attributes specified when one \(and only one\)`},
		"do_nothing false":           {`do_nothing = false`, `do_nothing must be true when set`},
		"escalate without targets":   {`escalate = { urgency = "HIGH" }`, `Missing escalation target`},
		"empty target set":           {`escalate = { user_ids = [] }`, `set must contain at least 1 elements`},
		"severity alias":             {`do_nothing = true` + "\n" + fmt.Sprintf(severity, "P1"), `must be one of`},
		"count threshold below two":  {`do_nothing = true` + "\n" + `condition_groups = [{ operator = "AND", conditions = [{ count = { threshold = 1, timeframe_minutes = 5 } }] }]`, `must be at least 2`},
		"two kinds in one condition": {`do_nothing = true` + "\n" + `condition_groups = [{ operator = "AND", conditions = [{ count = { threshold = 2, timeframe_minutes = 5 }, severity = { operator = "equal_to", severity = "SEV1" } }] }]`, `2 attributes specified when one \(and only one\)`},
		"delay beyond a day":         {`escalate = { user_ids = ["1"], delay_minutes = 1441 }`, `between 1 and 1440`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      providerConfig(srv.URL) + "resource \"vibeoncall_alert_route\" \"x\" {\nname = \"X\"\nrules = [{\n" + tc.rule + "\n}]\n}",
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
	t.Run("null rule", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6ProviderFactories(),
			Steps: []resource.TestStep{{
				Config:      providerConfig(srv.URL) + "resource \"vibeoncall_alert_route\" \"x\" {\nname = \"X\"\nrules = [null, { do_nothing = true }]\n}",
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`null value`),
			}},
		})
	})
	t.Run("fallback rule before another rule", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6ProviderFactories(),
			Steps: []resource.TestStep{{
				Config:      providerConfig(srv.URL) + "resource \"vibeoncall_alert_route\" \"x\" {\nname = \"X\"\nrules = [{ do_nothing = true }, { do_nothing = true }]\n}",
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Fallback rule is not last`),
			}},
		})
	})
	t.Run("empty rules list", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6ProviderFactories(),
			Steps: []resource.TestStep{{
				Config:      providerConfig(srv.URL) + `resource "vibeoncall_alert_route" "x" { ` + "\n" + `name = "X"` + "\n" + `rules = []` + "\n}",
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`list must contain at least 1 elements`),
			}},
		})
	})
}

func TestAlertRouteResourceMovesTeamWithItsSources(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := func(team string) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "billing" {
  name = "Billing"
}

resource "vibeoncall_alert_route" "payments" {
  team_id = vibeoncall_team.%[1]s.id
  name    = "Payments"
  rules   = [{ do_nothing = true }]
}

resource "vibeoncall_alert_source" "datadog" {
  team_id          = vibeoncall_team.%[1]s.id
  name             = "Datadog"
  type             = "datadog"
  alert_routing_id = vibeoncall_alert_route.payments.id

  lifecycle {
    create_before_destroy = true
  }
}
`, team)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config("payments")},
			{
				Config: config("billing"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(route, plancheck.ResourceActionCreateBeforeDestroy)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(route, "id", "2"),
					resource.TestCheckResourceAttrPair(route, "team_id", "vibeoncall_team.billing", "id"),
					resource.TestCheckResourceAttrPair("vibeoncall_alert_source.datadog", "alert_routing_id", route, "id"),
				),
			},
		},
	})
}
