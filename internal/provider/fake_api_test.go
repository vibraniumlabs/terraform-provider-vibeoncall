package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu            sync.Mutex
	nextTeamID    int
	teams         map[int]*fakeTeam
	users         []fakeUser
	memberships   map[int]map[int]string
	labels        []fakeLabel
	chatChannels  []fakeChatChannel
	chatQuery     url.Values
	afterTeams    func()
	nextPolicyID  int
	nextTierID    int
	policies      map[int]*fakePolicy
	teamDefaults  map[int]int
	nextServiceID int
	services      map[int]*fakeService
	nextSchedID   int
	nextRotID     int
	schedules     map[int]*fakeSchedule
	rotationPuts  int
	nextWindowID  int
	windows       map[int]*fakeWindow
	windowPatch   map[string]json.RawMessage
}

func strPtr(s string) *string { return &s }

func newFakeAPI(t *testing.T, users ...fakeUser) (*fakeAPI, *httptest.Server) {
	api := &fakeAPI{
		nextTeamID: 1, teams: map[int]*fakeTeam{}, users: users, memberships: map[int]map[int]string{},
		nextPolicyID: 1, nextTierID: 1, policies: map[int]*fakePolicy{}, teamDefaults: map[int]int{},
		nextSchedID: 1, nextRotID: 1, schedules: map[int]*fakeSchedule{},
		nextServiceID: 1, services: map[int]*fakeService{},
		nextWindowID: 1, windows: map[int]*fakeWindow{},
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
	case parts[0] == "services":
		a.serveServices(w, r, parts[1:], body)
	case parts[0] == "escalation-policies":
		a.serveEscalationPolicies(w, r, parts[1:], body)
	case parts[0] == "schedules":
		a.serveSchedules(w, r, parts[1:], body)
	case parts[0] == "maintenance-windows":
		a.serveMaintenanceWindows(w, r, parts[1:], body)
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
	if len(a.schedules) > 0 {
		return fmt.Errorf("%d schedules left after destroy", len(a.schedules))
	}
	if len(a.services) > 0 {
		return fmt.Errorf("%d services left after destroy", len(a.services))
	}
	if len(a.windows) > 0 {
		return fmt.Errorf("%d maintenance windows left after destroy", len(a.windows))
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

const fakeSchedulePageSize = 2

type fakeRotation struct {
	ID             int             `json:"id"`
	Name           string          `json:"name"`
	StartAt        string          `json:"startAt"`
	RotationConfig json.RawMessage `json:"rotationConfig"`
	Participants   []int           `json:"participants"`
	inUse          bool
}

type fakeSchedule struct {
	ID        int             `json:"id"`
	Name      string          `json:"name"`
	Timezone  string          `json:"timezone"`
	TeamIDs   []int           `json:"teamIds"`
	Rotations []*fakeRotation `json:"rotations"`
	inUse     bool
}

func scheduleJSON(s *fakeSchedule) map[string]any {
	var teamID *int
	if len(s.TeamIDs) > 0 {
		teamID = &s.TeamIDs[0]
	}
	return map[string]any{
		"id": s.ID, "name": s.Name, "timezone": s.Timezone, "teamId": teamID, "teamIds": s.TeamIDs, "rotations": s.Rotations,
	}
}

func (a *fakeAPI) serveSchedules(w http.ResponseWriter, r *http.Request, rest []string, body map[string]json.RawMessage) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			a.listSchedules(w, r)
		case http.MethodPost:
			a.createSchedule(w, body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	id, _ := strconv.Atoi(rest[0])
	s := a.schedules[id]
	if s == nil {
		notFound(w)
		return
	}
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, scheduleJSON(s))
	case len(rest) == 1 && r.Method == http.MethodPatch:
		if len(s.Rotations) == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Schedule has no active rotations"})
			return
		}
		var name string
		if raw, ok := body["name"]; ok {
			_ = json.Unmarshal(raw, &name)
			if a.scheduleNameTaken(name, s.ID) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "Schedule name already in use"})
				return
			}
			s.Name = name
		}
		if raw, ok := body["timezone"]; ok {
			_ = json.Unmarshal(raw, &s.Timezone)
		}
		writeJSON(w, http.StatusOK, scheduleJSON(s))
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if s.inUse {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("Schedule %d is still paged by escalation policy 3", s.ID)})
			return
		}
		delete(a.schedules, s.ID)
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 2 && rest[1] == "teams" && r.Method == http.MethodPut:
		var teamIDs []int
		_ = json.Unmarshal(body["teamIds"], &teamIDs)
		if !a.teamsExist(teamIDs) {
			notFound(w)
			return
		}
		sort.Ints(teamIDs)
		s.TeamIDs = teamIDs
		writeJSON(w, http.StatusOK, scheduleJSON(s))
	case len(rest) == 2 && rest[1] == "rotations" && r.Method == http.MethodPost:
		if _, ok := body["storeParticipantsAsSent"]; ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unrecognized key: storeParticipantsAsSent"})
			return
		}
		rot := a.rotationFromBody(body)
		if rot == nil {
			notFound(w)
			return
		}
		rot.ID = a.newRotationID()
		s.Rotations = append(s.Rotations, rot)
		writeJSON(w, http.StatusCreated, map[string]int{"id": rot.ID})
	case len(rest) == 3 && rest[1] == "rotations":
		a.serveRotation(w, r, s, rest[2], body)
	default:
		notFound(w)
	}
}

