package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var (
	_ resource.Resource                = &AlertSourceResource{}
	_ resource.ResourceWithImportState = &AlertSourceResource{}
)

func NewAlertSourceResource() resource.Resource {
	return &AlertSourceResource{}
}

type AlertSourceResource struct {
	client *client.ClientWithResponses
}

type AlertSourceResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	TeamID         types.String `tfsdk:"team_id"`
	AlertRoutingID types.String `tfsdk:"alert_routing_id"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	WebhookURL     types.String `tfsdk:"webhook_url"`
	WebhookToken   types.String `tfsdk:"webhook_token"`
	EmailAddress   types.String `tfsdk:"email_address"`
}

var alertSourceProviders = []string{
	string(client.CreateAlertSourceRequestProviderAzuremonitor), string(client.CreateAlertSourceRequestProviderCloudamqp),
	string(client.CreateAlertSourceRequestProviderCloudwatch), string(client.CreateAlertSourceRequestProviderCoralogix),
	string(client.CreateAlertSourceRequestProviderDatadog), string(client.CreateAlertSourceRequestProviderEmail),
	string(client.CreateAlertSourceRequestProviderEmbraceio), string(client.CreateAlertSourceRequestProviderGrafana),
	string(client.CreateAlertSourceRequestProviderHttp), string(client.CreateAlertSourceRequestProviderJira),
	string(client.CreateAlertSourceRequestProviderNewrelic),
	string(client.CreateAlertSourceRequestProviderOpsgenie), string(client.CreateAlertSourceRequestProviderPrometheus),
	string(client.CreateAlertSourceRequestProviderSentry), string(client.CreateAlertSourceRequestProviderSns),
}

func (r *AlertSourceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_alert_source"
}

func (r *AlertSourceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	computedSecret := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description,
			Computed:            true,
			Sensitive:           true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An alert source: the webhook URL, or inbound email address, a tool sends alerts to. Each alert that arrives is handed to the source's alert routing.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric alert source ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Source name, unique among sources of the same `type` in the same scope: the same team, or organization level.",
				Required:            true,
				Validators:          []validator.String{utf16LengthAtMost(255), noSurroundingWhitespace},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "The tool that sends to this source, such as `datadog`, `grafana`, `jira` (Jira Cloud), `http` or `email`. Changing it replaces the source, which gives it a new webhook URL.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(alertSourceProviders...)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of the team the source belongs to. Omit for an organization-level source. Changing it replaces the source, which gives it a new webhook URL.",
				Optional:            true,
				Validators:          []validator.String{int32NumericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"alert_routing_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of the alert routing that handles alerts arriving on this source. It must belong to the same team as the source, or be organization-level for an organization-level source.",
				Optional:            true,
				Validators:          []validator.String{int32NumericID},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the source accepts alerts. A disabled source rejects what arrives on it. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"webhook_url":   computedSecret("URL the monitoring tool posts alerts to. Null for an `email` source."),
			"webhook_token": computedSecret("Secret token at the end of `webhook_url`, for tools that take the token separately. Null for an `email` source."),
			"email_address": schema.StringAttribute{
				MarkdownDescription: "Inbound address of an `email` source. Null for every other type.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *AlertSourceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *AlertSourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AlertSourceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := r.client.CreateAlertSourceWithResponse(ctx, client.CreateAlertSourceJSONRequestBody{
		Name:           plan.Name.ValueString(),
		Provider:       client.CreateAlertSourceRequestProvider(plan.Type.ValueString()),
		TeamId:         nullableID[int](plan.TeamID),
		AlertRoutingId: nullableID[int](plan.AlertRoutingID),
		IsEnabled:      plan.Enabled.ValueBoolPointer(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create alert source failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create alert source failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setAlertSourceState(&plan, res.JSON201)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AlertSourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AlertSourceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.GetAlertSourceWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Read alert source failed", err.Error())
		return
	}
	if res.StatusCode() == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Read alert source failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setAlertSourceState(&state, res.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AlertSourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AlertSourceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(plan.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.UpdateAlertSourceWithResponse(ctx, id, client.UpdateAlertSourceJSONRequestBody{
		Name:           plan.Name.ValueStringPointer(),
		AlertRoutingId: nullableID[int](plan.AlertRoutingID),
		IsEnabled:      plan.Enabled.ValueBoolPointer(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update alert source failed", err.Error())
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Update alert source failed", apiError(res.StatusCode(), res.Body))
		return
	}
	setAlertSourceState(&plan, res.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AlertSourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AlertSourceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.DeleteAlertSourceWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Delete alert source failed", err.Error())
		return
	}
	if res.StatusCode() != http.StatusNoContent && res.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Delete alert source failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *AlertSourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric alert source ID, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func setAlertSourceState(m *AlertSourceResourceModel, s *client.AlertSource) {
	m.ID = itoa(s.Id)
	m.Name = types.StringValue(s.Name)
	m.Type = types.StringValue(string(s.Provider))
	m.TeamID = idFromNullable(s.TeamId)
	m.AlertRoutingID = idFromNullable(s.AlertRoutingId)
	m.Enabled = types.BoolValue(s.IsEnabled)
	m.WebhookURL = stringFromNullable(s.WebhookUrl)
	m.WebhookToken = stringFromNullable(s.WebhookToken)
	m.EmailAddress = stringFromNullable(s.EmailAddress)
}
