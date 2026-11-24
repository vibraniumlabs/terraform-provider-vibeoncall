package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

var scheduleUsers = []fakeUser{
	{ID: 7, Email: "alice@example.com", Status: "active"},
	{ID: 8, Email: "bob@example.com", Status: "active"},
	{ID: 9, Email: "carol@example.com", Status: "active"},
}

type wantRotation struct {
	ID           int
	Name         string
	Participants []int
}

func (a *fakeAPI) checkRotations(want []wantRotation) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		s := a.schedules[1]
		if s == nil {
			return errors.New("schedule 1 not found")
		}
		got := make([]wantRotation, len(s.Rotations))
		for i, rot := range s.Rotations {
			got[i] = wantRotation{ID: rot.ID, Name: rot.Name, Participants: rot.Participants}
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("schedule rotations = %+v, want %+v", got, want)
		}
		return nil
	}
}

func (a *fakeAPI) checkRotationConfig(index int, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		var got, expected any
		if err := json.Unmarshal(a.schedules[1].Rotations[index].RotationConfig, &got); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(want), &expected); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, expected) {
			return fmt.Errorf("rotation %d config = %v, want %v", index, got, expected)
		}
		return nil
	}
}

func (a *fakeAPI) checkScheduleTeams(want []int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if got := a.schedules[1].TeamIDs; !reflect.DeepEqual(got, want) {
			return fmt.Errorf("schedule teams = %v, want %v", got, want)
		}
		return nil
	}
}

func (a *fakeAPI) checkNoRotationPuts(*terraform.State) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rotationPuts > 0 {
		return fmt.Errorf("%d rotation PUTs, want none", a.rotationPuts)
	}
	return nil
}

func scheduleConfig(baseURL, attrs string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
resource "vibeoncall_team" "payments" {
  name = "Payments"
}

resource "vibeoncall_team" "platform" {
  name       = "Platform"
  depends_on = [vibeoncall_team.payments]
}

resource "vibeoncall_schedule" "test" {
%s
}
`, attrs)
}

func weeklyRotation(name string, participants ...string) string {
	return fmt.Sprintf(`{
      name            = %q
      start_date      = "2026-09-01"
      participant_ids = [%s]
      weekly = {
        start_day  = "monday"
        start_time = "09:00"
        end_day    = "monday"
        end_time   = "09:00"
      }
    }`, name, `"`+strings.Join(participants, `", "`)+`"`)
}

func rotationsAttr(rotations ...string) string {
	return "  rotations = [\n    " + strings.Join(rotations, ",\n    ") + ",\n  ]"
}

func expectUpdate() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("vibeoncall_schedule.test", plancheck.ResourceActionUpdate),
	}}
}

func TestScheduleResourceLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := scheduleConfig(srv.URL, `
  name      = "Payments primary"
  time_zone = "Asia/Taipei"
  team_ids  = [vibeoncall_team.payments.id]
`+rotationsAttr(weeklyRotation("Primary", "7", "8", "9")))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "time_zone", "Asia/Taipei"),
					resource.TestCheckTypeSetElemAttr("vibeoncall_schedule.test", "team_ids.*", "1"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "rotations.0.id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "rotations.0.weekly.start_day", "monday"),
					api.checkScheduleTeams([]int{1}),
					api.checkRotations([]wantRotation{{ID: 1, Name: "Primary", Participants: []int{7, 8, 9}}}),
					api.checkRotationConfig(0, `{"type":"weekly","payload":{"startDay":1,"startTime":"09:00","endDay":1,"endTime":"09:00"}}`),
				),
			},
			{
				ResourceName:      "vibeoncall_schedule.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				PreConfig:          func() { api.removeSchedule(1) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestScheduleResourceStoresEachRotationShape(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: scheduleConfig(srv.URL, `
  name      = "Shapes"
  time_zone = "America/New_York"
  rotations = [
    {
      name            = "Daily"
      start_date      = "2026-09-01"
      participant_ids = ["7"]
      daily = { start_time = "09:30", end_time = "17:30" }
    },
    {
      name            = "Weekdays"
      start_date      = "2026-09-01"
      participant_ids = ["8"]
      weekly = {
        start_day        = "monday"
        start_time       = "09:00"
        end_day          = "friday"
        end_time         = "17:00"
        daily_start_time = "09:00"
        daily_end_time   = "17:00"
        hand_off_day     = "wednesday"
      }
    },
    {
      name            = "Weekends"
      start_date      = "2026-09-05"
      participant_ids = ["9"]
      weekly_windows = {
        windows = [
          { start_day = "saturday", start_time = "00:00", end_day = "saturday", end_time = "23:00" },
          { start_day = "sunday", start_time = "00:00", end_day = "sunday", end_time = "23:00" },
        ]
        hand_off_day = "saturday"
      }
    },
  ]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_schedule.test", "team_ids"),
					api.checkScheduleTeams([]int{}),
					api.checkRotationConfig(0, `{"type":"daily","payload":{"startTime":"09:30","endTime":"17:30"}}`),
					api.checkRotationConfig(1, `{"type":"weekly","payload":{"startDay":1,"startTime":"09:00","endDay":5,"endTime":"17:00","dailyStartTime":"09:00","dailyEndTime":"17:00","handOffDay":3}}`),
					api.checkRotationConfig(2, `{"type":"weekly","payload":{"windows":[{"startDay":6,"startTime":"00:00","endDay":6,"endTime":"23:00"},{"startDay":0,"startTime":"00:00","endDay":0,"endTime":"23:00"}],"handOffDay":6}}`),
				),
			},
			{
				ResourceName:      "vibeoncall_schedule.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func handOffRotations(rangeTime, windowsTime string) string {
	attr := func(t string) string {
		if t == "" {
			return ""
		}
		return fmt.Sprintf("\n        hand_off_time = %q", t)
	}
	return fmt.Sprintf(`
  name      = "Hand off"
  time_zone = "Asia/Taipei"
  rotations = [
    {
      name            = "Weekdays"
      start_date      = "2026-09-01"
      participant_ids = ["7", "8"]
      weekly = {
        start_day     = "monday"
        start_time    = "09:00"
        end_day       = "friday"
        end_time      = "17:00"
        hand_off_day  = "wednesday"%s
      }
    },
    {
      name            = "Weekends"
      start_date      = "2026-09-05"
      participant_ids = ["9", "7"]
      weekly_windows = {
        windows = [
          { start_day = "saturday", start_time = "00:00", end_day = "sunday", end_time = "23:00" },
        ]
        hand_off_day = "saturday"%s
      }
    },
  ]`, attr(rangeTime), attr(windowsTime))
}