func (a *fakeAPI) serveRotation(w http.ResponseWriter, r *http.Request, s *fakeSchedule, ref string, body map[string]json.RawMessage) {
	rotationID, _ := strconv.Atoi(ref)
	index := slices.IndexFunc(s.Rotations, func(rot *fakeRotation) bool { return rot.ID == rotationID })
	if index == -1 {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodPut:
		rot := a.rotationFromBody(body)
		if rot == nil {
			notFound(w)
			return
		}
		var storeAsSent bool
		_ = json.Unmarshal(body["storeParticipantsAsSent"], &storeAsSent)
		if !storeAsSent && onlyMembershipChanged(s.Rotations[index].Participants, rot.Participants) {
			rot.Participants = append(rot.Participants[1:], rot.Participants[0])
		}
		rot.ID = rotationID
		s.Rotations[index] = rot
		a.rotationPuts++
		writeJSON(w, http.StatusOK, map[string]int{"id": rotationID})
	case http.MethodDelete:
		if s.Rotations[index].inUse {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("Rotation %d is still paged by escalation policy 3", rotationID)})
			return
		}
		if len(s.Rotations) == 1 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Cannot delete the last active rotation of a schedule"})
			return
		}
		s.Rotations = slices.Delete(s.Rotations, index, index+1)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// onlyMembershipChanged mirrors the API's membership-only classification, where it rotates the stored order to keep the current responder on call.
func onlyMembershipChanged(existing, requested []int) bool {
	var kept, added []int
	for _, id := range existing {
		if slices.Contains(requested, id) {
			kept = append(kept, id)
		}
	}
	for _, id := range requested {
		if !slices.Contains(existing, id) {
			added = append(added, id)
		}
	}
	return len(requested) > 1 && !slices.Equal(existing, requested) && slices.Equal(append(kept, added...), requested)
}

func (a *fakeAPI) createSchedule(w http.ResponseWriter, body map[string]json.RawMessage) {
	s := &fakeSchedule{TeamIDs: []int{}}
	_ = json.Unmarshal(body["name"], &s.Name)
	_ = json.Unmarshal(body["timezone"], &s.Timezone)
	if raw, ok := body["teamIds"]; ok {
		_ = json.Unmarshal(raw, &s.TeamIDs)
	}
	if a.scheduleNameTaken(s.Name, 0) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Schedule name already in use"})
		return
	}
	if !a.teamsExist(s.TeamIDs) {
		notFound(w)
		return
	}
	var rotations []map[string]json.RawMessage
	_ = json.Unmarshal(body["rotations"], &rotations)
	if len(rotations) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "At least one rotation is required"})
		return
	}
	for _, rb := range rotations {
		if _, ok := rb["storeParticipantsAsSent"]; ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unrecognized key: storeParticipantsAsSent"})
			return
		}
		rot := a.rotationFromBody(rb)
		if rot == nil {
			notFound(w)
			return
		}
		rot.ID = a.newRotationID()
		s.Rotations = append(s.Rotations, rot)
	}
	sort.Ints(s.TeamIDs)
	s.ID = a.nextSchedID
	a.nextSchedID++
	a.schedules[s.ID] = s
	writeJSON(w, http.StatusCreated, scheduleJSON(s))
}

func (a *fakeAPI) rotationFromBody(body map[string]json.RawMessage) *fakeRotation {
	rot := &fakeRotation{RotationConfig: body["rotationConfig"]}
	_ = json.Unmarshal(body["name"], &rot.Name)
	_ = json.Unmarshal(body["startAt"], &rot.StartAt)
	_ = json.Unmarshal(body["participants"], &rot.Participants)
	for _, id := range rot.Participants {
		if a.findUser(id) == nil {
			return nil
		}
	}
	return rot
}

func (a *fakeAPI) newRotationID() int {
	id := a.nextRotID
	a.nextRotID++
	return id
}

func (a *fakeAPI) scheduleNameTaken(name string, exceptID int) bool {
	for _, s := range a.schedules {
		if s.Name == name && s.ID != exceptID {
			return true
		}
	}
	return false
}

func (a *fakeAPI) teamsExist(ids []int) bool {
	for _, id := range ids {
		if a.teams[id] == nil {
			return false
		}
	}
	return true
}

