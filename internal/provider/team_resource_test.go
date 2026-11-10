package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type fakeTeam struct {
	ID          int     `json:"id"`
	ShortID     string  `json:"shortId"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

type fakeTeamAPI struct {
	mu     sync.Mutex
	nextID int
	teams  map[int]*fakeTeam
}

func newFakeTeamAPI(t *testing.T) (*fakeTeamAPI, *httptest.Server) {
	api := &fakeTeamAPI{nextID: 1, teams: map[int]*fakeTeam{}}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return api, srv
}

func (a *fakeTeamAPI) find(ref string) *fakeTeam {
	for _, team := range a.teams {
		if strconv.Itoa(team.ID) == ref || team.ShortID == ref {
			return team
		}
	}
	return nil
}

func (a *fakeTeamAPI) remove(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if team := a.find(ref); team != nil {
		delete(a.teams, team.ID)
	}
}

func (a *fakeTeamAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	var body map[string]json.RawMessage
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	if r.URL.Path == "/teams" && r.Method == http.MethodPost {
		team := &fakeTeam{ID: a.nextID, ShortID: fmt.Sprintf("VP-TEAM-%d", a.nextID)}
		a.nextID++
		applyTeamBody(team, body)
		a.teams[team.ID] = team
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(team)
		return
	}

	team := a.find(strings.TrimPrefix(r.URL.Path, "/teams/"))
	if team == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Team not found"}`))
		return
	}
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(team)
	case http.MethodPatch:
		applyTeamBody(team, body)
		_ = json.NewEncoder(w).Encode(team)
	case http.MethodDelete:
		delete(a.teams, team.ID)
		_, _ = w.Write([]byte(`{"success":true}`))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func applyTeamBody(team *fakeTeam, body map[string]json.RawMessage) {
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &team.Name)
	}
	if raw, ok := body["description"]; ok {
		team.Description = nil
		_ = json.Unmarshal(raw, &team.Description)
	}
}

func (a *fakeTeamAPI) checkDescription(id string, want *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		team := a.find(id)
		if team == nil {
			return fmt.Errorf("team %s not found", id)
		}
		if (team.Description == nil) != (want == nil) || (want != nil && *team.Description != *want) {
			return fmt.Errorf("description = %v, want %v", team.Description, want)
		}
		return nil
	}
}

func (a *fakeTeamAPI) checkNoTeams(*terraform.State) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.teams) > 0 {
		return fmt.Errorf("%d teams left after destroy", len(a.teams))
	}
	return nil
}

func protoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"vibeoncall": providerserver.NewProtocol6WithError(New("test")()),
	}
}

func teamConfig(baseURL, attrs string) string {
	return fmt.Sprintf(`
provider "vibeoncall" {
  api_key  = "test"
  base_url = %q
}

resource "vibeoncall_team" "test" {
%s
}
`, baseURL, attrs)
}

func TestTeamResourceLifecycle(t *testing.T) {
	api, srv := newFakeTeamAPI(t)
	description := "Owns the payment stack"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             api.checkNoTeams,
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
				PreConfig:          func() { api.remove("1") },
				Config:             teamConfig(srv.URL, `  name = "Payments Platform"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestTeamResourceRejectsValuesTheAPIWouldTrim(t *testing.T) {
	_, srv := newFakeTeamAPI(t)

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
