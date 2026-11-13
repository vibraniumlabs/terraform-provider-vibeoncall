package provider

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
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
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var (
	_ resource.Resource                     = &ServiceResource{}
	_ resource.ResourceWithImportState      = &ServiceResource{}
	_ resource.ResourceWithConfigValidators = &ServiceResource{}
)

func NewServiceResource() resource.Resource {
	return &ServiceResource{}
}

type ServiceResource struct {
	client *client.ClientWithResponses
}

type ServiceResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	TeamID             types.String `tfsdk:"team_id"`
	ExternalProvider   types.String `tfsdk:"external_provider"`
	ExternalServiceID  types.String `tfsdk:"external_service_id"`
	ResponderTeamIDs   types.Set    `tfsdk:"responder_team_ids"`
	ResponderUserIDs   types.Set    `tfsdk:"responder_user_ids"`
	StakeholderUserIDs types.Set    `tfsdk:"stakeholder_user_ids"`
}

type serviceResponderRef = struct {
	TeamId *int32 `json:"teamId,omitempty"`
	UserId *int32 `json:"userId,omitempty"`
}

func idSetAttribute(description string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: description,
		ElementType:         types.StringType,
		Optional:            true,
		Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(int32NumericID)},
	}
}

func (r *ServiceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

func (r *ServiceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A service in the service catalog, with the teams and users who respond to it and the stakeholders kept informed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric service ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Service name, unique within the organization regardless of case.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(255), noSurroundingWhitespace},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Service description. Remove the argument to clear it.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(1000), noSurroundingWhitespace},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of the owning team, which always responds to the service.",
				Optional:            true,
				Validators:          []validator.String{int32NumericID},
			},
			"external_provider": schema.StringAttribute{
				MarkdownDescription: "Provider the service is imported from, such as `backstage`. Set together with `external_service_id`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(32), noSurroundingWhitespace},
			},
			"external_service_id": schema.StringAttribute{
				MarkdownDescription: "The service's identifier in `external_provider`, unique per provider within the organization. Set together with `external_provider`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(128), noSurroundingWhitespace},
			},
			"responder_team_ids":   idSetAttribute("Numeric IDs of teams added as responders. The owning team responds without being listed; list it as well to keep it responding after `team_id` changes."),
			"responder_user_ids":   idSetAttribute("Numeric IDs of users added as responders. For a member of the owning team, use `vibeoncall_team_membership.<name>.user_id`, so Terraform removes the user from this service before it removes them from the team. For anyone else, use `data.vibeoncall_member.<name>.id`."),
			"stakeholder_user_ids": idSetAttribute("Numeric IDs of users added as stakeholders of the service. Stakeholders matched by a condition set in the app are not managed here."),
		},
	}
}

func (r *ServiceResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.RequiredTogether(path.MatchRoot("external_provider"), path.MatchRoot("external_service_id")),
	}
}