func TestScheduleResourceHandOffTime(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := func(rangeTime, windowsTime string) string {
		return scheduleConfig(srv.URL, handOffRotations(rangeTime, windowsTime))
	}
	// "" means the request carried no handOffTime.
	withTime := func(handOffTime string) string {
		if handOffTime == "" {
			return ""
		}
		return fmt.Sprintf(`,"handOffTime":%q`, handOffTime)
	}
	rangeConfig := func(handOffTime string) string {
		return `{"type":"weekly","payload":{"startDay":1,"startTime":"09:00","endDay":5,"endTime":"17:00","handOffDay":3` + withTime(handOffTime) + `}}`
	}
	windowsConfig := func(handOffTime string) string {
		return `{"type":"weekly","payload":{"windows":[{"startDay":6,"startTime":"00:00","endDay":0,"endTime":"23:00"}],"handOffDay":6` + withTime(handOffTime) + `}}`
	}
	const (
		rangeTimeAttr   = "rotations.0.weekly.hand_off_time"
		windowsTimeAttr = "rotations.1.weekly_windows.hand_off_time"
	)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: config("09:00", "18:30"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", rangeTimeAttr, "09:00"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", windowsTimeAttr, "18:30"),
					api.checkRotationConfig(0, rangeConfig("09:00")),
					api.checkRotationConfig(1, windowsConfig("18:30")),
				),
			},
			{
				ResourceName:      "vibeoncall_schedule.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           config("17:30", "18:30"),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", rangeTimeAttr, "17:30"),
					api.checkRotationConfig(0, rangeConfig("17:30")),
					api.checkRotationConfig(1, windowsConfig("18:30")),
				),
			},
			{
				// A PUT replaces the whole rotation, so leaving handOffTime out hands off at 00:00 again.
				Config:           config("", ""),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_schedule.test", rangeTimeAttr),
					resource.TestCheckNoResourceAttr("vibeoncall_schedule.test", windowsTimeAttr),
					api.checkRotationConfig(0, rangeConfig("")),
					api.checkRotationConfig(1, windowsConfig("")),
				),
			},
			{
				PreConfig:          func() { api.setRotationHandOffTime(0, "10:00") },
				Config:             config("", ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("vibeoncall_schedule.test", rangeTimeAttr),
					api.checkRotationConfig(0, rangeConfig("")),
				),
			},
			{
				Config: config("00:00", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", rangeTimeAttr, "00:00"),
					api.checkRotationConfig(0, rangeConfig("00:00")),
				),
			},
			{
				// Saving the rotation in the web app drops a stored 00:00, which still matches an explicit "00:00".
				PreConfig: func() { api.setRotationHandOffTime(0, "") },
				Config:    config("00:00", ""),
				PlanOnly:  true,
			},
		},
	})
}

