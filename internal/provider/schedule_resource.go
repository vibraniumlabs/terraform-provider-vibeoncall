package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

const dateLayout = "2006-01-02"

var weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

var (
	clockTime = stringvalidator.RegexMatches(regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`), "must be a 24-hour time in HH:MM format")
	weekday   = stringvalidator.OneOf(weekdays...)
)

type calendarDate struct{}

func (calendarDate) Description(context.Context) string {
	return "must be a date in YYYY-MM-DD format"
}

func (v calendarDate) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v calendarDate) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(dateLayout, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid date", fmt.Sprintf("%q %s.", req.ConfigValue.ValueString(), v.Description(ctx)))
	}
}

var (
	_ resource.Resource                = &ScheduleResource{}
	_ resource.ResourceWithImportState = &ScheduleResource{}
	_ resource.ResourceWithModifyPlan  = &ScheduleResource{}
)

func NewScheduleResource() resource.Resource {
	return &ScheduleResource{}
}

type ScheduleResource struct {
	client *client.ClientWithResponses
}

type ScheduleResourceModel struct {
	ID        types.String            `tfsdk:"id"`
	Name      types.String            `tfsdk:"name"`
	TimeZone  types.String            `tfsdk:"time_zone"`
	TeamIDs   types.Set               `tfsdk:"team_ids"`
	Rotations []scheduleRotationModel `tfsdk:"rotations"`
}

type scheduleRotationModel struct {
	ID             types.String        `tfsdk:"id"`
	Name           types.String        `tfsdk:"name"`
	StartDate      types.String        `tfsdk:"start_date"`
	ParticipantIDs types.List          `tfsdk:"participant_ids"`
	Daily          *dailyRotationModel `tfsdk:"daily"`
	Weekly         *weeklyRangeModel   `tfsdk:"weekly"`
	WeeklyWindows  *weeklyWindowsModel `tfsdk:"weekly_windows"`
}

type dailyRotationModel struct {
	StartTime types.String `tfsdk:"start_time"`
	EndTime   types.String `tfsdk:"end_time"`
}

type weeklyRangeModel struct {
	StartDay       types.String `tfsdk:"start_day"`
	StartTime      types.String `tfsdk:"start_time"`
	EndDay         types.String `tfsdk:"end_day"`
	EndTime        types.String `tfsdk:"end_time"`
	DailyStartTime types.String `tfsdk:"daily_start_time"`
	DailyEndTime   types.String `tfsdk:"daily_end_time"`
	HandOffDay     types.String `tfsdk:"hand_off_day"`
}

type weeklyWindowsModel struct {
	Windows    []weeklyWindowModel `tfsdk:"windows"`
	HandOffDay types.String        `tfsdk:"hand_off_day"`
}

type weeklyWindowModel struct {
	StartDay  types.String `tfsdk:"start_day"`
	StartTime types.String `tfsdk:"start_time"`
	EndDay    types.String `tfsdk:"end_day"`
	EndTime   types.String `tfsdk:"end_time"`
}

func (r *ScheduleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_schedule"
}

func timeAttribute(description string, required bool) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description + " A 24-hour time in `HH:MM` format, such as `17:30`.",
		Required:            required,
		Optional:            !required,
		Validators:          []validator.String{clockTime},
	}
}

func weekdayAttribute(description string, required bool) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description + " A lowercase weekday name, such as `monday`.",
		Required:            required,
		Optional:            !required,
		Validators:          []validator.String{weekday},
	}
}

func (r *ScheduleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An on-call schedule and its rotations.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric schedule ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Schedule name, unique within the organization.",
				Required:            true,
				Validators:          []validator.String{utf16LengthAtMost(255), noSurroundingWhitespace},
			},
			"time_zone": schema.StringAttribute{
				MarkdownDescription: "IANA time zone the rotation times are in, such as `America/New_York`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"team_ids": schema.SetAttribute{
				MarkdownDescription: "Numeric IDs of the teams that own the schedule. Omit for a global schedule.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(numericID)},
			},
			"rotations": schema.ListNestedAttribute{
				MarkdownDescription: "Rotations in display order. Each needs exactly one of `daily`, `weekly`, or `weekly_windows`. " +
					"Rotations are matched to existing ones by name, and rotations sharing a name by their order, so editing or renaming one keeps its ID and overrides. " +
					"Changing the order rewrites the rotations in place from their positions.",
				Required:   true,
				Validators: []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Numeric rotation ID, used by `rotation_targets` in `vibeoncall_escalation_policy`.",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "Rotation name.",
							Required:            true,
							Validators:          []validator.String{utf16LengthAtMost(255), noSurroundingWhitespace},
						},
						"start_date": schema.StringAttribute{
							MarkdownDescription: "First date the rotation takes effect, in `YYYY-MM-DD` format. The first participant is on call from this date, so it and the participant order together decide who is on call now.",
							Required:            true,
							Validators:          []validator.String{calendarDate{}},
						},
						"participant_ids": schema.ListAttribute{
							MarkdownDescription: "Numeric user IDs in the order they take turns. Removing someone can change who is on call now.",
							ElementType:         types.StringType,
							Required:            true,
							Validators: []validator.List{
								listvalidator.SizeAtLeast(1),
								listvalidator.UniqueValues(),
								listvalidator.ValueStringsAre(numericID),
							},
						},
						"daily": schema.SingleNestedAttribute{
							MarkdownDescription: "Hand off every day. Coverage runs from `start_time` to `end_time`; equal times cover the whole day.",
							Optional:            true,
							Validators: []validator.Object{objectvalidator.ExactlyOneOf(
								path.MatchRelative().AtParent().AtName("weekly"),
								path.MatchRelative().AtParent().AtName("weekly_windows"),
							)},
							Attributes: map[string]schema.Attribute{
								"start_time": timeAttribute("Coverage start and handoff time.", true),
								"end_time":   timeAttribute("Coverage end time.", true),
							},
						},
						"weekly": schema.SingleNestedAttribute{
							MarkdownDescription: "Hand off every week, covering one range from `start_day` `start_time` to `end_day` `end_time`. The same start and end covers the whole week.",
							Optional:            true,
							Attributes: map[string]schema.Attribute{
								"start_day":        weekdayAttribute("First day of the range.", true),
								"start_time":       timeAttribute("Time on `start_day` the range starts.", true),
								"end_day":          weekdayAttribute("Last day of the range.", true),
								"end_time":         timeAttribute("Time on `end_day` the range ends.", true),
								"daily_start_time": timeAttribute("Limits coverage within the range to these daily hours.", false),
								"daily_end_time":   timeAttribute("End of the daily hours.", false),
								"hand_off_day":     weekdayAttribute("Hand off at 00:00 on this day instead of at the range start.", false),
							},
						},
						"weekly_windows": schema.SingleNestedAttribute{
							MarkdownDescription: "Hand off every week, covering several windows.",
							Optional:            true,
							Attributes: map[string]schema.Attribute{
								"windows": schema.ListNestedAttribute{
									MarkdownDescription: "Coverage windows within the week.",
									Required:            true,
									Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"start_day":  weekdayAttribute("Day the window starts.", true),
											"start_time": timeAttribute("Time the window starts.", true),
											"end_day":    weekdayAttribute("Day the window ends.", true),
											"end_time":   timeAttribute("Time the window ends.", true),
										},
									},
								},
								"hand_off_day": weekdayAttribute("Hand off at 00:00 on this day instead of at the earliest window start.", false),
							},
						},
					},
				},
			},
		},
	}
}

func (r *ScheduleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *ScheduleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state ScheduleResourceModel
	if req.Plan.Get(ctx, &plan).HasError() || req.State.Get(ctx, &state).HasError() {
		return
	}
	desired, ok := rotationNames(plan.Rotations)
	if !ok {
		return
	}
	prior := priorRotations(state.Rotations)
	assigned := matchRotations(prior, desired)
	if rewritten := rewrittenRotations(prior, desired, assigned); len(rewritten) > 0 {
		resp.Diagnostics.AddWarning("Rotations will be rewritten in place",
			"The API can't move a rotation, so these rotations keep their IDs and take new content. "+
				"Overrides, escalation policies and routing rules that target these IDs will apply to the new content:\n"+
				strings.Join(rewritten, "\n"))
	}
	for i, id := range assigned {
		plan.Rotations[i].ID = types.StringUnknown()
		if id != "" {
			plan.Rotations[i].ID = types.StringValue(id)
		}
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("rotations"), plan.Rotations)...)
}

func (r *ScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rotations := make([]client.PublicScheduleRotationInput, 0, len(plan.Rotations))
	for _, rot := range plan.Rotations {
		in, diags := rotationToAPI(ctx, rot)
		resp.Diagnostics.Append(diags...)
		rotations = append(rotations, in)
	}
	teamIDs := teamIDsToAPI(ctx, plan.TeamIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.CreateScheduleWithResponse(ctx, client.CreateScheduleJSONRequestBody{
		Name:      plan.Name.ValueString(),
		Timezone:  plan.TimeZone.ValueString(),
		TeamIds:   &teamIDs,
		Rotations: rotations,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create schedule failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create schedule failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setScheduleState(&plan, res.JSON201)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ScheduleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	if !r.read(ctx, id, &state, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ScheduleResource) read(ctx context.Context, id int32, m *ScheduleResourceModel, diags *diag.Diagnostics) (found bool) {
	res, err := r.client.GetScheduleWithResponse(ctx, id)
	if err != nil {
		diags.AddError("Read schedule failed", err.Error())
		return false
	}
	if res.StatusCode() == http.StatusNotFound {
		return false
	}
	if res.JSON200 == nil {
		diags.AddError("Read schedule failed", apiError(res.StatusCode(), res.Body))
		return false
	}
	diags.Append(setScheduleState(m, res.JSON200)...)
	return true
}

func (r *ScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	r.applyUpdate(ctx, id, plan, state, &resp.Diagnostics)
	if !r.read(ctx, id, &plan, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Read schedule failed", "The schedule was deleted during the update.")
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ScheduleResource) applyUpdate(ctx context.Context, id int32, plan, state ScheduleResourceModel, diags *diag.Diagnostics) {
	r.updateRotations(ctx, id, plan.Rotations, state.Rotations, diags)
	if diags.HasError() {
		return
	}

	if !plan.Name.Equal(state.Name) || !plan.TimeZone.Equal(state.TimeZone) {
		res, err := r.client.UpdateScheduleWithResponse(ctx, id, client.UpdateScheduleJSONRequestBody{
			Name:     plan.Name.ValueStringPointer(),
			Timezone: plan.TimeZone.ValueStringPointer(),
		})
		if err != nil {
			diags.AddError("Update schedule failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			diags.AddError("Update schedule failed", apiError(res.StatusCode(), res.Body))
			return
		}
	}

	if !plan.TeamIDs.Equal(state.TeamIDs) {
		teamIDs := teamIDsToAPI(ctx, plan.TeamIDs, diags)
		if diags.HasError() {
			return
		}
		res, err := r.client.ReplaceScheduleTeamsWithResponse(ctx, id, client.ReplaceScheduleTeamsJSONRequestBody{TeamIds: teamIDs})
		if err != nil {
			diags.AddError("Update schedule teams failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			diags.AddError("Update schedule teams failed", apiError(res.StatusCode(), res.Body))
			return
		}
	}
}

func (r *ScheduleResource) updateRotations(ctx context.Context, scheduleID int32, planned, prior []scheduleRotationModel, diags *diag.Diagnostics) {
	assigned, _ := assignRotationIDs(planned, prior)

	priorByID := map[string]scheduleRotationModel{}
	for _, rot := range prior {
		priorByID[rot.ID.ValueString()] = rot
	}

	for i, rot := range planned {
		in, d := rotationToAPI(ctx, rot)
		diags.Append(d...)
		if diags.HasError() {
			return
		}
		if assigned[i] == "" {
			res, err := r.client.CreateScheduleRotationWithResponse(ctx, scheduleID, in)
			if err != nil {
				diags.AddError("Create rotation failed", err.Error())
				return
			}
			if res.JSON201 == nil {
				diags.AddError("Create rotation failed", apiError(res.StatusCode(), res.Body))
				return
			}
			continue
		}
		if sameRotation(rot, priorByID[assigned[i]]) {
			continue
		}
		rotationID, ok := int32ID(types.StringValue(assigned[i]), "rotation id", diags)
		if !ok {
			return
		}
		storeAsSent := true
		res, err := r.client.UpdateScheduleRotationWithResponse(ctx, scheduleID, rotationID, client.PublicScheduleRotationUpdateInput{
			Name:                    in.Name,
			StartAt:                 in.StartAt,
			RotationConfig:          in.RotationConfig,
			Participants:            in.Participants,
			StoreParticipantsAsSent: &storeAsSent,
		})
		if err != nil {
			diags.AddError("Update rotation failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			diags.AddError("Update rotation failed", apiError(res.StatusCode(), res.Body))
			return
		}
	}

	for _, rot := range prior {
		if slices.Contains(assigned, rot.ID.ValueString()) {
			continue
		}
		rotationID, ok := int32ID(rot.ID, "rotation id", diags)
		if !ok {
			return
		}
		res, err := r.client.DeleteScheduleRotationWithResponse(ctx, scheduleID, rotationID)
		if err != nil {
			diags.AddError("Delete rotation failed", err.Error())
			return
		}
		if code := res.StatusCode(); code != http.StatusNoContent && code != http.StatusNotFound {
			diags.AddError("Delete rotation failed", apiError(code, res.Body))
			return
		}
	}
}

func (r *ScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ScheduleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.DeleteScheduleWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Delete schedule failed", err.Error())
		return
	}
	switch res.StatusCode() {
	case http.StatusNoContent, http.StatusNotFound:
	case http.StatusConflict:
		resp.Diagnostics.AddError("Schedule is still in use",
			"An escalation policy or routing rule still pages this schedule or one of its rotations. Remove it from those, then destroy again.\n\n"+apiError(res.StatusCode(), res.Body))
	default:
		resp.Diagnostics.AddError("Delete schedule failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *ScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric schedule ID, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func sameRotation(a, b scheduleRotationModel) bool {
	return a.Name.Equal(b.Name) && a.StartDate.Equal(b.StartDate) && a.ParticipantIDs.Equal(b.ParticipantIDs) &&
		reflect.DeepEqual(rotationConfigToAPI(a), rotationConfigToAPI(b))
}

type priorRotation struct {
	ID   string
	Name string
}

func assignRotationIDs(planned, prior []scheduleRotationModel) ([]string, bool) {
	desired, ok := rotationNames(planned)
	if !ok {
		return nil, false
	}
	return matchRotations(priorRotations(prior), desired), true
}

func rotationNames(rotations []scheduleRotationModel) ([]string, bool) {
	names := make([]string, len(rotations))
	for i, rot := range rotations {
		if rot.Name.IsUnknown() {
			return nil, false
		}
		names[i] = rot.Name.ValueString()
	}
	return names, true
}

func priorRotations(rotations []scheduleRotationModel) []priorRotation {
	out := make([]priorRotation, len(rotations))
	for i, rot := range rotations {
		out[i] = priorRotation{ID: rot.ID.ValueString(), Name: rot.Name.ValueString()}
	}
	return out
}

func rewrittenRotations(prior []priorRotation, desired, assigned []string) []string {
	priorNames := make([]string, len(prior))
	for i, p := range prior {
		priorNames[i] = p.Name
	}
	var out []string
	for i, id := range assigned {
		if id == "" {
			continue
		}
		name := priorNames[slices.IndexFunc(prior, func(p priorRotation) bool { return p.ID == id })]
		if name == desired[i] || countName(desired, name) < countName(priorNames, name) {
			continue
		}
		out = append(out, fmt.Sprintf("- rotation %s changes from %q to %q", id, name, desired[i]))
	}
	return out
}

func countName(names []string, name string) int {
	n := 0
	for _, v := range names {
		if v == name {
			n++
		}
	}
	return n
}

// The API keeps rotation positions and only appends, so every candidate must leave the rotations in the desired order.
func matchRotations(prior []priorRotation, desired []string) []string {
	byName := matchRotationsByName(prior, desired)
	for _, candidate := range [][]string{pairUnmatchedRotations(prior, byName), byName} {
		if keepsRotationOrder(prior, candidate) {
			return candidate
		}
	}
	byPosition := make([]string, len(desired))
	for i := range min(len(desired), len(prior)) {
		byPosition[i] = prior[i].ID
	}
	return byPosition
}

func matchRotationsByName(prior []priorRotation, desired []string) []string {
	used := make([]bool, len(prior))
	out := make([]string, len(desired))
	for i, name := range desired {
		for j, p := range prior {
			if !used[j] && p.Name == name {
				used[j] = true
				out[i] = p.ID
				break
			}
		}
	}
	return out
}

func pairUnmatchedRotations(prior []priorRotation, matched []string) []string {
	var unmatched []string
	for _, p := range prior {
		if !slices.Contains(matched, p.ID) {
			unmatched = append(unmatched, p.ID)
		}
	}
	out := slices.Clone(matched)
	for i := range out {
		if out[i] == "" && len(unmatched) > 0 {
			out[i], unmatched = unmatched[0], unmatched[1:]
		}
	}
	return out
}

func keepsRotationOrder(prior []priorRotation, assigned []string) bool {
	last, created := -1, false
	for _, id := range assigned {
		if id == "" {
			created = true
			continue
		}
		pos := slices.IndexFunc(prior, func(p priorRotation) bool { return p.ID == id })
		if created || pos < last {
			return false
		}
		last = pos
	}
	return true
}

func teamIDsToAPI(ctx context.Context, set types.Set, diags *diag.Diagnostics) []int32 {
	var ids []types.String
	diags.Append(set.ElementsAs(ctx, &ids, false)...)
	out := make([]int32, 0, len(ids))
	for _, id := range ids {
		if n, ok := int32ID(id, "team_ids", diags); ok {
			out = append(out, n)
		}
	}
	return out
}

type apiWindow struct {
	StartDay  int    `json:"startDay"`
	StartTime string `json:"startTime"`
	EndDay    int    `json:"endDay"`
	EndTime   string `json:"endTime"`
}

type apiRotationPayload struct {
	StartDay       *int        `json:"startDay,omitempty"`
	StartTime      *string     `json:"startTime,omitempty"`
	EndDay         *int        `json:"endDay,omitempty"`
	EndTime        *string     `json:"endTime,omitempty"`
	DailyStartTime *string     `json:"dailyStartTime,omitempty"`
	DailyEndTime   *string     `json:"dailyEndTime,omitempty"`
	Windows        []apiWindow `json:"windows,omitempty"`
	HandOffDay     *int        `json:"handOffDay,omitempty"`
}

type apiRotationConfig struct {
	Type    string             `json:"type"`
	Payload apiRotationPayload `json:"payload"`
}

func weekdayNumber(v types.String) int {
	return slices.Index(weekdays, v.ValueString())
}

func optionalWeekday(v types.String) *int {
	if v.IsNull() {
		return nil
	}
	n := weekdayNumber(v)
	return &n
}

func weekdayName(n int) types.String {
	if n < 0 || n >= len(weekdays) {
		return types.StringValue(fmt.Sprint(n))
	}
	return types.StringValue(weekdays[n])
}

func optionalWeekdayName(n *int) types.String {
	if n == nil {
		return types.StringNull()
	}
	return weekdayName(*n)
}

func rotationConfigToAPI(rot scheduleRotationModel) apiRotationConfig {
	switch {
	case rot.Daily != nil:
		return apiRotationConfig{Type: "daily", Payload: apiRotationPayload{
			StartTime: rot.Daily.StartTime.ValueStringPointer(),
			EndTime:   rot.Daily.EndTime.ValueStringPointer(),
		}}
	case rot.Weekly != nil:
		w := rot.Weekly
		startDay, endDay := weekdayNumber(w.StartDay), weekdayNumber(w.EndDay)
		return apiRotationConfig{Type: "weekly", Payload: apiRotationPayload{
			StartDay:       &startDay,
			StartTime:      w.StartTime.ValueStringPointer(),
			EndDay:         &endDay,
			EndTime:        w.EndTime.ValueStringPointer(),
			DailyStartTime: w.DailyStartTime.ValueStringPointer(),
			DailyEndTime:   w.DailyEndTime.ValueStringPointer(),
			HandOffDay:     optionalWeekday(w.HandOffDay),
		}}
	default:
		w := rot.WeeklyWindows
		windows := make([]apiWindow, len(w.Windows))
		for i, win := range w.Windows {
			windows[i] = apiWindow{
				StartDay: weekdayNumber(win.StartDay), StartTime: win.StartTime.ValueString(),
				EndDay: weekdayNumber(win.EndDay), EndTime: win.EndTime.ValueString(),
			}
		}
		return apiRotationConfig{Type: "weekly", Payload: apiRotationPayload{Windows: windows, HandOffDay: optionalWeekday(w.HandOffDay)}}
	}
}

func rotationToAPI(ctx context.Context, rot scheduleRotationModel) (client.PublicScheduleRotationInput, diag.Diagnostics) {
	var diags diag.Diagnostics
	in := client.PublicScheduleRotationInput{Name: rot.Name.ValueString()}

	startAt, err := time.Parse(dateLayout, rot.StartDate.ValueString())
	if err != nil {
		diags.AddError("Invalid start_date", err.Error())
		return in, diags
	}
	in.StartAt = openapi_types.Date{Time: startAt}

	var participants []types.String
	diags.Append(rot.ParticipantIDs.ElementsAs(ctx, &participants, false)...)
	in.Participants = make([]int, len(participants))
	for i, p := range participants {
		in.Participants[i] = mustAtoi(p)
	}

	raw, err := json.Marshal(rotationConfigToAPI(rot))
	if err == nil {
		err = in.RotationConfig.UnmarshalJSON(raw)
	}
	if err != nil {
		diags.AddError("Invalid rotation config", err.Error())
	}
	return in, diags
}

func rotationFromAPI(rot client.PublicScheduleRotationResponse) (scheduleRotationModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	participants := make([]attr.Value, len(rot.Participants))
	for i, p := range rot.Participants {
		participants[i] = itoa(p)
	}
	m := scheduleRotationModel{
		ID:             itoa(int(rot.Id)),
		Name:           types.StringValue(rot.Name),
		StartDate:      types.StringValue(rot.StartAt.Format(dateLayout)),
		ParticipantIDs: types.ListValueMust(types.StringType, participants),
	}

	raw, err := rot.RotationConfig.MarshalJSON()
	var cfg apiRotationConfig
	if err == nil {
		err = json.Unmarshal(raw, &cfg)
	}
	if err != nil {
		diags.AddError("Unsupported rotation config", fmt.Sprintf("Rotation %d: %s", rot.Id, err))
		return m, diags
	}
	p := cfg.Payload
	switch {
	case cfg.Type == "daily" && p.StartTime != nil && p.EndTime != nil:
		m.Daily = &dailyRotationModel{StartTime: types.StringPointerValue(p.StartTime), EndTime: types.StringPointerValue(p.EndTime)}
	case cfg.Type == "weekly" && p.Windows != nil:
		w := &weeklyWindowsModel{HandOffDay: optionalWeekdayName(p.HandOffDay)}
		for _, win := range p.Windows {
			w.Windows = append(w.Windows, weeklyWindowModel{
				StartDay: weekdayName(win.StartDay), StartTime: types.StringValue(win.StartTime),
				EndDay: weekdayName(win.EndDay), EndTime: types.StringValue(win.EndTime),
			})
		}
		m.WeeklyWindows = w
	case cfg.Type == "weekly" && p.StartDay != nil && p.EndDay != nil && p.StartTime != nil && p.EndTime != nil:
		m.Weekly = &weeklyRangeModel{
			StartDay: weekdayName(*p.StartDay), StartTime: types.StringPointerValue(p.StartTime),
			EndDay: weekdayName(*p.EndDay), EndTime: types.StringPointerValue(p.EndTime),
			DailyStartTime: types.StringPointerValue(p.DailyStartTime),
			DailyEndTime:   types.StringPointerValue(p.DailyEndTime),
			HandOffDay:     optionalWeekdayName(p.HandOffDay),
		}
	default:
		diags.AddError("Unsupported rotation config", fmt.Sprintf("Rotation %d has a %q config this provider cannot read: %s", rot.Id, cfg.Type, raw))
	}
	return m, diags
}

func teamIDSet(ids []int32) types.Set {
	if len(ids) == 0 {
		return types.SetNull(types.StringType)
	}
	values := make([]attr.Value, len(ids))
	for i, id := range ids {
		values[i] = itoa(int(id))
	}
	return types.SetValueMust(types.StringType, values)
}

func setScheduleState(m *ScheduleResourceModel, s *client.PublicScheduleResponse) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = itoa(int(s.Id))
	m.Name = types.StringValue(s.Name)
	m.TimeZone = types.StringValue(s.Timezone)
	m.TeamIDs = teamIDSet(s.TeamIds)
	m.Rotations = make([]scheduleRotationModel, 0, len(s.Rotations))
	for _, rot := range s.Rotations {
		model, d := rotationFromAPI(rot)
		diags.Append(d...)
		m.Rotations = append(m.Rotations, model)
	}
	return diags
}
