package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

func (a *fakeAPI) checkWindowConditions(wantOperator, wantConditions string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if len(a.windows) != 1 {
			return fmt.Errorf("%d windows, want 1", len(a.windows))
		}
		var mw *fakeWindow
		for _, w := range a.windows {
			mw = w
		}
		id := mw.ID
		gotOperator := ""
		if mw.Operator != nil {
			gotOperator = *mw.Operator
		}
		if gotOperator != wantOperator {
			return fmt.Errorf("window %d operator = %q, want %q", id, gotOperator, wantOperator)
		}
		var got, want any
		if mw.Conditions != nil {
			if err := json.Unmarshal(mw.Conditions, &got); err != nil {
				return err
			}
		}
		if wantConditions != "" {
			if err := json.Unmarshal([]byte(wantConditions), &want); err != nil {
				return err
			}
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("window %d conditions = %s, want %s", id, mw.Conditions, wantConditions)
		}
		return nil
	}
}

func (a *fakeAPI) checkWindowPatchOmits(keys ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, k := range keys {
			if _, ok := a.windowPatch[k]; ok {
				return fmt.Errorf("last PATCH sent %s = %s", k, a.windowPatch[k])
			}
		}
		return nil
	}
}

func (a *fakeAPI) checkWindowStart(id int, want time.Time) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if got := a.windows[id].StartAt; !got.Equal(want) {
			return fmt.Errorf("window %d startAt = %s, want %s", id, got, want)
		}
		return nil
	}
}

func rfc3339PlusEight(t time.Time) string {
	return t.In(time.FixedZone("UTC+8", 8*60*60)).Format(time.RFC3339)
}

func standaloneWindowConfig(baseURL, label, name, start, end string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_maintenance_window" %q {
  name     = %q
  start_at = %q
  end_at   = %q
}
`, label, name, start, end)
}

func maintenanceWindowConfig(baseURL, team, start, end, extra string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "platform" {
  name       = "Platform"
  depends_on = [vibeoncall_team.payments]
}

resource "vibeoncall_maintenance_window" "deploy" {
  name     = "Deploy"
  team_id  = vibeoncall_team.%s.id
  start_at = %q
  end_at   = %q
  %s
}
`, team, start, end, extra)
}

const datadogAtMostSev3 = `
  condition_groups = [{
    operator = "AND"
    conditions = [
      { provider = { providers = ["datadog", "grafana"] } },
      { severity = { operator = "less_than_or_equal", severity = "SEV3" } },
    ]
  }]
`

const stagingOrNightly = `
  description           = "Friday deploy"
  announcement_channels = ["C1"]
  condition_groups = [
    {
      operator = "AND"
      conditions = [
        { field = { field_path = "details.env", operator = "exact_match", value = "staging", case_sensitive = false } },
      ]
    },
    {
      operator = "OR"
      conditions = [
        { time = { timezone = "Asia/Taipei", daily = { start_time = "22:00", end_time = "06:00" } } },
        { time = { timezone = "Asia/Taipei", weekly = [{ start_day = "saturday", end_day = "sunday", start_time = "00:00", end_time = "23:59" }] } },
      ]
    },
  ]
`

func TestMaintenanceWindowResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.chatChannels = []fakeChatChannel{{ID: "C1", Name: "deploys"}}
	start := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	startAt, endAt := rfc3339PlusEight(start), rfc3339PlusEight(end)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: maintenanceWindowConfig(srv.URL, "payments", startAt, endAt, datadogAtMostSev3),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "team_id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "start_at", startAt),
					resource.TestCheckNoResourceAttr("vibeoncall_maintenance_window.deploy", "description"),
					api.checkWindowStart(1, start),
					api.checkWindowConditions("AND", `[
						{"type":"provider","rule":{"operator":"one_of","providers":["datadog","grafana"]}},
						{"type":"severity","rule":{"operator":"less_than_or_equal","severity":"SEV3"}}
					]`),
				),
			},
			{
				Config: maintenanceWindowConfig(srv.URL, "payments", startAt, endAt, stagingOrNightly),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_maintenance_window.deploy", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "description", "Friday deploy"),
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "announcement_channels.0", "C1"),
					api.checkWindowPatchOmits("startAt", "endAt"),
					api.checkWindowConditions("OR", `[
						{"type":"group","operator":"AND","conditions":[
							{"type":"field","rule":{"fieldPath":"details.env","operator":"exact_match","conditionValue":"staging","isCaseSensitive":false}}
						]},
						{"type":"group","operator":"OR","conditions":[
							{"type":"time","rule":{"timezone":"Asia/Taipei","operator":"day_in_between","timeInterval":{"startHourOffset":"22:00","endHourOffset":"06:00"}}},
							{"type":"time","rule":{"timezone":"Asia/Taipei","operator":"week_in_between","timeInterval":{"weekRanges":[{"startDay":6,"endDay":0,"startHourOffset":"00:00","endHourOffset":"23:59"}]}}}
						]}
					]`),
				),
			},
			{
				ResourceName:            "vibeoncall_maintenance_window.deploy",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"start_at", "end_at"},
			},
			{
				Config: maintenanceWindowConfig(srv.URL, "payments", startAt, rfc3339PlusEight(end.Add(time.Hour)), ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_maintenance_window.deploy", "description"),
					resource.TestCheckNoResourceAttr("vibeoncall_maintenance_window.deploy", "announcement_channels.#"),
					resource.TestCheckNoResourceAttr("vibeoncall_maintenance_window.deploy", "condition_groups.#"),
					api.checkWindowPatchOmits("startAt"),
					api.checkWindowConditions("", ""),
				),
			},
			{
				Config: maintenanceWindowConfig(srv.URL, "platform", startAt, rfc3339PlusEight(end.Add(time.Hour)), ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_maintenance_window.deploy", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "id", "2"),
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "team_id", "2"),
				),
			},
			{
				PreConfig:          func() { api.removeWindow(2) },
				Config:             maintenanceWindowConfig(srv.URL, "platform", startAt, rfc3339PlusEight(end.Add(time.Hour)), ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestMaintenanceWindowResourceRejectsMovingStartedWindow(t *testing.T) {
	api, srv := newFakeAPI(t)
	start := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: maintenanceWindowConfig(srv.URL, "payments", start, end, "")},
			{
				Config:      maintenanceWindowConfig(srv.URL, "platform", start, end, ""),
				ExpectError: regexp.MustCompile(`Cannot move a started maintenance window to another team`),
			},
			{
				Config: maintenanceWindowConfig(srv.URL, "payments", start, end, ""),
				Check:  resource.TestCheckResourceAttr("vibeoncall_maintenance_window.deploy", "id", "1"),
			},
		},
	})
}

func TestMaintenanceWindowResourceGlobalWindow(t *testing.T) {
	api, srv := newFakeAPI(t)
	start := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: standaloneWindowConfig(srv.URL, "global", "Global", start, end),
				Check:  resource.TestCheckNoResourceAttr("vibeoncall_maintenance_window.global", "team_id"),
			},
			{
				ResourceName:      "vibeoncall_maintenance_window.global",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestMaintenanceWindowResourceImportsAppBuiltConditions(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putWindow(&fakeWindow{
		Name:     "From the app",
		StartAt:  time.Now().Add(time.Hour).Truncate(time.Second),
		EndAt:    time.Now().Add(2 * time.Hour).Truncate(time.Second),
		Operator: strPtr("OR"),
		Conditions: json.RawMessage(`[
			{"type":"group","operator":"AND","conditions":[{"type":"severity","rule":{"operator":"equal_to","severity":"SEV1"}}]},
			{"type":"group","operator":"OR","conditions":[{"type":"provider","rule":{"operator":"one_of","providers":["sentry"]}}]}
		]`),
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        standaloneWindowConfig(srv.URL, "x", "x", "2026-01-01T00:00:00Z", "2026-01-01T01:00:00Z"),
			ResourceName:  "vibeoncall_maintenance_window.x",
			ImportState:   true,
			ImportStateId: "1",
			ImportStateCheck: func(states []*terraform.InstanceState) error {
				attrs := states[0].Attributes
				for k, want := range map[string]string{
					"condition_groups.#":                                   "2",
					"condition_groups.0.operator":                          "AND",
					"condition_groups.0.conditions.0.severity.severity":    "SEV1",
					"condition_groups.1.operator":                          "OR",
					"condition_groups.1.conditions.0.provider.providers.0": "sentry",
				} {
					if attrs[k] != want {
						return fmt.Errorf("%s = %q, want %q", k, attrs[k], want)
					}
				}
				return nil
			},
		}},
	})
}