func TestScheduleResourceReadsMidnightHandOffAsUnset(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := providerConfig(srv.URL) + "\nresource \"vibeoncall_schedule\" \"test\" {" + handOffRotations("", "") + "\n}\n"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				// A rotation saved before handOffTime existed has no time stored.
				PreConfig: func() {
					api.schedules[1] = &fakeSchedule{ID: 1, Name: "Hand off", Timezone: "Asia/Taipei", TeamIDs: []int{}, Rotations: []*fakeRotation{
						{ID: 1, Name: "Weekdays", StartAt: "2026-09-01", Participants: []int{7, 8}, RotationConfig: json.RawMessage(`{"type":"weekly","payload":{"startDay":1,"startTime":"09:00","endDay":5,"endTime":"17:00","handOffDay":3}}`)},
						{ID: 2, Name: "Weekends", StartAt: "2026-09-05", Participants: []int{9, 7}, RotationConfig: json.RawMessage(`{"type":"weekly","payload":{"windows":[{"startDay":6,"startTime":"00:00","endDay":0,"endTime":"23:00"}],"handOffDay":6}}`)},
					}}
					api.nextSchedID, api.nextRotID = 2, 3
				},
				Config:             config,
				ResourceName:       "vibeoncall_schedule.test",
				ImportState:        true,
				ImportStateId:      "1",
				ImportStatePersist: true,
			},
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}

func TestScheduleResourceUpdatesDetailsAndTeamsInPlace(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	rotations := rotationsAttr(weeklyRotation("Primary", "7", "8"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: scheduleConfig(srv.URL, `
  name      = "Primary"
  time_zone = "Asia/Taipei"
  team_ids  = [vibeoncall_team.payments.id]
`+rotations),
			},
			{
				Config: scheduleConfig(srv.URL, `
  name      = "Shared primary"
  time_zone = "Europe/London"
  team_ids  = [vibeoncall_team.payments.id, vibeoncall_team.platform.id]
`+rotations),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "id", "1"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "name", "Shared primary"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "time_zone", "Europe/London"),
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "rotations.0.id", "1"),
					api.checkScheduleTeams([]int{1, 2}),
					api.checkNoRotationPuts,
				),
			},
			{
				Config: scheduleConfig(srv.URL, `
  name      = "Shared primary"
  time_zone = "Europe/London"
`+rotations),
				ConfigPlanChecks: expectUpdate(),
				Check:            api.checkScheduleTeams([]int{}),
			},
		},
	})
}

