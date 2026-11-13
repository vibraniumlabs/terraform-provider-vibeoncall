package provider

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var (
	_ resource.Resource                = &TeamResource{}
	_ resource.ResourceWithImportState = &TeamResource{}
)

func NewTeamResource() resource.Resource {
	return &TeamResource{}
}

type TeamResource struct {
	client *client.ClientWithResponses
}

type TeamResourceModel struct {
	ID          types.String `tfsdk:"id"`
	ShortID     types.String `tfsdk:"short_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Members     types.Set    `tfsdk:"members"`
}

type TeamMemberModel struct {
	UserID types.String `tfsdk:"user_id"`
	Role   types.String `tfsdk:"role"`
}

var teamMemberType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"user_id": types.StringType,
	"role":    types.StringType,
}}

func (r *TeamResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (r *TeamResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Vibe OnCall team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric team ID, referenced as `team_id` by schedules, escalation policies, and services. Import accepts this or the short ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"short_id": schema.StringAttribute{
				MarkdownDescription: "Team short ID, e.g. `VP-TEAM-…`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Team name.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtMost(255), noSurroundingWhitespace},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Team description. Remove the argument to clear it.",
				Optional:            true,
				Validators:          []validator.String{noSurroundingWhitespace},
			},
			"members": schema.SetNestedAttribute{
				MarkdownDescription: "Every member of the team. When set, Terraform removes members added elsewhere and adds back members removed elsewhere. When omitted, it shows the team's current members and Terraform leaves them alone, for example to manage them with `vibeoncall_team_membership`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Set{uniqueMemberUserIDs{}},
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"user_id": schema.StringAttribute{
							MarkdownDescription: "Numeric user ID, usually from the `vibeoncall_member` data source.",
							Required:            true,
							Validators:          []validator.String{int32NumericID},
						},
						"role": teamRoleAttribute(),
					},
				},
			},
		},
	}
}

func (r *TeamResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *TeamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TeamResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.CreateTeamWithResponse(ctx, client.CreateTeamJSONRequestBody{
		Name:        plan.Name.ValueString(),
		Description: nullableString(plan.Description),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create team failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create team failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setTeamState(&plan, res.JSON201)
	if membersConfigured(ctx, req.Config, &resp.Diagnostics) {
		want := membersFromSet(ctx, plan.Members, &resp.Diagnostics)
		plan.Members = memberSet(r.reconcileMembers(ctx, plan.ID.ValueString(), map[int32]string{}, want, &resp.Diagnostics))
	} else {
		plan.Members = memberSet(nil)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TeamResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.GetTeamWithResponse(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read team failed", err.Error())
		return
	}
	if res.StatusCode() == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Read team failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setTeamState(&state, res.JSON200)
	members := listTeamMembers(ctx, r.client, state.ID.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Members = memberSet(members)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state TeamResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) {
		res, err := r.client.UpdateTeamWithResponse(ctx, state.ID.ValueString(), client.UpdateTeamJSONRequestBody{
			Name:        plan.Name.ValueStringPointer(),
			Description: nullableString(plan.Description),
		})
		if err != nil {
			resp.Diagnostics.AddError("Update team failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			resp.Diagnostics.AddError("Update team failed", apiError(res.StatusCode(), res.Body))
			return
		}
		setTeamState(&plan, res.JSON200)
	}
	if membersConfigured(ctx, req.Config, &resp.Diagnostics) {
		want := membersFromSet(ctx, plan.Members, &resp.Diagnostics)
		if current := listTeamMembers(ctx, r.client, plan.ID.ValueString(), &resp.Diagnostics); !resp.Diagnostics.HasError() {
			plan.Members = memberSet(r.reconcileMembers(ctx, plan.ID.ValueString(), current, want, &resp.Diagnostics))
		} else {
			plan.Members = state.Members
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TeamResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.DeleteTeamWithResponse(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Delete team failed", err.Error())
		return
	}
	if res.StatusCode() != http.StatusOK && res.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Delete team failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *TeamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func setTeamState(m *TeamResourceModel, t *client.TeamResponse) {
	m.ID = types.StringValue(strconv.Itoa(int(t.Id)))
	m.ShortID = types.StringValue(t.ShortId)
	m.Name = types.StringValue(t.Name)
	m.Description = stringFromNullable(t.Description)
}

// Assigns before unassigning so a failed apply leaves extra members rather than missing ones.
func (r *TeamResource) reconcileMembers(ctx context.Context, teamID string, current, want map[int32]string, diags *diag.Diagnostics) map[int32]string {
	actual := maps.Clone(current)
	for _, id := range slices.Sorted(maps.Keys(want)) {
		if actual[id] == want[id] {
			continue
		}
		if !assignTeamMember(ctx, r.client, teamID, id, want[id], diags) {
			return actual
		}
		actual[id] = want[id]
	}
	for _, id := range slices.Sorted(maps.Keys(current)) {
		if _, ok := want[id]; ok {
			continue
		}
		if !unassignTeamMember(ctx, r.client, teamID, id, diags) {
			return actual
		}
		delete(actual, id)
	}
	return actual
}

func membersConfigured(ctx context.Context, config tfsdk.Config, diags *diag.Diagnostics) bool {
	var members types.Set
	diags.Append(config.GetAttribute(ctx, path.Root("members"), &members)...)
	return !members.IsNull()
}

func membersFromSet(ctx context.Context, set types.Set, diags *diag.Diagnostics) map[int32]string {
	var models []TeamMemberModel
	diags.Append(set.ElementsAs(ctx, &models, false)...)
	members := make(map[int32]string, len(models))
	for _, m := range models {
		if id, ok := int32ID(m.UserID, "user_id", diags); ok {
			members[id] = m.Role.ValueString()
		}
	}
	return members
}

func memberSet(members map[int32]string) types.Set {
	elems := make([]attr.Value, 0, len(members))
	for _, id := range slices.Sorted(maps.Keys(members)) {
		elems = append(elems, types.ObjectValueMust(teamMemberType.AttrTypes, map[string]attr.Value{
			"user_id": types.StringValue(strconv.Itoa(int(id))),
			"role":    types.StringValue(members[id]),
		}))
	}
	return types.SetValueMust(teamMemberType, elems)
}

type uniqueMemberUserIDs struct{}

func (uniqueMemberUserIDs) Description(context.Context) string {
	return "each user_id must appear at most once"
}

func (v uniqueMemberUserIDs) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v uniqueMemberUserIDs) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var models []TeamMemberModel
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &models, true)...)
	seen := map[string]bool{}
	for _, m := range models {
		if m.UserID.IsNull() || m.UserID.IsUnknown() {
			continue
		}
		if id := m.UserID.ValueString(); seen[id] {
			resp.Diagnostics.AddAttributeError(req.Path, "Duplicate team member", fmt.Sprintf("user_id %q is listed more than once.", id))
		} else {
			seen[id] = true
		}
	}
}

func teamRoleAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Team role: `member` or `admin`. Defaults to `member`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(string(client.AssignTeamMemberRequestRoleMember)),
		Validators: []validator.String{stringvalidator.OneOf(
			string(client.AssignTeamMemberRequestRoleMember),
			string(client.AssignTeamMemberRequestRoleAdmin),
		)},
	}
}

func listTeamMembers(ctx context.Context, c *client.ClientWithResponses, teamID string, diags *diag.Diagnostics) map[int32]string {
	res, err := c.ListTeamMembersWithResponse(ctx, teamID, nil)
	if err != nil {
		diags.AddError("List team members failed", err.Error())
		return nil
	}
	if res.StatusCode() == http.StatusNotFound {
		return map[int32]string{}
	}
	if res.JSON200 == nil {
		diags.AddError("List team members failed", apiError(res.StatusCode(), res.Body))
		return nil
	}
	members := make(map[int32]string, len(res.JSON200.Users))
	for _, u := range res.JSON200.Users {
		members[u.Id] = string(u.Role)
	}
	return members
}

func assignTeamMember(ctx context.Context, c *client.ClientWithResponses, teamID string, userID int32, role string, diags *diag.Diagnostics) bool {
	r := client.AssignTeamMemberRequestRole(role)
	res, err := c.AssignTeamMemberWithResponse(ctx, teamID, client.AssignTeamMemberJSONRequestBody{UserId: userID, Role: &r})
	if err != nil {
		diags.AddError("Assign team member failed", fmt.Sprintf("user %d: %s", userID, err))
		return false
	}
	if res.JSON200 == nil {
		diags.AddError("Assign team member failed", fmt.Sprintf("user %d: %s", userID, apiError(res.StatusCode(), res.Body)))
		return false
	}
	return true
}

func unassignTeamMember(ctx context.Context, c *client.ClientWithResponses, teamID string, userID int32, diags *diag.Diagnostics) bool {
	res, err := c.UnassignTeamMemberWithResponse(ctx, teamID, userID)
	if err != nil {
		diags.AddError("Unassign team member failed", fmt.Sprintf("user %d: %s", userID, err))
		return false
	}
	if res.StatusCode() != http.StatusOK && res.StatusCode() != http.StatusNotFound {
		diags.AddError("Unassign team member failed", fmt.Sprintf("user %d: %s", userID, apiError(res.StatusCode(), res.Body)))
		return false
	}
	return true
}
