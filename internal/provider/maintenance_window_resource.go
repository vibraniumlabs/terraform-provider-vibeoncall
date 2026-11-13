package provider

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var (
	_ resource.Resource                = &MaintenanceWindowResource{}
	_ resource.ResourceWithImportState = &MaintenanceWindowResource{}
	_ resource.ResourceWithModifyPlan  = &MaintenanceWindowResource{}
)

func NewMaintenanceWindowResource() resource.Resource {
	return &MaintenanceWindowResource{}
}

type MaintenanceWindowResource struct {
	client *client.ClientWithResponses
}

type MaintenanceWindowResourceModel struct {
	ID                   types.String          `tfsdk:"id"`
	Name                 types.String          `tfsdk:"name"`
	Description          types.String          `tfsdk:"description"`
	TeamID               types.String          `tfsdk:"team_id"`
	StartAt              types.String          `tfsdk:"start_at"`
	EndAt                types.String          `tfsdk:"end_at"`
	AnnouncementChannels []types.String        `tfsdk:"announcement_channels"`
	ConditionGroups      []conditionGroupModel `tfsdk:"condition_groups"`
}

func conditionAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"severity": schema.SingleNestedAttribute{
			MarkdownDescription: "Matches the alert severity. `SEV0` is the most severe and `SIGNAL` the least.",
			Optional:            true,
			Validators: []validator.Object{objectvalidator.ExactlyOneOf(
				path.MatchRelative().AtParent().AtName("field"),
				path.MatchRelative().AtParent().AtName("provider"),
				path.MatchRelative().AtParent().AtName("time"),
			)},
			Attributes: map[string]schema.Attribute{
				"operator": schema.StringAttribute{
					MarkdownDescription: "One of `equal_to`, `less_than`, `greater_than`, `less_than_or_equal`, `greater_than_or_equal`. `greater_than` means more severe, so `less_than_or_equal` with `SEV3` matches SEV3, SEV4, SEV5 and SIGNAL.",
					Required:            true,
					Validators: []validator.String{stringvalidator.OneOf(
						string(client.EqualTo), string(client.LessThan), string(client.GreaterThan),
						string(client.LessThanOrEqual), string(client.GreaterThanOrEqual),
					)},
				},
				"severity": schema.StringAttribute{
					MarkdownDescription: "One of `SEV0` to `SEV5`, or `SIGNAL`.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.OneOf("SEV0", "SEV1", "SEV2", "SEV3", "SEV4", "SEV5", "SIGNAL")},
				},
			},
		},
		"field": schema.SingleNestedAttribute{
			MarkdownDescription: "Matches a field of the incoming alert payload.",
			Optional:            true,
			Attributes: map[string]schema.Attribute{
				"field_path": schema.StringAttribute{
					MarkdownDescription: "Dot-path into the incoming payload. The alert source's own fields are under `details`, for example `details.env`. A path that matches nothing never matches.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				},
				"operator": schema.StringAttribute{
					MarkdownDescription: "One of `contains`, `exact_match`, `not_contains`, `matches_regex`.",
					Required:            true,
					Validators: []validator.String{stringvalidator.OneOf(
						string(client.Contains), string(client.ExactMatch), string(client.NotContains), string(client.MatchesRegex),
					)},
				},
				"value": schema.StringAttribute{
					MarkdownDescription: "Value to compare against, or the regular expression for `matches_regex`.",
					Required:            true,
				},
				"case_sensitive": schema.BoolAttribute{
					MarkdownDescription: "Whether the comparison is case-sensitive.",
					Optional:            true,
				},
			},
		},
		"provider": schema.SingleNestedAttribute{
			MarkdownDescription: "Matches alerts from any of the given alert sources.",
			Optional:            true,
			Attributes: map[string]schema.Attribute{
				"providers": schema.SetAttribute{
					MarkdownDescription: "Alert source types, such as `datadog`, `grafana` or `opsgenie`.",
					ElementType:         types.StringType,
					Required:            true,
					Validators: []validator.Set{
						setvalidator.SizeAtLeast(1),
						setvalidator.ValueStringsAre(stringvalidator.OneOf(
							string(client.Opsgenie), string(client.Grafana), string(client.Datadog), string(client.Coralogix),
							string(client.Newrelic), string(client.Prometheus), string(client.Cloudwatch), string(client.Sns),
							string(client.Heartbeat), string(client.Email), string(client.Sentry), string(client.Embraceio),
							string(client.Http), string(client.Azuremonitor), string(client.Cloudamqp),
						)),
					},
				},
			},
		},
		"time": schema.SingleNestedAttribute{
			MarkdownDescription: "Matches alerts that arrive within a recurring daily or weekly interval.",
			Optional:            true,
			Attributes: map[string]schema.Attribute{
				"timezone": schema.StringAttribute{
					MarkdownDescription: "IANA time zone the interval is in, such as `Asia/Taipei`.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				},
				"daily": schema.SingleNestedAttribute{
					MarkdownDescription: "Every day between two times. An interval whose end is before its start crosses midnight.",
					Optional:            true,
					Validators: []validator.Object{objectvalidator.ExactlyOneOf(
						path.MatchRelative().AtParent().AtName("weekly"),
					)},
					Attributes: map[string]schema.Attribute{
						"start_time": timeAttribute("Start time.", true),
						"end_time":   timeAttribute("End time.", true),
					},
				},
				"weekly": schema.ListNestedAttribute{
					MarkdownDescription: "Weekly ranges. The condition matches inside any of them.",
					Optional:            true,
					Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"start_day":  weekdayAttribute("Day the range starts.", true),
							"end_day":    weekdayAttribute("Day the range ends.", true),
							"start_time": timeAttribute("Start time on `start_day`.", true),
							"end_time":   timeAttribute("End time on `end_day`.", true),
						},
					},
				},
			},
		},
	}
}