func TestScheduleResourceKeepsRotationIDsAcrossChanges(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := func(rotations ...string) string {
		return scheduleConfig(srv.URL, `
  name      = "Primary"
  time_zone = "Asia/Taipei"
`+rotationsAttr(rotations...))
	}
	ids := func(ids ...string) resource.TestCheckFunc {
		checks := make([]resource.TestCheckFunc, 0, len(ids)+1)
		checks = append(checks, resource.TestCheckResourceAttr("vibeoncall_schedule.test", "rotations.#", fmt.Sprint(len(ids))))
		for i, id := range ids {
			checks = append(checks, resource.TestCheckResourceAttr("vibeoncall_schedule.test", fmt.Sprintf("rotations.%d.id", i), id))
		}
		return resource.ComposeAggregateTestCheckFunc(checks...)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: config(weeklyRotation("Primary", "7", "8"), weeklyRotation("Secondary", "8", "9"), weeklyRotation("Backup", "9", "7")),
				Check:  ids("1", "2", "3"),
			},
			{
				Config: config(weeklyRotation("Primary", "7", "8"), weeklyRotation("Backup", "9", "7")),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("vibeoncall_schedule.test", plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue("vibeoncall_schedule.test", tfjsonpath.New("rotations").AtSliceIndex(1).AtMapKey("id"), knownvalue.StringExact("3")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					ids("1", "3"),
					api.checkRotations([]wantRotation{{1, "Primary", []int{7, 8}}, {3, "Backup", []int{9, 7}}}),
				),
			},
			{
				Config:           config(weeklyRotation("Primary", "7", "8"), weeklyRotation("Backup night", "9", "7")),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					ids("1", "3"),
					api.checkRotations([]wantRotation{{1, "Primary", []int{7, 8}}, {3, "Backup night", []int{9, 7}}}),
				),
			},
			{
				Config:           config(weeklyRotation("Primary", "7", "8"), weeklyRotation("Backup night", "9", "7"), weeklyRotation("Weekend", "8")),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					ids("1", "3", "4"),
					api.checkRotations([]wantRotation{{1, "Primary", []int{7, 8}}, {3, "Backup night", []int{9, 7}}, {4, "Weekend", []int{8}}}),
				),
			},
			{
				Config:           config(weeklyRotation("Weekend", "8"), weeklyRotation("Primary", "7", "8"), weeklyRotation("Backup night", "9", "7")),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					ids("1", "3", "4"),
					api.checkRotations([]wantRotation{{1, "Weekend", []int{8}}, {3, "Primary", []int{7, 8}}, {4, "Backup night", []int{9, 7}}}),
				),
			},
		},
	})
}

func TestScheduleResourceKeepsParticipantOrderAsWritten(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := func(participants ...string) string {
		return scheduleConfig(srv.URL, `
  name      = "Primary"
  time_zone = "Asia/Taipei"
`+rotationsAttr(weeklyRotation("Primary", participants...)))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: config("7", "8", "9"),
				Check:  api.checkRotations([]wantRotation{{1, "Primary", []int{7, 8, 9}}}),
			},
			{
				Config:           config("9", "7"),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "rotations.0.participant_ids.0", "9"),
					api.checkRotations([]wantRotation{{1, "Primary", []int{9, 7}}}),
				),
			},
			{
				Config:           config("9", "7", "8"),
				ConfigPlanChecks: expectUpdate(),
				Check:            api.checkRotations([]wantRotation{{1, "Primary", []int{9, 7, 8}}}),
			},
		},
	})
}

func TestScheduleResourceRestoresRotationsBeforeRenamingImportedSchedule(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	api.schedules[1] = &fakeSchedule{ID: 1, Name: "Legacy", Timezone: "UTC", TeamIDs: []int{}, Rotations: []*fakeRotation{}}
	api.nextSchedID = 2
	config := func(name string) string {
		return scheduleConfig(srv.URL, fmt.Sprintf(`
  name      = %q
  time_zone = "Asia/Taipei"
`, name)+rotationsAttr(weeklyRotation("Primary", "7", "8")))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config:             config("Legacy"),
				ResourceName:       "vibeoncall_schedule.test",
				ImportState:        true,
				ImportStateId:      "1",
				ImportStatePersist: true,
			},
			{
				Config:           config("Payments primary"),
				ConfigPlanChecks: expectUpdate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vibeoncall_schedule.test", "name", "Payments primary"),
					api.checkRotations([]wantRotation{{1, "Primary", []int{7, 8}}}),
				),
			},
		},
	})
}

type expectNoDrift struct{}

func (expectNoDrift) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	if len(req.Plan.ResourceDrift) > 0 {
		resp.Error = fmt.Errorf("%s changed outside Terraform", req.Plan.ResourceDrift[0].Address)
	}
}

