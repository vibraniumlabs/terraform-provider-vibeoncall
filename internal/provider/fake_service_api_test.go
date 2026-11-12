package provider

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

type fakeResponderRow struct {
	TeamID int `json:"teamId,omitempty"`
	UserID int `json:"userId,omitempty"`
}

type fakeService struct {
	ID                int
	Name              string
	Description       *string
	TeamID            *int
	ExternalProvider  *string
	ExternalServiceID *string
	responders        []fakeResponderRow
	stakeholders      []int
}

func (a *fakeAPI) serviceJSON(s *fakeService) map[string]any {
	responders := []map[string]any{}
	addTeam := func(id int, source string) {
		if team := a.teams[id]; team != nil {
			responders = append(responders, map[string]any{"type": "team", "team": map[string]any{"id": team.ID, "name": team.Name}, "source": source})
		}
	}
	if s.TeamID != nil {
		addTeam(*s.TeamID, "owner")
	}
	for _, row := range s.responders {
		switch {
		case row.TeamID != 0 && (s.TeamID == nil || row.TeamID != *s.TeamID):
			addTeam(row.TeamID, "added")
		case row.UserID != 0:
			u := a.findUser(row.UserID)
			responders = append(responders, map[string]any{"type": "user", "user": map[string]any{"id": u.ID, "email": u.Email, "fullName": u.FullName}, "source": "added"})
		}
	}
	stakeholders := []map[string]any{}
	for _, id := range s.stakeholders {
		u := a.findUser(id)
		stakeholders = append(stakeholders, map[string]any{"id": u.ID, "email": u.Email, "fullName": u.FullName})
	}
	return map[string]any{
		"id": s.ID, "name": s.Name, "description": s.Description, "teamId": s.TeamID,
		"externalProvider": s.ExternalProvider, "externalServiceId": s.ExternalServiceID,
		"responders": responders, "stakeholders": stakeholders,
		"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z", "deletedAt": nil,
	}
}

func (a *fakeAPI) serveServices(w http.ResponseWriter, r *http.Request, rest []string, body map[string]json.RawMessage) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		s := &fakeService{ID: a.nextServiceID}
		if !a.applyServiceBody(w, s, body) {
			return
		}
		a.nextServiceID++
		a.services[s.ID] = s
		writeJSON(w, http.StatusCreated, map[string]any{"service": a.serviceJSON(s)})
		return
	}
	if len(rest) == 0 {
		notFound(w)
		return
	}

	id, _ := strconv.Atoi(rest[0])
	s := a.services[id]
	if s == nil {
		notFound(w)
		return
	}
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"service": a.serviceJSON(s)})
	case len(rest) == 1 && r.Method == http.MethodPatch:
		if a.applyServiceBody(w, s, body) {
			writeJSON(w, http.StatusOK, map[string]any{"service": a.serviceJSON(s)})
		}
	case len(rest) == 1 && r.Method == http.MethodDelete:
		delete(a.services, s.ID)
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 3 && rest[1] == "responders" && r.Method == http.MethodPost:
		a.mutateResponders(w, s, rest[2], body)
	case len(rest) == 3 && rest[1] == "stakeholders" && r.Method == http.MethodPost:
		a.mutateStakeholders(w, s, rest[2], body)
	default:
		notFound(w)
	}
}

func (a *fakeAPI) applyServiceBody(w http.ResponseWriter, s *fakeService, body map[string]json.RawMessage) bool {
	next := *s
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &next.Name)
	}
	for key, field := range map[string]**string{"description": &next.Description, "externalProvider": &next.ExternalProvider, "externalServiceId": &next.ExternalServiceID} {
		if raw, ok := body[key]; ok {
			*field = nil
			_ = json.Unmarshal(raw, field)
		}
	}
	if raw, ok := body["teamId"]; ok {
		next.TeamID = nil
		_ = json.Unmarshal(raw, &next.TeamID)
	}
	if next.TeamID != nil && a.teams[*next.TeamID] == nil {
		notFound(w)
		return false
	}
	for _, other := range a.services {
		if other.ID != s.ID && strings.EqualFold(other.Name, next.Name) {
			writeJSON(w, http.StatusConflict, map[string]string{"message": "A service with this name already exists"})
			return false
		}
	}
	*s = next
	return true
}

func (a *fakeAPI) mutateResponders(w http.ResponseWriter, s *fakeService, action string, body map[string]json.RawMessage) {
	var refs []fakeResponderRow
	_ = json.Unmarshal(body["responders"], &refs)
	for _, ref := range refs {
		if (ref.TeamID != 0 && a.teams[ref.TeamID] == nil) || (ref.UserID != 0 && a.findUser(ref.UserID) == nil) {
			notFound(w)
			return
		}
	}
	for _, ref := range refs {
		if s.TeamID != nil && ref.TeamID == *s.TeamID {
			continue
		}
		s.responders = addOrRemove(s.responders, ref, action)
	}
	writeJSON(w, http.StatusOK, map[string]any{"responders": a.serviceJSON(s)["responders"]})
}

func (a *fakeAPI) mutateStakeholders(w http.ResponseWriter, s *fakeService, action string, body map[string]json.RawMessage) {
	var userIDs []int
	_ = json.Unmarshal(body["userIds"], &userIDs)
	for _, id := range userIDs {
		if a.findUser(id) == nil {
			notFound(w)
			return
		}
	}
	for _, id := range userIDs {
		s.stakeholders = addOrRemove(s.stakeholders, id, action)
	}
	writeJSON(w, http.StatusOK, map[string]any{"stakeholders": a.serviceJSON(s)["stakeholders"]})
}

func (a *fakeAPI) removeService(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.services, id)
}

func (a *fakeAPI) setServiceOwner(id, teamID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.services[id].TeamID = &teamID
}

func addOrRemove[T comparable](rows []T, row T, action string) []T {
	i := slices.Index(rows, row)
	switch {
	case action == "add" && i < 0:
		return append(rows, row)
	case action == "remove" && i >= 0:
		return slices.Delete(rows, i, i+1)
	}
	return rows
}
