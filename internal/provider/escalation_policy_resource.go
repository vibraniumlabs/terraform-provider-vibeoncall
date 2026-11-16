package provider

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

const millisecondsPerMinute = 60_000

func minutesFromMs[T int | int64](ms T) int64 {
	return int64((ms + millisecondsPerMinute/2) / millisecondsPerMinute)
}

var (
	_ resource.Resource                = &EscalationPolicyResource{}
	_ resource.ResourceWithImportState = &EscalationPolicyResource{}
)

func NewEscalationPolicyResource() resource.Resource {
	return &EscalationPolicyResource{}
}

type EscalationPolicyResource struct {
	client *client.ClientWithResponses
}

type EscalationPolicyResourceModel struct {
	ID          types.String          `tfsdk:"id"`
	Name        types.String          `tfsdk:"name"`
	Description types.String          `tfsdk:"description"`
	TeamID      types.String          `tfsdk:"team_id"`
	Tiers       []escalationTierModel `tfsdk:"tiers"`
}

type escalationTierModel struct {
	AcknowledgementTimeoutMinutes types.Int64           `tfsdk:"acknowledgement_timeout_minutes"`
	ScheduleTargets               []scheduleTargetModel `tfsdk:"schedule_targets"`
	RotationTargets               []rotationTargetModel `tfsdk:"rotation_targets"`
	UserIDs                       []types.String        `tfsdk:"user_ids"`
}

type scheduleTargetModel struct {
	ScheduleID types.String `tfsdk:"schedule_id"`
	Mode       types.String `tfsdk:"mode"`
}

type rotationTargetModel struct {
	ScheduleID types.String `tfsdk:"schedule_id"`
	RotationID types.String `tfsdk:"rotation_id"`
	Mode       types.String `tfsdk:"mode"`
}

func targetModeAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Who to page: the `current`, `next`, or `previous` on-call. Defaults to `current`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(string(client.PublicEscalationPolicyScheduleTargetInputModeCurrent)),
		Validators: []validator.String{stringvalidator.OneOf(
			string(client.PublicEscalationPolicyScheduleTargetInputModeCurrent),
			string(client.PublicEscalationPolicyScheduleTargetInputModeNext),
			string(client.PublicEscalationPolicyScheduleTargetInputModePrevious),
		)},
	}
}

func (r *EscalationPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_escalation_policy"
}

func (r *EscalationPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An escalation policy: who is paged, in what order, and how long each tier waits for an acknowledgement. Set a team's default policy with `vibeoncall_team_default_escalation_policy`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric escalation policy ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Policy name, unique within its team.",
				Required:            true,
				Validators:          []validator.String{utf16LengthAtMost(255), noSurroundingWhitespace},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Policy description. Remove the argument to clear it.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1), utf16LengthAtMost(1000)},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of the owning team. Omit for a global policy. Changing it replaces the policy.",
				Optional:            true,
				Validators:          []validator.String{numericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tiers": schema.ListNestedAttribute{
				MarkdownDescription: "Tiers in escalation order, starting with the first. Each tier needs at least one target.",
				Required:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"acknowledgement_timeout_minutes": schema.Int64Attribute{
							MarkdownDescription: "Minutes to wait for an acknowledgement before escalating to the next tier. A timeout set outside the app that is not a whole number of minutes reads back rounded to the nearest minute, and the next change to `tiers` saves the rounded value.",
							Required:            true,
							Validators:          []validator.Int64{int64validator.AtLeast(1)},
						},
						"schedule_targets": schema.ListNestedAttribute{
							MarkdownDescription: "Schedules whose on-call is paged.",
							Optional:            true,
							Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"schedule_id": schema.StringAttribute{
										MarkdownDescription: "Numeric schedule ID.",
										Required:            true,
										Validators:          []validator.String{numericID},
									},
									"mode": targetModeAttribute(),
								},
							},
						},
						"rotation_targets": schema.ListNestedAttribute{
							MarkdownDescription: "Schedule rotations whose on-call is paged.",
							Optional:            true,
							Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"schedule_id": schema.StringAttribute{
										MarkdownDescription: "Numeric schedule ID.",
										Required:            true,
										Validators:          []validator.String{numericID},
									},
									"rotation_id": schema.StringAttribute{
										MarkdownDescription: "Numeric ID of a rotation in that schedule.",
										Required:            true,
										Validators:          []validator.String{numericID},
									},
									"mode": targetModeAttribute(),
								},
							},
						},
						"user_ids": schema.ListAttribute{
							MarkdownDescription: "Numeric IDs of users to page. For a member of the policy's team, use `vibeoncall_team_membership.<name>.user_id`, so Terraform removes the user from this policy before it removes them from the team. For anyone else, use `data.vibeoncall_member.<name>.id`.",
							ElementType:         types.StringType,
							Optional:            true,
							Validators: []validator.List{
								listvalidator.SizeAtLeast(1),
								listvalidator.ValueStringsAre(numericID),
								listvalidator.AtLeastOneOf(
									path.MatchRelative().AtParent().AtName("schedule_targets"),
									path.MatchRelative().AtParent().AtName("rotation_targets"),
								),
							},
						},
					},
				},
			},
		},
	}
}

