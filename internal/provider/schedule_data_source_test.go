package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func scheduleDataSourceConfig(baseURL, attrs string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
data "vibeoncall_schedule" "test" {
%s
}
`, attrs)
}

func seededRotation(id int, name string) *fakeRotation {
	return &fakeRotation{
		ID: id, Name: name, StartAt: "2026-09-01", Participants: []int{7},
		RotationConfig: json.RawMessage(`{"type":"daily","payload":{"startTime":"09:00","endTime":"09:00"}}`),
	}
}

func seedSchedules(api *fakeAPI) {
	api.teams[1] = &fakeTeam{ID: 1, ShortID: "VP-TEAM-1", Name: "Payments"}
	api.teams[2] = &fakeTeam{ID: 2, ShortID: "VP-TEAM-2", Name: "Platform"}
	for i := 1; i <= 5; i++ {
		api.schedules[i] = &fakeSchedule{ID: i, Name: fmt.Sprintf("Schedule %d", i), Timezone: "UTC", TeamIDs: []int{2}, Rotations: []*fakeRotation{seededRotation(100+i, "Primary")}}
	}
	api.schedules[1] = &fakeSchedule{
		ID: 1, Name: "Payments primary", Timezone: "Asia/Taipei", TeamIDs: []int{1, 2},
		Rotations: []*fakeRotation{seededRotation(11, "Day"), seededRotation(12, "Night")},
	}
}

func TestScheduleDataSourceFindsNameAcrossPages(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedSchedules(api)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: scheduleDataSourceConfig(srv.URL, `  name = "Payments primary"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "id", "1"),
				resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "time_zone", "Asia/Taipei"),
				resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "team_ids.#", "2"),
				resource.TestCheckTypeSetElemAttr("data.vibeoncall_schedule.test", "team_ids.*", "1"),
				resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "rotations.#", "2"),
				resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "rotations.1.id", "12"),
				resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "rotations.1.name", "Night"),
			),
		}},
	})
}

func TestScheduleDataSourceNarrowsByTeam(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedSchedules(api)
	api.schedules[2].TeamIDs = []int{1}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: scheduleDataSourceConfig(srv.URL, `
  name    = "Schedule 2"
  team_id = "1"`),
				Check: resource.TestCheckResourceAttr("data.vibeoncall_schedule.test", "id", "2"),
			},
			{
				Config: scheduleDataSourceConfig(srv.URL, `
  name    = "Schedule 2"
  team_id = "2"`),
				ExpectError: regexp.MustCompile(`Team 2 owns no schedule named "Schedule 2"`),
			},
		},
	})
}

func TestScheduleDataSourceErrorsWhenNameNotFound(t *testing.T) {
	api, srv := newFakeAPI(t)
	seedSchedules(api)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      scheduleDataSourceConfig(srv.URL, `  name = "payments primary"`),
			ExpectError: regexp.MustCompile(`Schedule not found`),
		}},
	})
}
