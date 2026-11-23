package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func alertSourceConfig(baseURL, source string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_alert_source" "datadog" {
  team_id = vibeoncall_team.payments.id
  %s
}
`, source)
}

func seedTeamRoutings(api *fakeAPI) {
	teamID := 1
	api.seedRouting(10, &teamID)
	api.seedRouting(11, &teamID)
	api.seedRouting(20, nil)
}

func TestAlertSourceResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedTeamRoutings(api)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: alertSourceConfig(srv.URL, `
  name             = "Datadog production"
  type             = "datadog"
  alert_routing_id = "10"
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "team_id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "alert_routing_id", "10"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "enabled", "true"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "webhook_url", "https://api.example.com/webhooks/datadog/token-1"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "webhook_token", "token-1"),
					resource.TestCheckNoResourceAttr("vibeoncall_alert_source.datadog", "email_address"),
				),
			},
			{
				Config: alertSourceConfig(srv.URL, `
  name             = "Datadog prod"
  type             = "datadog"
  alert_routing_id = "11"
  enabled          = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_alert_source.datadog", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "name", "Datadog prod"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "alert_routing_id", "11"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "enabled", "false"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "webhook_token", "token-1"),
				),
			},
			{
				ResourceName:      "vibeoncall_alert_source.datadog",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: alertSourceConfig(srv.URL, `
  name = "Datadog prod"
  type = "datadog"
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_alert_source.datadog", "alert_routing_id"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "enabled", "true"),
				),
			},
			{
				PreConfig:          func() { api.removeSource(1) },
				Config:             alertSourceConfig(srv.URL, `name = "Datadog prod"`+"\n"+`type = "datadog"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAlertSourceResourceTypeChangeReplaces(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: alertSourceConfig(srv.URL, `name = "Alerts"`+"\n"+`type = "datadog"`)},
			{
				Config: alertSourceConfig(srv.URL, `name = "Alerts"`+"\n"+`type = "email"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_alert_source.datadog", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "id", "2"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "email_address", "source-2@alerts.example.com"),
					resource.TestCheckNoResourceAttr("vibeoncall_alert_source.datadog", "webhook_url"),
					resource.TestCheckNoResourceAttr("vibeoncall_alert_source.datadog", "webhook_token"),
				),
			},
		},
	})
}

func TestAlertSourceResourceJira(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedTeamRoutings(api)
	const source = "vibeoncall_alert_source.jira"
	config := func(name, routing string, enabled bool) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}
resource "vibeoncall_alert_source" "jira" {
  name             = %q
  type             = "jira"
  team_id          = vibeoncall_team.payments.id
  alert_routing_id = %q
  enabled          = %t
}
`, name, routing, enabled)
	}
	updated := config("Jira issues renamed", "11", false)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: config("Jira issues", "10", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(source, "type", "jira"),
					resource.TestCheckResourceAttr(source, "alert_routing_id", "10"),
					resource.TestCheckResourceAttr(source, "webhook_url", "https://api.example.com/webhooks/jira/token-1"),
					resource.TestCheckResourceAttr(source, "webhook_token", "token-1"),
					resource.TestCheckNoResourceAttr(source, "email_address"),
				),
			},
			{
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(source, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(source, "id", "1"),
					resource.TestCheckResourceAttr(source, "type", "jira"),
					resource.TestCheckResourceAttr(source, "name", "Jira issues renamed"),
					resource.TestCheckResourceAttr(source, "alert_routing_id", "11"),
					resource.TestCheckResourceAttr(source, "enabled", "false"),
					resource.TestCheckResourceAttr(source, "webhook_url", "https://api.example.com/webhooks/jira/token-1"),
					resource.TestCheckResourceAttr(source, "webhook_token", "token-1"),
				),
			},
			{ResourceName: source, ImportState: true, ImportStateVerify: true},
			{Config: updated, PlanOnly: true},
		},
	})
}

func TestAlertSourceResourcePagerDuty(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{{
			Config: alertSourceConfig(srv.URL, `name = "PagerDuty incidents"`+"\n"+`type = "pagerduty"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "type", "pagerduty"),
				resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "webhook_url", "https://api.example.com/webhooks/pagerduty/token-1"),
				resource.TestCheckNoResourceAttr("vibeoncall_alert_source.datadog", "email_address"),
			),
		}},
	})
}

func TestAlertSourceResourceDisabledInAppIsReEnabled(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := alertSourceConfig(srv.URL, `name = "Alerts"`+"\n"+`type = "grafana"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() { api.setSourceEnabled(1, false) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_alert_source.datadog", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("vibeoncall_alert_source.datadog", "enabled", "true"),
			},
		},
	})
}

func TestAlertSourceResourceOrganizationLevel(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedTeamRoutings(api)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL) + `
resource "vibeoncall_alert_source" "shared" {
  name             = "Shared"
  type             = "http"
  alert_routing_id = "20"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_alert_source.shared", "team_id"),
					resource.TestCheckResourceAttr("vibeoncall_alert_source.shared", "alert_routing_id", "20"),
				),
			},
		},
	})
}

func TestAlertSourceResourceRejectsRoutingOutsideItsTeam(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedTeamRoutings(api)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{{
			Config:      alertSourceConfig(srv.URL, `name = "Alerts"`+"\n"+`type = "datadog"`+"\n"+`alert_routing_id = "20"`),
			ExpectError: regexp.MustCompile(`outside the source scope`),
		}},
	})
}

func TestAlertSourceResourceRejectsMalformedImportID(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        providerConfig(srv.URL) + `resource "vibeoncall_alert_source" "x" { ` + "\n" + `name = "X"` + "\n" + `type = "datadog"` + "\n}",
			ResourceName:  "vibeoncall_alert_source.x",
			ImportState:   true,
			ImportStateId: "Datadog",
			ExpectError:   regexp.MustCompile(`Invalid import ID`),
		}},
	})
}

func TestAlertSourceResourceValidation(t *testing.T) {
	_, srv := newFakeAPI(t)

	for name, tc := range map[string]struct {
		attrs string
		want  string
	}{
		"unknown type":              {`name = "X"` + "\n" + `type = "zabbix"`, `value must be one of`},
		"heartbeat is not a source": {`name = "X"` + "\n" + `type = "heartbeat"`, `value must be one of`},
		"padded name":               {`name = " X"` + "\n" + `type = "datadog"`, `must not be empty or start or end with whitespace`},
		"routing beyond int32":      {`name = "X"` + "\n" + `type = "datadog"` + "\n" + `alert_routing_id = "2147483648"`, `at most 2147483647`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      providerConfig(srv.URL) + "resource \"vibeoncall_alert_source\" \"x\" {\n" + tc.attrs + "\n}",
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}
