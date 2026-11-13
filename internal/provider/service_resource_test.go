package provider

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var serviceTestUsers = []fakeUser{
	{ID: 7, Email: "ada@example.com", Status: "active"},
	{ID: 8, Email: "grace@example.com", Status: "active"},
}

func (a *fakeAPI) checkFirstServiceResponders(want []fakeResponderRow) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if got := a.services[1].responders; !slices.Equal(got, want) {
			return fmt.Errorf("service 1 responder rows = %+v, want %+v", got, want)
		}
		return nil
	}
}

func serviceConfig(baseURL, service string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "platform" {
  name       = "Platform"
  depends_on = [vibeoncall_team.payments]
}

resource "vibeoncall_team" "search" {
  name       = "Search"
  depends_on = [vibeoncall_team.platform]
}

resource "vibeoncall_service" "checkout" {
  name = "Checkout"
  %s
}
`, service)
}

func TestServiceResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t, serviceTestUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: serviceConfig(srv.URL, `
  description          = "Takes payments"
  team_id              = vibeoncall_team.payments.id
  external_provider    = "backstage"
  external_service_id  = "component:checkout"
  responder_team_ids   = [vibeoncall_team.platform.id]
  responder_user_ids   = ["7"]
  stakeholder_user_ids = ["8"]
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_service.checkout", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_service.checkout", "team_id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_service.checkout", "external_provider", "backstage"),
					resource.TestCheckTypeSetElemAttr("vibeoncall_service.checkout", "responder_team_ids.*", "2"),
					resource.TestCheckTypeSetElemAttr("vibeoncall_service.checkout", "stakeholder_user_ids.*", "8"),
					api.checkFirstServiceResponders([]fakeResponderRow{{TeamID: 2}, {UserID: 7}}),
				),
			},
			{
				Config: serviceConfig(srv.URL, `
  team_id              = vibeoncall_team.payments.id
  responder_team_ids   = [vibeoncall_team.search.id]
  responder_user_ids   = ["8", "7"]
  stakeholder_user_ids = ["7", "8"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_service.checkout", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "description"),
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "external_provider"),
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "external_service_id"),
					resource.TestCheckResourceAttr("vibeoncall_service.checkout", "stakeholder_user_ids.#", "2"),
					api.checkFirstServiceResponders([]fakeResponderRow{{UserID: 7}, {TeamID: 3}, {UserID: 8}}),
				),
			},
			{
				ResourceName:      "vibeoncall_service.checkout",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: serviceConfig(srv.URL, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "team_id"),
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "responder_team_ids"),
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "responder_user_ids"),
					resource.TestCheckNoResourceAttr("vibeoncall_service.checkout", "stakeholder_user_ids"),
					api.checkFirstServiceResponders(nil),
				),
			},
			{
				PreConfig:          func() { api.removeService(1) },
				Config:             serviceConfig(srv.URL, ``),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestServiceResourceOwnerCanAlsoBeAResponder(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: serviceConfig(srv.URL, `
  team_id            = vibeoncall_team.payments.id
  responder_team_ids = [vibeoncall_team.payments.id, vibeoncall_team.platform.id]
`),
				Check: api.checkFirstServiceResponders([]fakeResponderRow{{TeamID: 1}, {TeamID: 2}}),
			},
			{
				Config: serviceConfig(srv.URL, `
  team_id            = vibeoncall_team.platform.id
  responder_team_ids = [vibeoncall_team.payments.id, vibeoncall_team.platform.id]
`),
				Check: api.checkFirstServiceResponders([]fakeResponderRow{{TeamID: 1}, {TeamID: 2}}),
			},
			{
				Config: serviceConfig(srv.URL, `
  team_id            = vibeoncall_team.platform.id
  responder_team_ids = [vibeoncall_team.payments.id]
`),
				Check: api.checkFirstServiceResponders([]fakeResponderRow{{TeamID: 1}}),
			},
		},
	})
}

func TestServiceResourceOwnerChangedInAppKeepsResponders(t *testing.T) {
	api, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: serviceConfig(srv.URL, `
  team_id            = vibeoncall_team.payments.id
  responder_team_ids = [vibeoncall_team.platform.id]
`),
			},
			{
				PreConfig: func() { api.setServiceOwner(1, 2) },
				Config: serviceConfig(srv.URL, `
  team_id            = vibeoncall_team.platform.id
  responder_team_ids = [vibeoncall_team.platform.id]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: serviceConfig(srv.URL, `team_id = vibeoncall_team.search.id`),
				Check:  api.checkFirstServiceResponders(nil),
			},
		},
	})
}

func TestServiceResourceRequiresExternalIdentityTogether(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: providerConfig(srv.URL) + `
resource "vibeoncall_service" "x" {
  name              = "X"
  external_provider = "backstage"
}
`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`must be configured together`),
		}},
	})
}

func TestServiceResourcePartialFailureKeepsAppliedChanges(t *testing.T) {
	api, srv := newFakeAPI(t, serviceTestUsers...)
	config := func(members string) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_service" "x" {
  name = "X"
  %s
}
`, members)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config(``)},
			{
				Config:      config(`responder_user_ids = ["7"]` + "\n" + `stakeholder_user_ids = ["99"]`),
				ExpectError: regexp.MustCompile(`Add service stakeholders failed`),
			},
			{
				Config: config(`responder_user_ids = ["7"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestServiceResourceRejectsMalformedImportID(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        providerConfig(srv.URL) + `resource "vibeoncall_service" "x" { name = "X" }`,
			ResourceName:  "vibeoncall_service.x",
			ImportState:   true,
			ImportStateId: "Checkout",
			ExpectError:   regexp.MustCompile(`Invalid import ID`),
		}},
	})
}

func TestServiceResourceFailedCreateIsReplaced(t *testing.T) {
	api, srv := newFakeAPI(t, serviceTestUsers...)
	config := func(stakeholder string) string {
		return providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_service" "x" {
  name                 = "X"
  stakeholder_user_ids = [%q]
}
`, stakeholder)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config:      config("99"),
				ExpectError: regexp.MustCompile(`Add service stakeholders failed`),
			},
			{
				Config: config("8"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_service.x", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.TestCheckResourceAttr("vibeoncall_service.x", "id", "2"),
			},
		},
	})
}

func TestServiceResourceValidation(t *testing.T) {
	_, srv := newFakeAPI(t)

	for name, tc := range map[string]struct {
		attrs string
		want  string
	}{
		"team_id beyond int32":             {`name = "X"` + "\n" + `team_id = "4294967298"`, `at most 2147483647`},
		"responder beyond int32":           {`name = "X"` + "\n" + `responder_user_ids = ["2147483648"]`, `at most 2147483647`},
		"name over 255 characters":         {`name = "` + strings.Repeat("服", 256) + `"`, `character count must be at most 255`},
		"name of 255 multibyte characters": {`name = "` + strings.Repeat("服", 255) + `"`, ``},
	} {
		t.Run(name, func(t *testing.T) {
			step := resource.TestStep{
				Config:             providerConfig(srv.URL) + "resource \"vibeoncall_service\" \"x\" {\n" + tc.attrs + "\n}",
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			}
			if tc.want != "" {
				step.ExpectError = regexp.MustCompile(tc.want)
			}
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: protoV6ProviderFactories(), Steps: []resource.TestStep{step}})
		})
	}
}
