package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type fakeRouting struct {
	ID          int
	Name        string
	Description *string
	TeamID      *int
	IsEnabled   bool
	Rules       []map[string]any
	seeded      bool
}

func (rt *fakeRouting) json() map[string]any {
	return map[string]any{
		"id": rt.ID, "name": rt.Name, "description": rt.Description, "teamId": rt.TeamID,
		"isEnabled": rt.IsEnabled, "rules": rt.Rules,
		"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
	}
}

func (a *fakeAPI) serveAlertRoutings(w http.ResponseWriter, r *http.Request, rest []string, body map[string]json.RawMessage) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		rt := &fakeRouting{ID: a.nextRoutingID, IsEnabled: true, Rules: []map[string]any{}}
		_ = json.Unmarshal(body["teamId"], &rt.TeamID)
		if rt.TeamID != nil && a.teams[*rt.TeamID] == nil {
			notFound(w)
			return
		}
		applyRoutingBody(rt, body)
		a.nextRoutingID++
		a.routings[rt.ID] = rt
		writeJSON(w, http.StatusCreated, rt.json())
		return
	}
	if len(rest) != 1 {
		notFound(w)
		return
	}

	id, _ := strconv.Atoi(rest[0])
	rt := a.routings[id]
	if rt == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, rt.json())
	case http.MethodPatch:
		for key := range body {
			if key != "name" && key != "description" && key != "isEnabled" && key != "rules" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unrecognized key: " + key})
				return
			}
		}
		applyRoutingBody(rt, body)
		writeJSON(w, http.StatusOK, rt.json())
	case http.MethodDelete:
		for _, s := range a.sources {
			if s.AlertRoutingID != nil && *s.AlertRoutingID == rt.ID {
				writeJSON(w, http.StatusConflict, map[string]string{"message": "An alert source or heartbeat still uses this routing"})
				return
			}
		}
		delete(a.routings, rt.ID)
		w.WriteHeader(http.StatusNoContent)
	default:
		notFound(w)
	}
}

func applyRoutingBody(rt *fakeRouting, body map[string]json.RawMessage) {
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &rt.Name)
	}
	if raw, ok := body["description"]; ok {
		rt.Description = nil
		_ = json.Unmarshal(raw, &rt.Description)
	}
	if raw, ok := body["isEnabled"]; ok {
		_ = json.Unmarshal(raw, &rt.IsEnabled)
	}
	if raw, ok := body["rules"]; ok {
		rt.Rules = []map[string]any{}
		_ = json.Unmarshal(raw, &rt.Rules)
		for _, rule := range rt.Rules {
			if _, ok := rule["name"]; !ok {
				rule["name"] = nil
			}
		}
	}
}

func (a *fakeAPI) teamOwnsRoutings(teamID int) bool {
	for _, rt := range a.routings {
		if !rt.seeded && rt.TeamID != nil && *rt.TeamID == teamID {
			return true
		}
	}
	return false
}

func (a *fakeAPI) seedRouting(id int, teamID *int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routings[id] = &fakeRouting{ID: id, Name: "Seeded " + strconv.Itoa(id), TeamID: teamID, IsEnabled: true, Rules: []map[string]any{}, seeded: true}
}

func (a *fakeAPI) setRoutingRules(id int, rules string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routings[id].Rules = nil
	if err := json.Unmarshal([]byte(rules), &a.routings[id].Rules); err != nil {
		panic(err)
	}
}

func (a *fakeAPI) removeRouting(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.routings, id)
}

func (a *fakeAPI) checkRoutingRules(id int, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		got, _ := json.Marshal(a.routings[id].Rules)
		var g, w any
		_ = json.Unmarshal(got, &g)
		if err := json.Unmarshal([]byte(want), &w); err != nil {
			return err
		}
		if !reflect.DeepEqual(g, w) {
			return fmt.Errorf("routing %d rules =\n%s\nwant\n%s", id, got, want)
		}
		return nil
	}
}
