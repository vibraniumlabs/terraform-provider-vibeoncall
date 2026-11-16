package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = &AlertRouteResource{}
	_ resource.ResourceWithImportState = &AlertRouteResource{}
)

func NewAlertRouteResource() resource.Resource {
	return &AlertRouteResource{}
}

type AlertRouteResource struct {
	client *client.ClientWithResponses
}

type AlertRouteResourceModel struct {
	ID          types.String          `tfsdk:"id"`
	Name        types.String          `tfsdk:"name"`
	Description types.String          `tfsdk:"description"`
	TeamID      types.String          `tfsdk:"team_id"`
	Enabled     types.Bool            `tfsdk:"enabled"`
	Rules       []alertRouteRuleModel `tfsdk:"rules"`
}

type alertRouteRuleModel struct {
	Name            types.String                               `tfsdk:"name"`
	ConditionGroups []conditionGroupModel[routeConditionModel] `tfsdk:"condition_groups"`
	Escalate        *escalateActionModel                       `tfsdk:"escalate"`
	DoNothing       types.Bool                                 `tfsdk:"do_nothing"`
}

type escalateActionModel struct {
	EscalationPolicyIDs []types.String `tfsdk:"escalation_policy_ids"`
	ScheduleIDs         []types.String `tfsdk:"schedule_ids"`
	UserIDs             []types.String `tfsdk:"user_ids"`
	Urgency             types.String   `tfsdk:"urgency"`
	DelaySeconds        types.Int64    `tfsdk:"delay_seconds"`
}

type routeConditionModel struct {
	conditionModel
	Count *countConditionModel `tfsdk:"count"`
}

type countConditionModel struct {
	Threshold        types.Int64 `tfsdk:"threshold"`
	TimeframeSeconds types.Int64 `tfsdk:"timeframe_seconds"`
}

type routeRuleJSON struct {
	Name            *string           `json:"name"`
	LogicalOperator string            `json:"logicalOperator"`
	Conditions      []json.RawMessage `json:"conditions"`
	Action          routeActionJSON   `json:"action"`
}

type routeActionJSON struct {
	Type  string             `json:"type"`
	Value *escalateValueJSON `json:"value,omitempty"`
}

type escalateValueJSON struct {
	Escalation          []escalationTargetJSON `json:"escalation"`
	NotificationUrgency *string                `json:"notificationUrgency,omitempty"`
	DelayMs             *int64                 `json:"delayMs,omitempty"`
}

type escalationTargetJSON struct {
	Type  string `json:"type"`
	Value int    `json:"value"`
}

type countRuleJSON struct {
	CountThreshold int64 `json:"countThreshold"`
	Timeframe      int64 `json:"timeframe"`
}

const (
	actionEscalate  = "ESCALATE"
	actionDoNothing = "DO_NOTHING"

	targetEscalationPolicy = "escalationPolicy"
	targetSchedule         = "schedule"
	targetUser             = "user"
)

func (r *AlertRouteResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_alert_route"
}

func routeConditionAttributes() map[string]schema.SingleNestedAttribute {
	attrs := conditionAttributes()
	attrs["count"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Matches once `threshold` alerts arrived within `timeframe_seconds`.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"threshold": schema.Int64Attribute{
				MarkdownDescription: "Number of alerts, at least 2.",
				Required:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(2)},
			},
			"timeframe_seconds": schema.Int64Attribute{
				MarkdownDescription: "Window the alerts must arrive within, in seconds, at least 60.",
				Required:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(60)},
			},
		},
	}
	return attrs
}

var escalationTargetAttributes = []string{"escalation_policy_ids", "schedule_ids", "user_ids"}

func targetIDsAttribute(description string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: description,
		ElementType:         types.StringType,
		Optional:            true,
		Validators: []validator.Set{
			setvalidator.SizeAtLeast(1),
			setvalidator.ValueStringsAre(int32NumericID),
		},
	}
}

