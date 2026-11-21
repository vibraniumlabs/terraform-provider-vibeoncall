package provider

import (
	"fmt"
	"regexp"
	"strings"
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
		"unknown type":              {`name = "X"` + "\n" + `type = "pagerduty"`, `value must be one of`},
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

func TestAlertSourceResourceEmailDeduplication(t *testing.T) {
	api, srv := newFakeAPI(t)
	config := func(setting string) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_alert_source" "email" {
  name = "Email alerts"
  type = "email"
  %s
}
`, setting)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config(""), Check: resource.TestCheckNoResourceAttr("vibeoncall_alert_source.email", "deduplication")},
			{Config: config(`deduplication = ""`), Check: resource.TestCheckResourceAttr("vibeoncall_alert_source.email", "deduplication", "")},
			{ResourceName: "vibeoncall_alert_source.email", ImportState: true, ImportStateVerify: true},
			{Config: config(`deduplication = "{{subject}}-{{sender}}"`), Check: resource.TestCheckResourceAttr("vibeoncall_alert_source.email", "deduplication", "{{subject}}-{{sender}}")},
			{ResourceName: "vibeoncall_alert_source.email", ImportState: true, ImportStateVerify: true},
			{Config: config(""), Check: resource.TestCheckNoResourceAttr("vibeoncall_alert_source.email", "deduplication")},
			{Config: config(""), PlanOnly: true, ExpectNonEmptyPlan: false},
			{Config: config(`deduplication = "literal"`), Check: resource.TestCheckResourceAttr("vibeoncall_alert_source.email", "deduplication", "literal")},
			{Config: config(`deduplication = null`), Check: resource.TestCheckNoResourceAttr("vibeoncall_alert_source.email", "deduplication")},
			{ResourceName: "vibeoncall_alert_source.email", ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestAlertSourceResourceDefaultDeduplicationImport(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.sources[77] = &fakeSource{ID: 77, Name: "Existing email", Provider: "email", IsEnabled: true}
	config := func(name string) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_alert_source" "email" {
  name = %q
  type = "email"
}
`, name)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config("Existing email"), ResourceName: "vibeoncall_alert_source.email", ImportState: true, ImportStateId: "77", ImportStatePersist: true},
			{Config: config("Existing email"), PlanOnly: true, ExpectNonEmptyPlan: false},
			{Config: config("Renamed email"), Check: resource.TestCheckNoResourceAttr("vibeoncall_alert_source.email", "deduplication")},
		},
	})
}

func TestAlertSourceResourceRejectsInvalidDeduplication(t *testing.T) {
	_, srv := newFakeAPI(t)
	for _, tc := range []struct{ provider, setting, error string }{
		{"datadog", `deduplication = ""`, "email sources only"},
		{"email", `deduplication = "` + strings.Repeat("a", 501) + `"`, "500"},
		{"email", `deduplication = "` + strings.Repeat("😀", 251) + `"`, "500"},
	} {
		t.Run(tc.provider+tc.setting, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config: providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_alert_source" "source" {
  name = "Alert source"
  type = %q
  %s
}
`, tc.provider, tc.setting),
					ExpectError: regexp.MustCompile(tc.error),
				}},
			})
		})
	}
}
