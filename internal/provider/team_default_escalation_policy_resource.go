package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

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
	_ resource.Resource                = &TeamDefaultEscalationPolicyResource{}
	_ resource.ResourceWithImportState = &TeamDefaultEscalationPolicyResource{}
)

func NewTeamDefaultEscalationPolicyResource() resource.Resource {
	return &TeamDefaultEscalationPolicyResource{}
}

type TeamDefaultEscalationPolicyResource struct {
	client *client.ClientWithResponses
}

type TeamDefaultEscalationPolicyResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	TeamID             types.String `tfsdk:"team_id"`
	EscalationPolicyID types.String `tfsdk:"escalation_policy_id"`
}

func (r *TeamDefaultEscalationPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_default_escalation_policy"
}

func (r *TeamDefaultEscalationPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A team's default escalation policy, paged when the team is paged without a policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `team_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric team ID.",
				Required:            true,
				Validators:          []validator.String{numericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"escalation_policy_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of an escalation policy that belongs to the team.",
				Required:            true,
				Validators:          []validator.String{numericID},
			},
		},
	}
}

func (r *TeamDefaultEscalationPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *TeamDefaultEscalationPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TeamDefaultEscalationPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.setDefault(ctx, plan, &resp.Diagnostics) {
		return
	}
	plan.ID = plan.TeamID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamDefaultEscalationPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TeamDefaultEscalationPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	team, ok := int32ID(state.TeamID, "team_id", &resp.Diagnostics)
	if !ok {
		return
	}

	limit, offset := int32(listPageSize), int32(0)
	params := &client.ListEscalationPoliciesParams{TeamId: &team, Limit: &limit, Offset: &offset}
	for {
		res, err := r.client.ListEscalationPoliciesWithResponse(ctx, params)
		if err != nil {
			resp.Diagnostics.AddError("Read team default escalation policy failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			resp.Diagnostics.AddError("Read team default escalation policy failed", apiError(res.StatusCode(), res.Body))
			return
		}
		for _, p := range res.JSON200.Policies {
			if p.IsDefault {
				state.EscalationPolicyID = itoa(p.Id)
				resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
				return
			}
		}
		if !res.JSON200.HasMore {
			break
		}
		offset += int32(len(res.JSON200.Policies))
	}
	resp.State.RemoveResource(ctx)
}

func (r *TeamDefaultEscalationPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TeamDefaultEscalationPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.setDefault(ctx, plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamDefaultEscalationPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TeamDefaultEscalationPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.EscalationPolicyID, "escalation_policy_id", &resp.Diagnostics)
	if !ok {
		return
	}

	isDefault := false
	res, err := r.client.UpdateEscalationPolicyWithResponse(ctx, id, client.UpdateEscalationPolicyJSONRequestBody{IsDefault: &isDefault})
	if err != nil {
		resp.Diagnostics.AddError("Clear team default escalation policy failed", err.Error())
		return
	}
	if res.StatusCode() != http.StatusOK && res.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Clear team default escalation policy failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *TeamDefaultEscalationPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric team ID, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), req.ID)...)
}

func (r *TeamDefaultEscalationPolicyResource) setDefault(ctx context.Context, m TeamDefaultEscalationPolicyResourceModel, diags *diag.Diagnostics) bool {
	id, ok := int32ID(m.EscalationPolicyID, "escalation_policy_id", diags)
	if !ok {
		return false
	}

	got, err := r.client.GetEscalationPolicyWithResponse(ctx, id)
	if err != nil {
		diags.AddError("Read escalation policy failed", err.Error())
		return false
	}
	if got.JSON200 == nil {
		diags.AddError("Read escalation policy failed", apiError(got.StatusCode(), got.Body))
		return false
	}
	policyTeamID, err := got.JSON200.TeamId.Get()
	if err != nil {
		diags.AddAttributeError(path.Root("escalation_policy_id"), "Escalation policy is global",
			fmt.Sprintf("Escalation policy %d has no team. Only a team's own policy can be its default.", id))
		return false
	}
	if strconv.Itoa(policyTeamID) != m.TeamID.ValueString() {
		diags.AddAttributeError(path.Root("escalation_policy_id"), "Escalation policy belongs to another team",
			fmt.Sprintf("Escalation policy %d belongs to team %d, not team %s.", id, policyTeamID, m.TeamID.ValueString()))
		return false
	}

	isDefault := true
	res, err := r.client.UpdateEscalationPolicyWithResponse(ctx, id, client.UpdateEscalationPolicyJSONRequestBody{IsDefault: &isDefault})
	if err != nil {
		diags.AddError("Set team default escalation policy failed", err.Error())
		return false
	}
	if res.JSON200 == nil {
		diags.AddError("Set team default escalation policy failed", apiError(res.StatusCode(), res.Body))
		return false
	}
	return true
}
