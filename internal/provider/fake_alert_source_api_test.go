package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

type fakeSource struct {
	ID             int
	Name           string
	Provider       string
	TeamID         *int
	AlertRoutingID *int
	IsEnabled      bool
}

func (s *fakeSource) json() map[string]any {
	var webhookURL, webhookToken, emailAddress *string
	if s.Provider == "email" {
		emailAddress = strPtr(fmt.Sprintf("source-%d@alerts.example.com", s.ID))
	} else {
		webhookToken = strPtr(fmt.Sprintf("token-%d", s.ID))
		webhookURL = strPtr(fmt.Sprintf("https://api.example.com/webhooks/%s/%s", s.Provider, *webhookToken))
	}
	return map[string]any{
		"id": s.ID, "name": s.Name, "provider": s.Provider, "teamId": s.TeamID,
		"alertRoutingId": s.AlertRoutingID, "isEnabled": s.IsEnabled,
		"webhookUrl": webhookURL, "webhookToken": webhookToken, "emailAddress": emailAddress,
		"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
	}
}

func (a *fakeAPI) serveAlertSources(w http.ResponseWriter, r *http.Request, rest []string, body map[string]json.RawMessage) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		s := &fakeSource{ID: a.nextSourceID, IsEnabled: true}
		_ = json.Unmarshal(body["provider"], &s.Provider)
		_ = json.Unmarshal(body["teamId"], &s.TeamID)
		if s.TeamID != nil && a.teams[*s.TeamID] == nil {
			notFound(w)
			return
		}
		if !a.applySourceBody(w, s, body) {
			return
		}
		a.nextSourceID++
		a.sources[s.ID] = s
		writeJSON(w, http.StatusCreated, s.json())
		return
	}
	if len(rest) != 1 {
		notFound(w)
		return
	}

	id, _ := strconv.Atoi(rest[0])
	s := a.sources[id]
	if s == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.json())
	case http.MethodPatch:
		for key := range body {
			if key != "name" && key != "alertRoutingId" && key != "isEnabled" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unrecognized key: " + key})
				return
			}
		}
		if a.applySourceBody(w, s, body) {
			writeJSON(w, http.StatusOK, s.json())
		}
	case http.MethodDelete:
		delete(a.sources, s.ID)
		w.WriteHeader(http.StatusNoContent)
	default:
		notFound(w)
	}
}

func (a *fakeAPI) applySourceBody(w http.ResponseWriter, s *fakeSource, body map[string]json.RawMessage) bool {
	next := *s
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &next.Name)
	}
	if raw, ok := body["isEnabled"]; ok {
		_ = json.Unmarshal(raw, &next.IsEnabled)
	}
	if raw, ok := body["alertRoutingId"]; ok {
		next.AlertRoutingID = nil
		_ = json.Unmarshal(raw, &next.AlertRoutingID)
	}
	if next.AlertRoutingID != nil {
		routing := a.routings[*next.AlertRoutingID]
		if routing == nil {
			notFound(w)
			return false
		}
		if !sameTeam(routing.TeamID, next.TeamID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Alert routing is outside the source scope"})
			return false
		}
	}
	*s = next
	return true
}

func sameTeam(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (a *fakeAPI) teamOwnsSources(teamID int) bool {
	for _, s := range a.sources {
		if s.TeamID != nil && *s.TeamID == teamID {
			return true
		}
	}
	return false
}

func (a *fakeAPI) removeSource(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sources, id)
}

func (a *fakeAPI) setSourceEnabled(id int, enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sources[id].IsEnabled = enabled
}

func (a *fakeAPI) addSourceUsing(routingID int) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextSourceID
	a.nextSourceID++
	a.sources[id] = &fakeSource{ID: id, Name: "Made in the app", Provider: "datadog", TeamID: a.routings[routingID].TeamID, AlertRoutingID: &routingID, IsEnabled: true}
	return id
}