func TestConditionGroupsFromAPIRejectsShapesTheAppCannotBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		operator   string
		conditions string
	}{
		"plain condition next to a group": {"OR", `[
			{"type":"severity","rule":{"operator":"equal_to","severity":"SEV1"}},
			{"type":"group","operator":"OR","conditions":[{"type":"provider","rule":{"operator":"one_of","providers":["sentry"]}}]}
		]`},
		"groups joined by AND": {"AND", `[
			{"type":"group","operator":"OR","conditions":[{"type":"severity","rule":{"operator":"equal_to","severity":"SEV1"}}]},
			{"type":"group","operator":"OR","conditions":[{"type":"provider","rule":{"operator":"one_of","providers":["sentry"]}}]}
		]`},
		"group inside a group": {"OR", nestedGroupConditions},
	} {
		t.Run(name, func(t *testing.T) {
			var conditions []client.MaintenanceWindowCondition
			if err := json.Unmarshal([]byte(tc.conditions), &conditions); err != nil {
				t.Fatal(err)
			}
			if _, err := conditionGroupsFromAPI(conditions, tc.operator); !errors.Is(err, errUnsupportedConditionShape) {
				t.Fatalf("err = %v, want errUnsupportedConditionShape", err)
			}
		})
	}
}

const nestedGroupConditions = `[{"type":"group","operator":"OR","conditions":[
	{"type":"group","operator":"AND","conditions":[{"type":"severity","rule":{"operator":"equal_to","severity":"SEV1"}}]}
]}]`

func TestMaintenanceWindowResourceImportRejectsShapesTheAppCannotBuild(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putWindow(&fakeWindow{
		Name:       "From the API",
		StartAt:    time.Now().Add(time.Hour),
		EndAt:      time.Now().Add(2 * time.Hour),
		Operator:   strPtr("OR"),
		Conditions: json.RawMessage(nestedGroupConditions),
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        standaloneWindowConfig(srv.URL, "x", "x", "2026-01-01T00:00:00Z", "2026-01-01T01:00:00Z"),
			ResourceName:  "vibeoncall_maintenance_window.x",
			ImportState:   true,
			ImportStateId: "1",
			ExpectError:   regexp.MustCompile(`Unsupported maintenance window conditions`),
		}},
	})
}

func TestMaintenanceWindowResourceUpdateOfEndedWindowFails(t *testing.T) {
	api, srv := newFakeAPI(t)
	start := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	config := func(name string) string { return standaloneWindowConfig(srv.URL, "past", name, start, end) }

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config("Past")},
			{Config: config("Past"), PlanOnly: true},
			{Config: config("Renamed"), ExpectError: regexp.MustCompile(`Ended maintenance windows cannot be updated`)},
		},
	})
}