func (a *fakeAPI) listSchedules(w http.ResponseWriter, r *http.Request) {
	ids := make([]int, 0, len(a.schedules))
	for id := range a.schedules {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	teamID, _ := strconv.Atoi(r.URL.Query().Get("teamId"))
	matched := []map[string]any{}
	for _, id := range ids {
		s := a.schedules[id]
		if teamID == 0 || slices.Contains(s.TeamIDs, teamID) {
			matched = append(matched, scheduleJSON(s))
		}
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	start, end := min(offset, len(matched)), min(offset+fakeSchedulePageSize, len(matched))
	writeJSON(w, http.StatusOK, map[string]any{"schedules": matched[start:end], "total": len(matched), "hasMore": end < len(matched)})
}

func (a *fakeAPI) setRotationInUse(rotationID int, inUse bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, rot := range a.schedules[1].Rotations {
		if rot.ID == rotationID {
			rot.inUse = inUse
		}
	}
}

func (a *fakeAPI) setScheduleInUse(id int, inUse bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.schedules[id].inUse = inUse
}

func (a *fakeAPI) removeSchedule(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.schedules, id)
}

type fakeWindow struct {
	ID          int
	Name        string
	Description *string
	TeamID      *int
	StartAt     time.Time
	EndAt       time.Time
	Conditions  json.RawMessage
	Operator    *string
	Channels    []string
}

func (a *fakeAPI) windowJSON(mw *fakeWindow) map[string]any {
	channels := []map[string]string{}
	for _, id := range mw.Channels {
		if c := a.findChatChannel(id); c != nil {
			channels = append(channels, map[string]string{"id": c.ID, "name": c.Name})
		}
	}
	conditions := mw.Conditions
	if len(conditions) == 0 {
		conditions = json.RawMessage("null")
	}
	return map[string]any{
		"maintenanceWindow": map[string]any{
			"id": mw.ID, "name": mw.Name, "description": mw.Description, "teamId": mw.TeamID,
			"startAt": mw.StartAt.UTC().Format("2006-01-02T15:04:05.000Z"), "endAt": mw.EndAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			"conditions": conditions, "conditionsLogicalOperator": mw.Operator,
			"announcementChannels": channels, "createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:00:00.000Z",
		},
	}
}

func (a *fakeAPI) serveMaintenanceWindows(w http.ResponseWriter, r *http.Request, rest []string, body map[string]json.RawMessage) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		mw := &fakeWindow{ID: a.nextWindowID}
		if raw, ok := body["teamId"]; ok {
			_ = json.Unmarshal(raw, &mw.TeamID)
		}
		if mw.TeamID != nil && a.teams[*mw.TeamID] == nil {
			notFound(w)
			return
		}
		if !a.applyWindowBody(w, mw, body) {
			return
		}
		a.nextWindowID++
		a.windows[mw.ID] = mw
		writeJSON(w, http.StatusCreated, a.windowJSON(mw))
		return
	}

	id := 0
	if len(rest) == 1 {
		id, _ = strconv.Atoi(rest[0])
	}
	mw := a.windows[id]
	if mw == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.windowJSON(mw))
	case http.MethodPatch:
		a.windowPatch = body
		if !mw.EndAt.After(time.Now()) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Ended maintenance windows cannot be updated"})
			return
		}
		updated := *mw
		if !a.applyWindowBody(w, &updated, body) {
			return
		}
		*mw = updated
		writeJSON(w, http.StatusOK, a.windowJSON(mw))
	case http.MethodDelete:
		delete(a.windows, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *fakeAPI) applyWindowBody(w http.ResponseWriter, mw *fakeWindow, body map[string]json.RawMessage) bool {
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &mw.Name)
	}
	if raw, ok := body["description"]; ok {
		mw.Description = nil
		_ = json.Unmarshal(raw, &mw.Description)
	}
	if raw, ok := body["startAt"]; ok {
		_ = json.Unmarshal(raw, &mw.StartAt)
	}
	if raw, ok := body["endAt"]; ok {
		_ = json.Unmarshal(raw, &mw.EndAt)
	}
	if raw, ok := body["conditions"]; ok {
		mw.Conditions = raw
		if string(raw) == "null" {
			mw.Conditions = nil
		}
	}
	if raw, ok := body["conditionsLogicalOperator"]; ok {
		mw.Operator = nil
		_ = json.Unmarshal(raw, &mw.Operator)
	}
	if raw, ok := body["announcementChannels"]; ok {
		mw.Channels = nil
		_ = json.Unmarshal(raw, &mw.Channels)
		for _, id := range mw.Channels {
			if a.findChatChannel(id) == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Channel %s is not on the connected chat platform", id)})
				return false
			}
		}
	}
	if (mw.Conditions == nil) != (mw.Operator == nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "conditions and conditionsLogicalOperator must both be provided or both be null"})
		return false
	}
	return true
}

func (a *fakeAPI) findChatChannel(id string) *fakeChatChannel {
	for i := range a.chatChannels {
		if a.chatChannels[i].ID == id {
			return &a.chatChannels[i]
		}
	}
	return nil
}

func (a *fakeAPI) putWindow(mw *fakeWindow) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mw.ID = a.nextWindowID
	a.nextWindowID++
	a.windows[mw.ID] = mw
}

func (a *fakeAPI) removeWindow(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.windows, id)
}

func (a *fakeAPI) setWindowConditions(id int, operator, conditions string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.windows[id].Operator = &operator
	a.windows[id].Conditions = json.RawMessage(conditions)
}