func (r *MaintenanceWindowResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_maintenance_window"
}

func (r *MaintenanceWindowResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A maintenance window: a planned period during which matching alerts do not page anyone. Alerts are still ingested and created. Suited to planned, short-lived windows.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric maintenance window ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Window name.",
				Required:            true,
				Validators:          []validator.String{utf16LengthAtMost(64), noSurroundingWhitespace},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Window description. Remove the argument to clear it.",
				Optional:            true,
				Validators:          []validator.String{utf16LengthAtMost(512), noSurroundingWhitespace},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of the team whose alerts the window covers. Omit for a global window that covers every team. Changing it replaces the window, which fails once `start_at` has passed.",
				Optional:            true,
				Validators:          []validator.String{numericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"start_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 start time, such as `2026-11-20T09:00:00+08:00`. A new window cannot start in the past, and a window that has started cannot change it.",
				Validators:          []validator.String{rfc3339Timestamp{}},
				Required:            true,
			},
			"end_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 end time, after `start_at`. To end an active window early, set it to a time a few minutes ahead. A window that has ended cannot be changed.",
				Validators:          []validator.String{rfc3339Timestamp{}},
				Required:            true,
			},
			"announcement_channels": schema.SetAttribute{
				MarkdownDescription: "IDs of the Slack or Microsoft Teams channels to announce the window's start and end in, from `data.vibeoncall_chat_channel.<name>.id`.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
			},
			"condition_groups": schema.ListNestedAttribute{
				MarkdownDescription: "Limits which alerts the window covers. An alert is covered when it matches any group. Omit to cover every alert of the team, or of the organization for a global window.",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"operator": schema.StringAttribute{
							MarkdownDescription: "`AND` to require every condition in the group, `OR` to require any.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(logicalAND, logicalOR)},
						},
						"conditions": schema.ListNestedAttribute{
							MarkdownDescription: "Conditions in the group. Each sets exactly one of `severity`, `field`, `provider` or `time`.",
							Required:            true,
							Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
							NestedObject:        schema.NestedAttributeObject{Attributes: conditionAttributes()},
						},
					},
				},
			},
		},
	}
}

func (r *MaintenanceWindowResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *MaintenanceWindowResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var planTeam, stateTeam types.String
	var startAt types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("team_id"), &planTeam)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("team_id"), &stateTeam)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("start_at"), &startAt)...)
	if resp.Diagnostics.HasError() || planTeam.Equal(stateTeam) || startAt.IsUnknown() {
		return
	}
	if !parseTime(startAt).After(time.Now()) {
		resp.Diagnostics.AddAttributeError(path.Root("team_id"), "Cannot move a started maintenance window to another team",
			"Changing team_id replaces the window, and a new window cannot start in the past. "+
				"Destroying it first would end the maintenance early. Keep team_id, or also move start_at to a time that has not passed.")
	}
}