func (r *AlertRouteResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An alert routing: ordered rules that decide what happens to each alert an alert source receives. The first rule whose conditions match pages its targets, or does nothing.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric alert routing ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Routing name, unique in its scope: the same team, or organization level.",
				Required:            true,
				Validators:          []validator.String{utf16LengthAtMost(255), noSurroundingWhitespace},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Routing description. Remove the argument to clear it.",
				Optional:            true,
				Validators:          []validator.String{utf16LengthAtMost(5000), noSurroundingWhitespace},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of the team the routing belongs to. Omit for an organization-level routing. Changing it replaces the routing.",
				Optional:            true,
				Validators:          []validator.String{int32NumericID},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the routing evaluates its rules. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "Rules in evaluation order. The first rule whose conditions match decides what happens to the alert. Omit to have the routing page no one.",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1), listvalidator.NoNullValues(), fallbackRuleIsLast{}},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							MarkdownDescription: "Rule name.",
							Optional:            true,
							Validators:          []validator.String{stringvalidator.LengthAtLeast(1), utf16LengthAtMost(255)},
						},
						"condition_groups": conditionGroupsAttribute("Which alerts the rule matches. An alert matches when it matches any group. Omit on the last rule to match every alert that no earlier rule matched.", routeConditionAttributes()),
						"escalate": schema.SingleNestedAttribute{
							MarkdownDescription: "Page the targets. Set at least one of `escalation_policy_ids`, `schedule_ids` or `user_ids`.",
							Optional:            true,
							Validators: []validator.Object{
								objectvalidator.ExactlyOneOf(path.MatchRelative().AtParent().AtName("do_nothing")),
								escalateHasTarget{},
							},
							Attributes: map[string]schema.Attribute{
								"escalation_policy_ids": targetIDsAttribute("Numeric IDs of escalation policies to page."),
								"schedule_ids":          targetIDsAttribute("Numeric IDs of schedules whose on-call members to page."),
								"user_ids":              targetIDsAttribute("Numeric IDs of members to page."),
								"urgency": schema.StringAttribute{
									MarkdownDescription: "`HIGH`, `MEDIUM` or `LOW`. Overrides the urgency the targets are paged with.",
									Optional:            true,
									Validators:          []validator.String{stringvalidator.OneOf("HIGH", "MEDIUM", "LOW")},
								},
								"delay_seconds": schema.Int64Attribute{
									MarkdownDescription: "Seconds to wait before paging, from 60 to 86400.",
									Optional:            true,
									Validators:          []validator.Int64{int64validator.Between(60, 86400)},
								},
							},
						},
						"do_nothing": schema.BoolAttribute{
							MarkdownDescription: "Set to `true` to create the alert without paging anyone.",
							Optional:            true,
							Validators:          []validator.Bool{mustBeTrue{}},
						},
					},
				},
			},
		},
	}
}

type mustBeTrue struct{}

func (mustBeTrue) Description(context.Context) string { return "must be true when set" }

func (v mustBeTrue) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v mustBeTrue) ValidateBool(ctx context.Context, req validator.BoolRequest, resp *validator.BoolResponse) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && !req.ConfigValue.ValueBool() {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value", "do_nothing must be true when set. Use escalate to page someone instead.")
	}
}

type escalateHasTarget struct{}

func (escalateHasTarget) Description(context.Context) string {
	return "must set at least one of escalation_policy_ids, schedule_ids or user_ids"
}

func (v escalateHasTarget) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v escalateHasTarget) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	attrs := req.ConfigValue.Attributes()
	for _, name := range escalationTargetAttributes {
		if !attrs[name].IsNull() {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Missing escalation target", "escalate "+v.Description(ctx)+".")
}

type fallbackRuleIsLast struct{}

func (fallbackRuleIsLast) Description(context.Context) string {
	return "only the last rule may omit condition_groups"
}

func (v fallbackRuleIsLast) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v fallbackRuleIsLast) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	rules := req.ConfigValue.Elements()
	for i, rule := range rules[:max(len(rules)-1, 0)] {
		obj, ok := rule.(types.Object)
		if ok && !obj.IsNull() && !obj.IsUnknown() && obj.Attributes()["condition_groups"].IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path.AtListIndex(i), "Fallback rule is not last",
				"A rule without condition_groups matches every alert, so the rules after it would never run. Only the last rule may omit condition_groups.")
		}
	}
}

