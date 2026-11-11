package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type fakeTeam struct {
	ID          int     `json:"id"`
	ShortID     string  `json:"shortId"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

type fakeUser struct {
	ID       int     `json:"id"`
	Email    string  `json:"email"`
	FullName *string `json:"fullName"`
	Status   string  `json:"status"`
}

type fakeLabel struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type fakeChatChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fakeScheduleTarget struct {
	ScheduleID int    `json:"scheduleId"`
	Mode       string `json:"mode"`
}

type fakeRotationTarget struct {
	ScheduleID int    `json:"scheduleId"`
	RotationID int    `json:"rotationId"`
	Mode       string `json:"mode"`
}

type fakeTier struct {
	ID                           int                  `json:"id"`
	AcknowledgementTimeThreshold int                  `json:"acknowledgementTimeThreshold"`
	ScheduleTargets              []fakeScheduleTarget `json:"scheduleTargets"`
	RotationTargets              []fakeRotationTarget `json:"rotationTargets"`
	UserIDs                      []int                `json:"userIds"`
}

type fakePolicy struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	TeamID      *int       `json:"teamId"`
	Tiers       []fakeTier `json:"tiers"`
	inUse       bool
}

type fakeAPI struct {
	mu           sync.Mutex
	nextTeamID   int
	teams        map[int]*fakeTeam
	users        []fakeUser
	memberships  map[int]map[int]string
	labels       []fakeLabel
	chatChannels []fakeChatChannel
	chatQuery    url.Values
	afterTeams   func()
	nextPolicyID int
	nextTierID   int
	policies     map[int]*fakePolicy
	teamDefaults map[int]int
}

func strPtr(s string) *string { return &s }

func newFakeAPI(t *testing.T, users ...fakeUser) (*fakeAPI, *httptest.Server) {
	api := &fakeAPI{
		nextTeamID: 1, teams: map[int]*fakeTeam{}, users: users, memberships: map[int]map[int]string{},
		nextPolicyID: 1, nextTierID: 1, policies: map[int]*fakePolicy{}, teamDefaults: map[int]int{},
	}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return api, srv
}

func (a *fakeAPI) findTeam(ref string) *fakeTeam {
	for _, team := range a.teams {
		if strconv.Itoa(team.ID) == ref || team.ShortID == ref {
			return team
		}
	}
	return nil
}

func (a *fakeAPI) findUser(id int) *fakeUser {
	for i := range a.users {
		if a.users[i].ID == id {
			return &a.users[i]
		}
	}
	return nil
}

func (a *fakeAPI) removeTeam(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if team := a.findTeam(ref); team != nil {
		delete(a.teams, team.ID)
	}
}

func (a *fakeAPI) removeMembership(teamID, userID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.memberships[teamID], userID)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	var body map[string]json.RawMessage
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	switch {
	case len(parts) == 1 && parts[0] == "members" && r.Method == http.MethodGet:
		a.listMembers(w, r)
	case len(parts) == 1 && parts[0] == "teams" && r.Method == http.MethodGet:
		a.listTeams(w, r)
	case len(parts) == 1 && parts[0] == "labels" && r.Method == http.MethodGet:
		a.listLabels(w)
	case r.URL.Path == "/integrations/chat/channels" && r.Method == http.MethodGet:
		a.listChatChannels(w, r)
	case len(parts) == 1 && parts[0] == "teams" && r.Method == http.MethodPost:
		team := &fakeTeam{ID: a.nextTeamID, ShortID: fmt.Sprintf("VP-TEAM-%d", a.nextTeamID)}
		a.nextTeamID++
		applyTeamBody(team, body)
		a.teams[team.ID] = team
		writeJSON(w, http.StatusCreated, team)
	case parts[0] == "escalation-policies":
		a.serveEscalationPolicies(w, r, parts[1:], body)
	case len(parts) >= 2 && parts[0] == "teams":
		team := a.findTeam(parts[1])
		if team == nil {
			notFound(w)
			return
		}
		if len(parts) == 2 {
			a.serveTeam(w, r, team, body)
			return
		}
		a.serveTeamMembers(w, r, team, parts[2:], body)
	default:
		notFound(w)
	}
}

func (a *fakeAPI) serveTeam(w http.ResponseWriter, r *http.Request, team *fakeTeam, body map[string]json.RawMessage) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, team)
	case http.MethodPatch:
		applyTeamBody(team, body)
		writeJSON(w, http.StatusOK, team)
	case http.MethodDelete:
		delete(a.teams, team.ID)
		delete(a.memberships, team.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *fakeAPI) serveTeamMembers(w http.ResponseWriter, r *http.Request, team *fakeTeam, rest []string, body map[string]json.RawMessage) {
	members := a.memberships[team.ID]
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		users := []map[string]any{}
		ids := make([]int, 0, len(members))
		for id := range members {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			u := a.findUser(id)
			users = append(users, map[string]any{"id": u.ID, "email": u.Email, "fullName": u.FullName, "status": u.Status, "role": members[id]})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users, "total": len(users), "hasMore": false})
	case len(rest) == 1 && r.Method == http.MethodPost:
		var userID int
		_ = json.Unmarshal(body["userId"], &userID)
		role := "member"
		if raw, ok := body["role"]; ok {
			_ = json.Unmarshal(raw, &role)
		}
		if a.findUser(userID) == nil {
			notFound(w)
			return
		}
		if members == nil {
			members = map[int]string{}
			a.memberships[team.ID] = members
		}
		members[userID] = role
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "userId": userID, "role": role})
	case len(rest) == 2 && r.Method == http.MethodDelete:
		userID, _ := strconv.Atoi(rest[1])
		if _, ok := members[userID]; !ok {
			notFound(w)
			return
		}
		delete(members, userID)
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	default:
		notFound(w)
	}
}

func pageBounds(r *http.Request, n int) (start, end int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	return min(offset, n), min(offset+limit, n)
}

func (a *fakeAPI) listTeams(w http.ResponseWriter, r *http.Request) {
	teams := make([]*fakeTeam, 0, len(a.teams))
	for _, team := range a.teams {
		teams = append(teams, team)
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].ID < teams[j].ID })
	start, end := pageBounds(r, len(teams))
	writeJSON(w, http.StatusOK, map[string]any{"teams": teams[start:end], "total": len(teams), "hasMore": end < len(teams)})
	if a.afterTeams != nil {
		a.afterTeams()
	}
}

func (a *fakeAPI) listLabels(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"labels": a.labels})
}

func (a *fakeAPI) listChatChannels(w http.ResponseWriter, r *http.Request) {
	a.chatQuery = r.URL.Query()
	search := strings.ToLower(a.chatQuery.Get("search"))
	matched := []fakeChatChannel{}
	for _, c := range a.chatChannels {
		if strings.Contains(strings.ToLower(c.Name), search) {
			matched = append(matched, c)
		}
	}
	start, end := pageBounds(r, len(matched))
	writeJSON(w, http.StatusOK, map[string]any{"channels": matched[start:end], "total": len(matched), "hasMore": end < len(matched)})
}

func (a *fakeAPI) listMembers(w http.ResponseWriter, r *http.Request) {
	search := strings.ToLower(r.URL.Query().Get("search"))

	matched := []map[string]any{}
	for _, u := range a.users {
		name := ""
		if u.FullName != nil {
			name = *u.FullName
		}
		if strings.Contains(strings.ToLower(u.Email), search) || strings.Contains(strings.ToLower(name), search) {
			matched = append(matched, map[string]any{
				"id": u.ID, "email": u.Email, "fullName": u.FullName, "status": u.Status, "role": nil, "labels": []any{},
				"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
			})
		}
	}
	start, end := pageBounds(r, len(matched))
	writeJSON(w, http.StatusOK, map[string]any{"members": matched[start:end], "total": len(matched), "hasMore": end < len(matched)})
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

func (a *fakeAPI) checkNothingLeft(*terraform.State) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.teams) > 0 {
		return fmt.Errorf("%d teams left after destroy", len(a.teams))
	}
	if len(a.policies) > 0 {
		return fmt.Errorf("%d escalation policies left after destroy", len(a.policies))
	}
	for teamID, members := range a.memberships {
		if len(members) > 0 {
			return fmt.Errorf("team %d still has %d members after destroy", teamID, len(members))
		}
	}
	return nil
}

func protoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"vibeoncall": providerserver.NewProtocol6WithError(New("test")()),
	}
}

func providerConfig(baseURL string) string {
	return fmt.Sprintf(`
provider "vibeoncall" {
  api_key  = "test"
  base_url = %q
}
`, baseURL)
}

const fakePolicyPageSize = 1

func (a *fakeAPI) policyJSON(p *fakePolicy) map[string]any {
	isDefault := p.TeamID != nil && a.teamDefaults[*p.TeamID] == p.ID
	return map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description, "teamId": p.TeamID, "isDefault": isDefault,
		"updatedAt": "2026-01-01T00:00:00Z", "tiers": p.Tiers,
	}
}

func (a *fakeAPI) serveEscalationPolicies(w http.ResponseWriter, r *http.Request, rest []string, body map[string]json.RawMessage) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			a.listPolicies(w, r)
		case http.MethodPost:
			p := &fakePolicy{ID: a.nextPolicyID}
			a.nextPolicyID++
			if raw, ok := body["teamId"]; ok {
				_ = json.Unmarshal(raw, &p.TeamID)
			}
			if p.TeamID != nil && a.teams[*p.TeamID] == nil {
				notFound(w)
				return
			}
			a.applyPolicyBody(p, body)
			a.policies[p.ID] = p
			writeJSON(w, http.StatusCreated, a.policyJSON(p))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	id, _ := strconv.Atoi(rest[0])
	p := a.policies[id]
	if p == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.policyJSON(p))
	case http.MethodPatch:
		if raw, ok := body["isDefault"]; ok {
			var isDefault bool
			_ = json.Unmarshal(raw, &isDefault)
			if isDefault && p.TeamID == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Only team policies can be default"})
				return
			}
			if isDefault {
				a.teamDefaults[*p.TeamID] = p.ID
			} else if p.TeamID != nil && a.teamDefaults[*p.TeamID] == p.ID {
				delete(a.teamDefaults, *p.TeamID)
			}
		}
		a.applyPolicyBody(p, body)
		writeJSON(w, http.StatusOK, a.policyJSON(p))
	case http.MethodDelete:
		if p.inUse {
			writeJSON(w, http.StatusConflict, map[string]string{"message": fmt.Sprintf("Escalation policy %d still has 1 reference(s)", p.ID)})
			return
		}
		a.deletePolicyLocked(p)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *fakeAPI) listPolicies(w http.ResponseWriter, r *http.Request) {
	matched := []map[string]any{}
	ids := make([]int, 0, len(a.policies))
	for id := range a.policies {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	teamID := r.URL.Query().Get("teamId")
	for _, id := range ids {
		p := a.policies[id]
		if teamID == "" || (p.TeamID != nil && strconv.Itoa(*p.TeamID) == teamID) {
			matched = append(matched, a.policyJSON(p))
		}
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	start, end := min(offset, len(matched)), min(offset+fakePolicyPageSize, len(matched))
	writeJSON(w, http.StatusOK, map[string]any{"policies": matched[start:end], "total": len(matched), "hasMore": end < len(matched)})
}

func (a *fakeAPI) applyPolicyBody(p *fakePolicy, body map[string]json.RawMessage) {
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &p.Name)
	}
	if raw, ok := body["description"]; ok {
		_ = json.Unmarshal(raw, &p.Description)
	}
	if raw, ok := body["tiers"]; ok {
		var tiers []fakeTier
		_ = json.Unmarshal(raw, &tiers)
		for i := range tiers {
			tiers[i].ID = a.nextTierID
			a.nextTierID++
			tiers[i].ScheduleTargets = append([]fakeScheduleTarget{}, tiers[i].ScheduleTargets...)
			tiers[i].RotationTargets = append([]fakeRotationTarget{}, tiers[i].RotationTargets...)
			tiers[i].UserIDs = append([]int{}, tiers[i].UserIDs...)
		}
		p.Tiers = tiers
	}
}

func (a *fakeAPI) deletePolicyLocked(p *fakePolicy) {
	if p.TeamID != nil && a.teamDefaults[*p.TeamID] == p.ID {
		delete(a.teamDefaults, *p.TeamID)
	}
	delete(a.policies, p.ID)
}

func (a *fakeAPI) removePolicy(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.policies[id]; p != nil {
		a.deletePolicyLocked(p)
	}
}

func (a *fakeAPI) setPolicyInUse(id int, inUse bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.policies[id].inUse = inUse
}

func (a *fakeAPI) setTeamDefault(teamID, policyID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if policyID == 0 {
		delete(a.teamDefaults, teamID)
		return
	}
	a.teamDefaults[teamID] = policyID
}