func (r *MaintenanceWindowResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MaintenanceWindowResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	conditions, operator, err := conditionGroupsToAPI(plan.ConditionGroups)
	if err != nil {
		resp.Diagnostics.AddError("Invalid condition_groups", err.Error())
	}
	if resp.Diagnostics.HasError() {
		return
	}

	teamID := nullable.NewNullNullable[int32]()
	if !plan.TeamID.IsNull() {
		id, ok := int32ID(plan.TeamID, "team_id", &resp.Diagnostics)
		if !ok {
			return
		}
		teamID = nullable.NewNullableWithValue(id)
	}
	body := client.CreateMaintenanceWindowJSONRequestBody{
		Name:                      plan.Name.ValueString(),
		Description:               nullableString(plan.Description),
		TeamId:                    teamID,
		StartAt:                   parseTime(plan.StartAt),
		EndAt:                     parseTime(plan.EndAt),
		AnnouncementChannels:      announcementChannelsToAPI(plan.AnnouncementChannels),
		Conditions:                nullableConditions(conditions),
		ConditionsLogicalOperator: nullableOperator[client.CreateMaintenanceWindowRequestConditionsLogicalOperator](operator),
	}
	res, err := r.client.CreateMaintenanceWindowWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Create maintenance window failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create maintenance window failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setMaintenanceWindowState(&plan, &res.JSON201.MaintenanceWindow, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MaintenanceWindowResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MaintenanceWindowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.GetMaintenanceWindowWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Read maintenance window failed", err.Error())
		return
	}
	if res.StatusCode() == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Read maintenance window failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setMaintenanceWindowState(&state, &res.JSON200.MaintenanceWindow, state.Name.IsNull())...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MaintenanceWindowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state MaintenanceWindowResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if differsOnlyInTimeFormat(plan, state) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}
	conditions, operator, err := conditionGroupsToAPI(plan.ConditionGroups)
	if err != nil {
		resp.Diagnostics.AddError("Invalid condition_groups", err.Error())
		return
	}

	body := client.UpdateMaintenanceWindowJSONRequestBody{
		Name:                      plan.Name.ValueStringPointer(),
		Description:               nullableString(plan.Description),
		AnnouncementChannels:      announcementChannelsToAPI(plan.AnnouncementChannels),
		Conditions:                nullableConditions(conditions),
		ConditionsLogicalOperator: nullableOperator[client.UpdateMaintenanceWindowRequestConditionsLogicalOperator](operator),
	}
	body.StartAt = changedTime(plan.StartAt, state.StartAt)
	body.EndAt = changedTime(plan.EndAt, state.EndAt)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.UpdateMaintenanceWindowWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Update maintenance window failed", err.Error())
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Update maintenance window failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setMaintenanceWindowState(&plan, &res.JSON200.MaintenanceWindow, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MaintenanceWindowResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MaintenanceWindowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.DeleteMaintenanceWindowWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Delete maintenance window failed", err.Error())
		return
	}
	switch res.StatusCode() {
	case http.StatusNoContent, http.StatusNotFound:
	default:
		resp.Diagnostics.AddError("Delete maintenance window failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *MaintenanceWindowResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric maintenance window ID, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

type rfc3339Timestamp struct{}

func (rfc3339Timestamp) Description(context.Context) string {
	return "must be an RFC 3339 timestamp"
}

func (v rfc3339Timestamp) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (rfc3339Timestamp) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid RFC 3339 timestamp", err.Error())
	}
}

func parseTime(v types.String) time.Time {
	t, _ := time.Parse(time.RFC3339, v.ValueString())
	return t
}

func changedTime(plan, state types.String) *time.Time {
	if sameInstant(plan, state) {
		return nil
	}
	t := parseTime(plan)
	return &t
}

func keepIfSameInstant(prior types.String, t time.Time) types.String {
	if !prior.IsNull() && parseTime(prior).Equal(t) {
		return prior
	}
	return types.StringValue(t.UTC().Format(time.RFC3339Nano))
}

func sameInstant(a, b types.String) bool {
	return parseTime(a).Equal(parseTime(b))
}

func differsOnlyInTimeFormat(plan, state MaintenanceWindowResourceModel) bool {
	if !sameInstant(plan.StartAt, state.StartAt) || !sameInstant(plan.EndAt, state.EndAt) {
		return false
	}
	plan.StartAt, plan.EndAt = state.StartAt, state.EndAt
	return reflect.DeepEqual(plan, state)
}

func announcementChannelsToAPI(ids []types.String) nullable.Nullable[[]string] {
	if ids == nil {
		return nullable.NewNullNullable[[]string]()
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.ValueString())
	}
	return nullable.NewNullableWithValue(out)
}

func nullableConditions(conditions []client.MaintenanceWindowCondition) nullable.Nullable[[]client.MaintenanceWindowCondition] {
	if conditions == nil {
		return nullable.NewNullNullable[[]client.MaintenanceWindowCondition]()
	}
	return nullable.NewNullableWithValue(conditions)
}

func nullableOperator[T ~string](operator string) nullable.Nullable[T] {
	if operator == "" {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(T(operator))
}

func setMaintenanceWindowState(m *MaintenanceWindowResourceModel, w *client.MaintenanceWindow, importing bool) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = itoa(int(w.Id))
	m.Name = types.StringValue(w.Name)
	m.Description = stringFromNullable(w.Description)
	if m.Description.ValueString() == "" {
		m.Description = types.StringNull()
	}
	m.TeamID = types.StringNull()
	if teamID, err := w.TeamId.Get(); err == nil {
		m.TeamID = itoa(int(teamID))
	}
	m.StartAt = keepIfSameInstant(m.StartAt, w.StartAt)
	m.EndAt = keepIfSameInstant(m.EndAt, w.EndAt)

	m.AnnouncementChannels = nil
	for _, c := range w.AnnouncementChannels {
		m.AnnouncementChannels = append(m.AnnouncementChannels, types.StringValue(c.Id))
	}

	conditions, _ := w.Conditions.Get()
	operator, _ := w.ConditionsLogicalOperator.Get()
	groups, err := conditionGroupsFromAPI(conditions, string(operator))
	if err != nil && importing {
		diags.AddError("Unsupported maintenance window conditions", fmt.Sprintf("Maintenance window %d: %s.", w.Id, err))
		return diags
	}
	if err != nil {
		diags.AddWarning("Maintenance window conditions changed outside Terraform",
			fmt.Sprintf("Maintenance window %d: %s. The next apply replaces them with condition_groups from the configuration.", w.Id, err))
		groups = []conditionGroupModel{}
	}
	m.ConditionGroups = groups
	return diags
}
