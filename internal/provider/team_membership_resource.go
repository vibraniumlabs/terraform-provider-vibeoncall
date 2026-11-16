package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var (
	_ resource.Resource                = &TeamMembershipResource{}
	_ resource.ResourceWithImportState = &TeamMembershipResource{}
)

func NewTeamMembershipResource() resource.Resource {
	return &TeamMembershipResource{}
}

type TeamMembershipResource struct {
	client *client.ClientWithResponses
}

type TeamMembershipResourceModel struct {
	ID     types.String `tfsdk:"id"`
	TeamID types.String `tfsdk:"team_id"`
	UserID types.String `tfsdk:"user_id"`
	Role   types.String `tfsdk:"role"`
}

func (r *TeamMembershipResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_membership"
}

func (r *TeamMembershipResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A member's place on a team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<team_id>:<user_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric team ID. Changing it moves the member by replacing the membership.",
				Required:            true,
				Validators:          []validator.String{numericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"user_id": schema.StringAttribute{
				MarkdownDescription: "Numeric user ID, usually from the `vibeoncall_member` data source.",
				Required:            true,
				Validators:          []validator.String{numericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": teamRoleAttribute(),
		},
	}
}

func (r *TeamMembershipResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *TeamMembershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TeamMembershipResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.assign(ctx, plan, &resp.Diagnostics) {
		return
	}
	plan.ID = types.StringValue(plan.TeamID.ValueString() + ":" + plan.UserID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamMembershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TeamMembershipResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	members := listTeamMembers(ctx, r.client, state.TeamID.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	userID, ok := int32ID(state.UserID, "user_id", &resp.Diagnostics)
	if !ok {
		return
	}
	if role, ok := members[userID]; ok {
		state.Role = types.StringValue(role)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *TeamMembershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TeamMembershipResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.assign(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamMembershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TeamMembershipResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if userID, ok := int32ID(state.UserID, "user_id", &resp.Diagnostics); ok {
		unassignTeamMember(ctx, r.client, state.TeamID.ValueString(), userID, &resp.Diagnostics)
	}
}

func (r *TeamMembershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	teamID, userID, ok := strings.Cut(req.ID, ":")
	if !ok || !isNumericID(teamID) || !isNumericID(userID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <team_id>:<user_id> with numeric IDs, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), teamID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), userID)...)
}

func (r *TeamMembershipResource) assign(ctx context.Context, m TeamMembershipResourceModel, diags *diag.Diagnostics) bool {
	userID, ok := int32ID(m.UserID, "user_id", diags)
	return ok && assignTeamMember(ctx, r.client, m.TeamID.ValueString(), userID, m.Role.ValueString(), diags)
}