func (r *AlertRouteResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *AlertRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AlertRouteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rules, err := alertRouteRulesToAPI(plan.Rules)
	if err != nil {
		resp.Diagnostics.AddError("Invalid rules", err.Error())
		return
	}

	res, err := r.client.CreateAlertRoutingWithResponse(ctx, client.CreateAlertRoutingJSONRequestBody{
		Name:        plan.Name.ValueString(),
		Description: nullableString(plan.Description),
		TeamId:      nullableID[int](plan.TeamID),
		IsEnabled:   plan.Enabled.ValueBoolPointer(),
		Rules:       &rules,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create alert routing failed", err.Error())
		return
	}
	if res.JSON201 == nil {
		resp.Diagnostics.AddError("Create alert routing failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setAlertRouteState(&plan, res.JSON201, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AlertRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AlertRouteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.GetAlertRoutingWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Read alert routing failed", err.Error())
		return
	}
	if res.StatusCode() == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Read alert routing failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setAlertRouteState(&state, res.JSON200, state.Name.IsNull())...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AlertRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AlertRouteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(plan.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}
	rules, err := alertRouteRulesToAPI(plan.Rules)
	if err != nil {
		resp.Diagnostics.AddError("Invalid rules", err.Error())
		return
	}

	res, err := r.client.UpdateAlertRoutingWithResponse(ctx, id, client.UpdateAlertRoutingJSONRequestBody{
		Name:        plan.Name.ValueStringPointer(),
		Description: nullableString(plan.Description),
		IsEnabled:   plan.Enabled.ValueBoolPointer(),
		Rules:       &rules,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update alert routing failed", err.Error())
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("Update alert routing failed", apiError(res.StatusCode(), res.Body))
		return
	}
	resp.Diagnostics.Append(setAlertRouteState(&plan, res.JSON200, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AlertRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AlertRouteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := int32ID(state.ID, "id", &resp.Diagnostics)
	if !ok {
		return
	}

	res, err := r.client.DeleteAlertRoutingWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Delete alert routing failed", err.Error())
		return
	}
	switch res.StatusCode() {
	case http.StatusNoContent, http.StatusNotFound:
	case http.StatusConflict:
		resp.Diagnostics.AddError("Alert routing is still in use",
			fmt.Sprintf("An alert source or heartbeat still sends alerts to routing %d. Point them at another routing, or delete them, first. API response: %s", id, apiError(res.StatusCode(), res.Body)))
	default:
		resp.Diagnostics.AddError("Delete alert routing failed", apiError(res.StatusCode(), res.Body))
	}
}

func (r *AlertRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !isNumericID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric alert routing ID, got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func alertRouteRulesToAPI(rules []alertRouteRuleModel) ([]client.AlertRoutingRuleInput, error) {
	out := make([]routeRuleJSON, 0, len(rules))
	for i, rule := range rules {
		conditions, operator, err := conditionGroupsToJSON(rule.ConditionGroups)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		if conditions == nil {
			conditions, operator = []json.RawMessage{}, logicalAND
		}
		r := routeRuleJSON{
			Name:            rule.Name.ValueStringPointer(),
			LogicalOperator: operator,
			Conditions:      conditions,
			Action:          routeActionJSON{Type: actionDoNothing},
		}
		if e := rule.Escalate; e != nil {
			value := &escalateValueJSON{NotificationUrgency: e.Urgency.ValueStringPointer()}
			for _, t := range []struct {
				kind string
				ids  []types.String
			}{{targetEscalationPolicy, e.EscalationPolicyIDs}, {targetSchedule, e.ScheduleIDs}, {targetUser, e.UserIDs}} {
				for _, id := range t.ids {
					value.Escalation = append(value.Escalation, escalationTargetJSON{Type: t.kind, Value: mustAtoi(id)})
				}
			}
			if !e.DelaySeconds.IsNull() {
				ms := e.DelaySeconds.ValueInt64() * millisecondsPerSecond
				value.DelayMs = &ms
			}
			r.Action = routeActionJSON{Type: actionEscalate, Value: value}
		}
		out = append(out, r)
	}
	return decodeJSON[[]client.AlertRoutingRuleInput](out)
}

func setAlertRouteState(m *AlertRouteResourceModel, r *client.AlertRouting, importing bool) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = itoa(r.Id)
	m.Name = types.StringValue(r.Name)
	m.Description = stringFromNullable(r.Description)
	if m.Description.ValueString() == "" {
		m.Description = types.StringNull()
	}
	m.TeamID = idFromNullable(r.TeamId)
	m.Enabled = types.BoolValue(r.IsEnabled)

	rules, err := decodeJSON[[]routeRuleJSON](r.Rules)
	if err != nil {
		diags.AddError("Unreadable alert routing rules", err.Error())
		return diags
	}
	m.Rules = nil
	for i, rule := range rules {
		model, err := alertRouteRuleFromAPI(rule)
		if err != nil && importing {
			diags.AddError("Unsupported alert routing rule", fmt.Sprintf("Alert routing %d, rule %d: %s.", r.Id, i, err))
			return diags
		}
		if err != nil {
			diags.AddWarning("Alert routing rules changed outside Terraform",
				fmt.Sprintf("Alert routing %d, rule %d: %s. The next apply replaces the rules with those in the configuration.", r.Id, i, err))
			model.ConditionGroups = []conditionGroupModel[routeConditionModel]{}
		}
		m.Rules = append(m.Rules, model)
	}
	return diags
}

var errNeverMatchingRule = errors.New("the rule has no conditions joined by OR, so it never matches, and the provider cannot express it. " +
	"Give it conditions or delete it in the app")

func alertRouteRuleFromAPI(rule routeRuleJSON) (alertRouteRuleModel, error) {
	m := alertRouteRuleModel{Name: types.StringPointerValue(rule.Name), DoNothing: types.BoolNull()}
	switch rule.Action.Type {
	case actionDoNothing:
		m.DoNothing = types.BoolValue(true)
	case actionEscalate:
		e := &escalateActionModel{Urgency: types.StringNull(), DelaySeconds: types.Int64Null()}
		if v := rule.Action.Value; v != nil {
			for _, t := range v.Escalation {
				id := types.StringValue(strconv.Itoa(t.Value))
				switch t.Type {
				case targetEscalationPolicy:
					e.EscalationPolicyIDs = append(e.EscalationPolicyIDs, id)
				case targetSchedule:
					e.ScheduleIDs = append(e.ScheduleIDs, id)
				case targetUser:
					e.UserIDs = append(e.UserIDs, id)
				}
			}
			e.Urgency = types.StringPointerValue(v.NotificationUrgency)
			if v.DelayMs != nil && *v.DelayMs > 0 {
				e.DelaySeconds = types.Int64Value(secondsFromMs(*v.DelayMs))
			}
		}
		m.Escalate = e
	default:
		return m, fmt.Errorf("unsupported action %q", rule.Action.Type)
	}

	if len(rule.Conditions) == 0 && rule.LogicalOperator != logicalAND {
		return m, errNeverMatchingRule
	}
	groups, err := conditionGroupsFromJSON(rule.Conditions, rule.LogicalOperator, routeConditionFromJSON)
	m.ConditionGroups = groups
	return m, err
}

func (c routeConditionModel) toJSON() (json.RawMessage, error) {
	if c.Count == nil {
		return c.conditionModel.toJSON()
	}
	return json.Marshal(leafJSON{Type: "count", Rule: countRuleJSON{
		CountThreshold: c.Count.Threshold.ValueInt64(),
		Timeframe:      c.Count.TimeframeSeconds.ValueInt64() * millisecondsPerSecond,
	}})
}

func routeConditionFromJSON(raw []byte) (routeConditionModel, error) {
	var head struct {
		Type string          `json:"type"`
		Rule json.RawMessage `json:"rule"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return routeConditionModel{}, err
	}
	if head.Type != "count" {
		c, err := conditionFromJSON(raw)
		return routeConditionModel{conditionModel: c}, err
	}
	var v countRuleJSON
	if err := json.Unmarshal(head.Rule, &v); err != nil {
		return routeConditionModel{}, err
	}
	return routeConditionModel{Count: &countConditionModel{
		Threshold:        types.Int64Value(v.CountThreshold),
		TimeframeSeconds: types.Int64Value(secondsFromMs(v.Timeframe)),
	}}, nil
}