func TestMaintenanceWindowResourceRejectsInvalidConditions(t *testing.T) {
	_, srv := newFakeAPI(t)

	for name, tc := range map[string]struct {
		condition string
		want      string
	}{
		"no condition kind":      {`{}`, `No attribute specified`},
		"two condition kinds":    {`{ severity = { operator = "equal_to", severity = "SEV1" }, provider = { providers = ["sentry"] } }`, `2 attributes specified`},
		"severity alias":         {`{ severity = { operator = "equal_to", severity = "P1" } }`, `must be\s+one of`},
		"unknown provider":       {`{ provider = { providers = ["pagerduty"] } }`, `must be\s+one of`},
		"daily and weekly":       {`{ time = { timezone = "UTC", daily = { start_time = "09:00", end_time = "17:00" }, weekly = [{ start_day = "monday", end_day = "friday", start_time = "09:00", end_time = "17:00" }] } }`, `2 attributes specified`},
		"neither daily nor week": {`{ time = { timezone = "UTC" } }`, `No attribute specified`},
		"malformed clock time":   {`{ time = { timezone = "UTC", daily = { start_time = "9:00", end_time = "17:00" } } }`, `24-hour time in HH:MM`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config: providerConfig(srv.URL) + fmt.Sprintf(`
resource "vibeoncall_maintenance_window" "x" {
  name             = "x"
  start_at         = "2026-01-01T00:00:00Z"
  end_at           = "2026-01-01T01:00:00Z"
  condition_groups = [{ operator = "AND", conditions = [%s] }]
}
`, tc.condition),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}

func TestMaintenanceWindowResourceRejectsMalformedImportID(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        standaloneWindowConfig(srv.URL, "x", "x", "2026-01-01T00:00:00Z", "2026-01-01T01:00:00Z"),
			ResourceName:  "vibeoncall_maintenance_window.x",
			ImportState:   true,
			ImportStateId: "deploy",
			ExpectError:   regexp.MustCompile(`Invalid import ID`),
		}},
	})
}

func TestMaintenanceWindowResourceRewritesUnsupportedConditionsSetThroughTheAPI(t *testing.T) {
	api, srv := newFakeAPI(t)
	start := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	config := maintenanceWindowConfig(srv.URL, "payments", start, end, datadogAtMostSev3)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					api.setWindowConditions(1, "OR", nestedGroupConditions)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("vibeoncall_maintenance_window.deploy", plancheck.ResourceActionUpdate),
					},
				},
				Check: api.checkWindowConditions("AND", `[
					{"type":"provider","rule":{"operator":"one_of","providers":["datadog","grafana"]}},
					{"type":"severity","rule":{"operator":"less_than_or_equal","severity":"SEV3"}}
				]`),
			},
		},
	})
}

func TestMaintenanceWindowResourceImportedEndedWindowConvergesWithOffsetTimes(t *testing.T) {
	api, srv := newFakeAPI(t)
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	end := start.Add(time.Hour)
	api.putWindow(&fakeWindow{Name: "Past", StartAt: start, EndAt: end})
	config := standaloneWindowConfig(srv.URL, "past", "Past", rfc3339PlusEight(start), rfc3339PlusEight(end))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "vibeoncall_maintenance_window.past",
				ImportState:        true,
				ImportStateId:      "1",
				ImportStatePersist: true,
			},
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_maintenance_window.past", "start_at", rfc3339PlusEight(start)),
					api.checkWindowPatchOmits("name", "startAt", "endAt"),
				),
			},
		},
	})
}

func TestMaintenanceWindowResourceTracksFractionalSeconds(t *testing.T) {
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("UTC+8", 8*60*60)} {
		t.Run(zone.String(), func(t *testing.T) {
			api, srv := newFakeAPI(t)
			start := time.Now().Add(24 * time.Hour).Truncate(time.Second).Add(500 * time.Millisecond).In(zone)
			startAt := start.Format(time.RFC3339Nano)
			config := standaloneWindowConfig(srv.URL, "precise", "Precise", startAt, start.Add(time.Hour).Format(time.RFC3339Nano))

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				CheckDestroy:             api.checkNothingLeft,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr("vibeoncall_maintenance_window.precise", "start_at", startAt),
							api.checkWindowStart(1, start),
						),
					},
					{
						PreConfig: func() {
							api.mu.Lock()
							defer api.mu.Unlock()
							api.windows[1].StartAt = start.Add(250 * time.Millisecond)
						},
						Config:             config,
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
					},
				},
			})
		})
	}
}

func TestMaintenanceWindowResourceRejectsNonRFC3339Time(t *testing.T) {
	_, srv := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      standaloneWindowConfig(srv.URL, "x", "X", "2026-11-20 09:00", "2026-11-20T10:00:00Z"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`Invalid RFC 3339 timestamp`),
		}},
	})
}
