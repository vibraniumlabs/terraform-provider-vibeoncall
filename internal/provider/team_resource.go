package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

// Matches the characters JavaScript's String.prototype.trim removes, which the API applies to names and descriptions.
var noSurroundingWhitespace = stringvalidator.RegexMatches(
	regexp.MustCompile(`(?s)^[^\s\x0B\p{Z}\x{FEFF}](.*[^\s\x0B\p{Z}\x{FEFF}])?$`),
	"must not be empty or start or end with whitespace",
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
}

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
		},
	}
}

func (r *TeamResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	r.client = c
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
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state TeamResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

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

func nullableString(v types.String) nullable.Nullable[string] {
	if v.IsNull() {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(v.ValueString())
}

func stringFromNullable(v nullable.Nullable[string]) types.String {
	s, err := v.Get()
	if err != nil {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func apiError(status int, body []byte) string {
	return fmt.Sprintf("HTTP %d: %s", status, string(body))
}