func (r *ServiceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *ServiceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ServiceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.CreateServiceWithResponse(ctx, client.CreateServiceJSONRequestBody{
		Name:              plan.Name.ValueString(),
		Description:       nullableString(plan.Description),
		TeamId:            nullableInt32(plan.TeamID),
		ExternalProvider:  nullableString(plan.ExternalProvider),
		ExternalServiceId: nullableString(plan.ExternalServiceID),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create service failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create service failed", apiError(res.StatusCode(), res.Body))
		return
	}
	id := res.JSON201.Service.Id

	var created ServiceResourceModel
	setServiceState(&created, &res.JSON201.Service)
	resp.Diagnostics.Append(resp.State.Set(ctx, &created)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.addMembers(ctx, id, created, plan, &resp.Diagnostics)
	r.saveReadBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *ServiceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ServiceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.GetServiceWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Read service failed", err.Error())
		return
	}
	if res.StatusCode() == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Read service failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setServiceState(&state, &res.JSON200.Service)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ServiceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ServiceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	if r.removeMembers(ctx, id, state, plan, &resp.Diagnostics) && r.updateFields(ctx, id, state, plan, &resp.Diagnostics) {
		r.addMembers(ctx, id, state, plan, &resp.Diagnostics)
	}
	r.saveReadBack(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *ServiceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ServiceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.DeleteServiceWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Delete service failed", err.Error())
		return
	}
	if res.StatusCode() != http.StatusNoContent && res.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Delete service failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *ServiceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric service ID, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *ServiceResource) updateFields(ctx context.Context, id int32, state, plan ServiceResourceModel, diags *diag.Diagnostics) bool {
	if plan.Name.Equal(state.Name) && plan.Description.Equal(state.Description) && plan.TeamID.Equal(state.TeamID) &&
		plan.ExternalProvider.Equal(state.ExternalProvider) && plan.ExternalServiceID.Equal(state.ExternalServiceID) {
		return true
	}
	res, err := r.client.UpdateServiceWithResponse(ctx, id, client.UpdateServiceJSONRequestBody{
		Name:              plan.Name.ValueStringPointer(),
		Description:       nullableString(plan.Description),
		TeamId:            nullableInt32(plan.TeamID),
		ExternalProvider:  nullableString(plan.ExternalProvider),
		ExternalServiceId: nullableString(plan.ExternalServiceID),
	})
	if err != nil {
		diags.AddError("Update service failed", err.Error())
		return false
	}
	if res.JSON200 == nil {
		diags.AddError("Update service failed", apiError(res.StatusCode(), res.Body))
		return false
	}
	return true
}

func (r *ServiceResource) removeMembers(ctx context.Context, id int32, from, to ServiceResourceModel, diags *diag.Diagnostics) bool {
	teams := missingIDs(to.ResponderTeamIDs, from.ResponderTeamIDs)
	users := missingIDs(to.ResponderUserIDs, from.ResponderUserIDs)
	stakeholders := missingIDs(to.StakeholderUserIDs, from.StakeholderUserIDs)

	if refs := responderRefs(teams, users); len(refs) > 0 {
		res, err := r.client.RemoveServiceRespondersWithResponse(ctx, id, client.RemoveServiceRespondersJSONRequestBody{Responders: refs})
		if err != nil {
			diags.AddError("Remove service responders failed", err.Error())
			return false
		}
		if res.JSON200 == nil {
			diags.AddError("Remove service responders failed", apiError(res.StatusCode(), res.Body))
			return false
		}
	}
	if len(stakeholders) > 0 {
		res, err := r.client.RemoveServiceStakeholdersWithResponse(ctx, id, client.RemoveServiceStakeholdersJSONRequestBody{UserIds: stakeholders})
		if err != nil {
			diags.AddError("Remove service stakeholders failed", err.Error())
			return false
		}
		if res.JSON200 == nil {
			diags.AddError("Remove service stakeholders failed", apiError(res.StatusCode(), res.Body))
			return false
		}
	}
	return true
}

func (r *ServiceResource) addMembers(ctx context.Context, id int32, from, to ServiceResourceModel, diags *diag.Diagnostics) {
	teams := missingIDs(from.ResponderTeamIDs, to.ResponderTeamIDs)
	users := missingIDs(from.ResponderUserIDs, to.ResponderUserIDs)
	stakeholders := missingIDs(from.StakeholderUserIDs, to.StakeholderUserIDs)

	if refs := responderRefs(teams, users); len(refs) > 0 {
		res, err := r.client.AddServiceRespondersWithResponse(ctx, id, client.AddServiceRespondersJSONRequestBody{Responders: refs})
		if err != nil {
			diags.AddError("Add service responders failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			diags.AddError("Add service responders failed", apiError(res.StatusCode(), res.Body))
			return
		}
	}
	if len(stakeholders) > 0 {
		res, err := r.client.AddServiceStakeholdersWithResponse(ctx, id, client.AddServiceStakeholdersJSONRequestBody{UserIds: stakeholders})
		if err != nil {
			diags.AddError("Add service stakeholders failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			diags.AddError("Add service stakeholders failed", apiError(res.StatusCode(), res.Body))
		}
	}
}

func (r *ServiceResource) saveReadBack(ctx context.Context, id int32, state *tfsdk.State, diags *diag.Diagnostics) {
	res, err := r.client.GetServiceWithResponse(ctx, id)
	if err != nil {
		diags.AddError("Read service failed", err.Error())
		return
	}
	if res.JSON200 == nil {
		diags.AddError("Read service failed", apiError(res.StatusCode(), res.Body))
		return
	}
	var m ServiceResourceModel
	setServiceState(&m, &res.JSON200.Service)
	diags.Append(state.Set(ctx, &m)...)
}

func setServiceState(m *ServiceResourceModel, s *client.ServiceCatalogPublicSummary) {
	m.ID = itoa(int(s.Id))
	m.Name = types.StringValue(s.Name)
	m.Description = stringFromNullable(s.Description)
	m.TeamID = types.StringNull()
	if teamID, err := s.TeamId.Get(); err == nil {
		m.TeamID = itoa(int(teamID))
	}
	m.ExternalProvider = stringFromNullable(s.ExternalProvider)
	m.ExternalServiceID = stringFromNullable(s.ExternalServiceId)

	var teams, users, stakeholders []int
	for _, responder := range s.Responders {
		switch v, _ := responder.ValueByDiscriminator(); v := v.(type) {
		case client.ServiceTeamResponder:
			teams = append(teams, int(v.Team.Id))
		case client.ServiceUserResponder:
			users = append(users, int(v.User.Id))
		}
	}
	for _, stakeholder := range s.Stakeholders {
		stakeholders = append(stakeholders, int(stakeholder.Id))
	}
	m.ResponderTeamIDs = idSet(teams)
	m.ResponderUserIDs = idSet(users)
	m.StakeholderUserIDs = idSet(stakeholders)
}

func idSet(ids []int) types.Set {
	if len(ids) == 0 {
		return types.SetNull(types.StringType)
	}
	elems := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		elems = append(elems, itoa(id))
	}
	return types.SetValueMust(types.StringType, elems)
}

func idsOf(s types.Set) []int {
	ids := make([]int, 0, len(s.Elements()))
	for _, e := range s.Elements() {
		if v, ok := e.(types.String); ok {
			ids = append(ids, mustAtoi(v))
		}
	}
	return ids
}

func missingIDs(from, to types.Set) []int {
	present := idsOf(from)
	var missing []int
	for _, id := range idsOf(to) {
		if !slices.Contains(present, id) {
			missing = append(missing, id)
		}
	}
	return missing
}

func responderRefs(teamIDs, userIDs []int) []serviceResponderRef {
	refs := make([]serviceResponderRef, 0, len(teamIDs)+len(userIDs))
	for _, id := range teamIDs {
		teamID := int32(id)
		refs = append(refs, serviceResponderRef{TeamId: &teamID})
	}
	for _, id := range userIDs {
		userID := int32(id)
		refs = append(refs, serviceResponderRef{UserId: &userID})
	}
	return refs
}

func nullableInt32(v types.String) nullable.Nullable[int32] {
	if v.IsNull() {
		return nullable.NewNullNullable[int32]()
	}
	return nullable.NewNullableWithValue(int32(mustAtoi(v)))
}