func TestScheduleResourceSavesWhatAPartialUpdateApplied(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := func(rotations ...string) string {
		return scheduleConfig(srv.URL, `
  name      = "Primary"
  time_zone = "Asia/Taipei"
`+rotationsAttr(rotations...))
	}
	updated := config(weeklyRotation("Primary", "9", "7"), weeklyRotation("Backup", "9"), weeklyRotation("Weekend", "7"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{
				Config: config(weeklyRotation("Primary", "7", "8"), weeklyRotation("Secondary", "8", "9"), weeklyRotation("Backup", "9")),
			},
			{
				PreConfig:   func() { api.setRotationInUse(2, true) },
				Config:      updated,
				ExpectError: regexp.MustCompile(`Delete rotation failed`),
			},
			{
				PreConfig: func() { api.setRotationInUse(2, false) },
				Config:    updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					expectNoDrift{},
					plancheck.ExpectKnownValue("vibeoncall_schedule.test", tfjsonpath.New("rotations").AtSliceIndex(2).AtMapKey("id"), knownvalue.StringExact("4")),
				}},
				Check: api.checkRotations([]wantRotation{{1, "Primary", []int{9, 7}}, {3, "Backup", []int{9}}, {4, "Weekend", []int{7}}}),
			},
		},
	})
}