func (r *EscalationPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *EscalationPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan EscalationPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.CreateEscalationPolicyWithResponse(ctx, client.CreateEscalationPolicyJSONRequestBody{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		TeamId:      nullableID[int](plan.TeamID),
		Tiers:       tiersToAPI(plan.Tiers),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create escalation policy failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create escalation policy failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setEscalationPolicyState(&plan, res.JSON201)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *EscalationPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state EscalationPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.GetEscalationPolicyWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Read escalation policy failed", err.Error())
		return
	}
	if res.StatusCode() == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Read escalation policy failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setEscalationPolicyState(&state, res.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *EscalationPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state EscalationPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	description := plan.Description.ValueString()
	body := client.UpdateEscalationPolicyJSONRequestBody{
		Name:        plan.Name.ValueStringPointer(),
		Description: &description,
	}
	if !reflect.DeepEqual(plan.Tiers, state.Tiers) {
		tiers := tiersToAPI(plan.Tiers)
		body.Tiers = &tiers
	}
	res, err := r.client.UpdateEscalationPolicyWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Update escalation policy failed", err.Error())
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Update escalation policy failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setEscalationPolicyState(&plan, res.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *EscalationPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state EscalationPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.DeleteEscalationPolicyWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Delete escalation policy failed", err.Error())
		return
	}
	switch res.StatusCode() {
	case http.StatusNoContent, http.StatusNotFound:
	case http.StatusConflict:
		resp.Diagnostics.AddError("Escalation policy is still in use",
			"A routing rule or automation still uses this policy. Point it at another policy, then destroy again.\n\n"+apiError(res.StatusCode(), res.Body))
	default:
		resp.Diagnostics.AddError("Delete escalation policy failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *EscalationPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric escalation policy ID, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func tiersToAPI(tiers []escalationTierModel) []client.PublicEscalationPolicyTierInput {
	out := make([]client.PublicEscalationPolicyTierInput, 0, len(tiers))
	for _, t := range tiers {
		in := client.PublicEscalationPolicyTierInput{
			AcknowledgementTimeThreshold: int(t.AcknowledgementTimeoutMinutes.ValueInt64()) * millisecondsPerMinute,
		}
		if t.ScheduleTargets != nil {
			targets := make([]client.PublicEscalationPolicyScheduleTargetInput, 0, len(t.ScheduleTargets))
			for _, st := range t.ScheduleTargets {
				mode := client.PublicEscalationPolicyScheduleTargetInputMode(st.Mode.ValueString())
				targets = append(targets, client.PublicEscalationPolicyScheduleTargetInput{
					ScheduleId: mustAtoi(st.ScheduleID),
					Mode:       &mode,
				})
			}
			in.ScheduleTargets = &targets
		}
		if t.RotationTargets != nil {
			targets := make([]client.PublicEscalationPolicyRotationTargetInput, 0, len(t.RotationTargets))
			for _, rt := range t.RotationTargets {
				mode := client.PublicEscalationPolicyRotationTargetInputMode(rt.Mode.ValueString())
				targets = append(targets, client.PublicEscalationPolicyRotationTargetInput{
					ScheduleId: mustAtoi(rt.ScheduleID),
					RotationId: mustAtoi(rt.RotationID),
					Mode:       &mode,
				})
			}
			in.RotationTargets = &targets
		}
		if t.UserIDs != nil {
			ids := make([]int, 0, len(t.UserIDs))
			for _, id := range t.UserIDs {
				ids = append(ids, mustAtoi(id))
			}
			in.UserIds = &ids
		}
		out = append(out, in)
	}
	return out
}

func setEscalationPolicyState(m *EscalationPolicyResourceModel, p *client.PublicEscalationPolicyResponse) {
	m.ID = itoa(p.Id)
	m.Name = types.StringValue(p.Name)
	m.Description = types.StringNull()
	if p.Description != "" {
		m.Description = types.StringValue(p.Description)
	}
	m.TeamID = idFromNullable(p.TeamId)

	m.Tiers = make([]escalationTierModel, 0, len(p.Tiers))
	for _, t := range p.Tiers {
		tier := escalationTierModel{
			AcknowledgementTimeoutMinutes: types.Int64Value(minutesFromMs(t.AcknowledgementTimeThreshold)),
		}
		for _, st := range t.ScheduleTargets {
			tier.ScheduleTargets = append(tier.ScheduleTargets, scheduleTargetModel{
				ScheduleID: itoa(st.ScheduleId),
				Mode:       types.StringValue(string(st.Mode)),
			})
		}
		for _, rt := range t.RotationTargets {
			tier.RotationTargets = append(tier.RotationTargets, rotationTargetModel{
				ScheduleID: itoa(rt.ScheduleId),
				RotationID: itoa(rt.RotationId),
				Mode:       types.StringValue(string(rt.Mode)),
			})
		}
		for _, id := range t.UserIds {
			tier.UserIDs = append(tier.UserIDs, itoa(id))
		}
		m.Tiers = append(m.Tiers, tier)
	}
}