func TestScheduleResourceRejectsInvalidConfigAtPlanTime(t *testing.T) {
	_, srv := newFakeAPI(t, scheduleUsers...)
	valid := map[string]string{
		"team_ids":        `vibeoncall_team.payments.id`,
		"start_date":      `"2026-09-01"`,
		"participant_ids": `"7", "8"`,
		"shape":           `daily = { start_time = "09:00", end_time = "17:00" }`,
	}

	for name, tc := range map[string]struct {
		field, value, want string
	}{
		"no rotation shape":                 {"shape", ``, `No attribute specified when one \(and only one\)`},
		"two rotation shapes":               {"shape", `daily = { start_time = "09:00", end_time = "17:00" }` + "\n" + `weekly = { start_day = "monday", start_time = "09:00", end_day = "monday", end_time = "09:00" }`, `2 attributes specified when one \(and only one\)`},
		"minute out of range":               {"shape", `daily = { start_time = "09:60", end_time = "17:00" }`, `must be a 24-hour time`},
		"hour out of range":                 {"shape", `daily = { start_time = "24:00", end_time = "17:00" }`, `must be a 24-hour time`},
		"unknown weekday":                   {"shape", `weekly = { start_day = "mon", start_time = "09:00", end_day = "monday", end_time = "09:00" }`, `value must be one of`},
		"duplicate participant":             {"participant_ids", `"7", "7"`, `duplicate`},
		"malformed start date":              {"start_date", `"2026/09/01"`, `must be a date in YYYY-MM-DD format`},
		"impossible start date":             {"start_date", `"2026-02-30"`, `must be a date in YYYY-MM-DD format`},
		"empty team list":                   {"team_ids", ``, `at least 1`},
		"hand off time without day":         {"shape", `weekly = { start_day = "monday", start_time = "09:00", end_day = "monday", end_time = "09:00", hand_off_time = "09:00" }`, `hand_off_day" must be specified when`},
		"windows hand off time without day": {"shape", `weekly_windows = { windows = [{ start_day = "monday", start_time = "09:00", end_day = "monday", end_time = "17:00" }], hand_off_time = "09:00" }`, `hand_off_day" must be specified when`},
		"malformed hand off time":           {"shape", `weekly = { start_day = "monday", start_time = "09:00", end_day = "monday", end_time = "09:00", hand_off_day = "monday", hand_off_time = "9:00" }`, `must be a 24-hour time`},
	} {
		t.Run(name, func(t *testing.T) {
			attrs := map[string]string{}
			for k, v := range valid {
				attrs[k] = v
			}
			attrs[tc.field] = tc.value
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config: scheduleConfig(srv.URL, fmt.Sprintf(`
  name      = "Primary"
  time_zone = "Asia/Taipei"
  team_ids  = [%s]
  rotations = [{
    name            = "Primary"
    start_date      = %s
    participant_ids = [%s]
    %s
  }]`, attrs["team_ids"], attrs["start_date"], attrs["participant_ids"], attrs["shape"])),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}

func TestScheduleResourceDeleteInUseShowsReadableError(t *testing.T) {
	api, srv := newFakeAPI(t, scheduleUsers...)
	config := scheduleConfig(srv.URL, `
  name      = "Primary"
  time_zone = "Asia/Taipei"
`+rotationsAttr(weeklyRotation("Primary", "7")))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNothingLeft,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:   func() { api.setScheduleInUse(1, true) },
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)Schedule is still in use.*escalation policy or routing rule`),
			},
			{
				PreConfig: func() { api.setScheduleInUse(1, false) },
				Config:    config,
			},
		},
	})
}

func TestScheduleResourceRejectsMalformedImportID(t *testing.T) {
	_, srv := newFakeAPI(t, scheduleUsers...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: scheduleConfig(srv.URL, `
  name      = "Primary"
  time_zone = "Asia/Taipei"
`+rotationsAttr(weeklyRotation("Primary", "7"))),
			ResourceName:  "vibeoncall_schedule.test",
			ImportState:   true,
			ImportStateId: "Primary",
			ExpectError:   regexp.MustCompile(`Invalid import ID`),
		}},
	})
}

func TestRewrittenRotations(t *testing.T) {
	ab := []priorRotation{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}}
	ll := []priorRotation{{ID: "1", Name: "L"}, {ID: "2", Name: "L"}}
	for name, tc := range map[string]struct {
		prior   []priorRotation
		desired []string
		want    []string
	}{
		"unchanged":                      {ab, []string{"A", "B"}, nil},
		"renamed":                        {ab, []string{"A", "X"}, nil},
		"last removed":                   {ab, []string{"A"}, nil},
		"appended":                       {ab, []string{"A", "B", "C"}, nil},
		"one of duplicate names renamed": {ll, []string{"L", "X"}, nil},
		"reordered":                      {ab, []string{"B", "A"}, []string{`- rotation 1 changes from "A" to "B"`, `- rotation 2 changes from "B" to "A"`}},
		"inserted in the middle":         {ab, []string{"A", "X", "B"}, []string{`- rotation 2 changes from "B" to "X"`}},
		"inserted between duplicates":    {ll, []string{"L", "X", "L"}, []string{`- rotation 2 changes from "L" to "X"`}},
	} {
		t.Run(name, func(t *testing.T) {
			got := rewrittenRotations(tc.prior, tc.desired, matchRotations(tc.prior, tc.desired))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("rewrittenRotations(%v) = %q, want %q", tc.desired, got, tc.want)
			}
		})
	}
}

func TestMatchRotations(t *testing.T) {
	prior := func(pairs ...string) []priorRotation {
		out := make([]priorRotation, 0, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, priorRotation{Name: pairs[i], ID: pairs[i+1]})
		}
		return out
	}

	for name, tc := range map[string]struct {
		prior   []priorRotation
		desired []string
		want    []string
	}{
		"unchanged":                    {prior("A", "1", "B", "2"), []string{"A", "B"}, []string{"1", "2"}},
		"middle removed":               {prior("A", "1", "B", "2", "C", "3"), []string{"A", "C"}, []string{"1", "3"}},
		"appended":                     {prior("A", "1"), []string{"A", "B"}, []string{"1", ""}},
		"last renamed":                 {prior("A", "1", "B", "2"), []string{"A", "X"}, []string{"1", "2"}},
		"middle renamed":               {prior("A", "1", "B", "2", "C", "3"), []string{"A", "X", "C"}, []string{"1", "2", "3"}},
		"middle removed and appended":  {prior("A", "1", "B", "2", "C", "3"), []string{"A", "C", "D"}, []string{"1", "3", ""}},
		"reordered":                    {prior("A", "1", "B", "2"), []string{"B", "A"}, []string{"1", "2"}},
		"inserted in the middle":       {prior("A", "1", "B", "2"), []string{"A", "X", "B"}, []string{"1", "2", ""}},
		"duplicate names unchanged":    {prior("L", "7", "L", "8", "B", "9"), []string{"L", "L", "B"}, []string{"7", "8", "9"}},
		"duplicate names renamed":      {prior("L", "7", "L", "8", "B", "9"), []string{"L day", "L night", "B"}, []string{"7", "8", "9"}},
		"one of duplicate names gone":  {prior("L", "7", "L", "8", "B", "9"), []string{"L", "B"}, []string{"7", "9"}},
		"every rotation replaced":      {prior("A", "1"), []string{"X", "Y"}, []string{"1", ""}},
		"rotations removed from tail":  {prior("A", "1", "B", "2", "C", "3"), []string{"A"}, []string{"1"}},
		"same names with new appended": {prior("A", "1", "B", "2"), []string{"A", "B", "B"}, []string{"1", "2", ""}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := matchRotations(tc.prior, tc.desired); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("matchRotations(%v, %v) = %q, want %q", tc.prior, tc.desired, got, tc.want)
			}
		})
	}
}
