package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type conditionGroupModel[L any] struct {
	Operator   types.String `tfsdk:"operator"`
	Conditions []L          `tfsdk:"conditions"`
}

type conditionModel struct {
	Severity *severityConditionModel `tfsdk:"severity"`
	Field    *fieldConditionModel    `tfsdk:"field"`
	Provider *providerConditionModel `tfsdk:"provider"`
	Time     *timeConditionModel     `tfsdk:"time"`
}

type severityConditionModel struct {
	Operator types.String `tfsdk:"operator"`
	Severity types.String `tfsdk:"severity"`
}

type fieldConditionModel struct {
	FieldPath     types.String `tfsdk:"field_path"`
	Operator      types.String `tfsdk:"operator"`
	Value         types.String `tfsdk:"value"`
	CaseSensitive types.Bool   `tfsdk:"case_sensitive"`
}

type providerConditionModel struct {
	Providers []types.String `tfsdk:"providers"`
}

type timeConditionModel struct {
	Timezone types.String           `tfsdk:"timezone"`
	Daily    *dailyTimeRangeModel   `tfsdk:"daily"`
	Weekly   []weeklyTimeRangeModel `tfsdk:"weekly"`
}

type dailyTimeRangeModel struct {
	StartTime types.String `tfsdk:"start_time"`
	EndTime   types.String `tfsdk:"end_time"`
}

type weeklyTimeRangeModel struct {
	StartDay  types.String `tfsdk:"start_day"`
	EndDay    types.String `tfsdk:"end_day"`
	StartTime types.String `tfsdk:"start_time"`
	EndTime   types.String `tfsdk:"end_time"`
}

const (
	logicalAND = "AND"
	logicalOR  = "OR"
)

var alertProviders = []string{
	"opsgenie", "grafana", "datadog", "coralogix", "newrelic", "prometheus", "cloudwatch", "sns",
	"heartbeat", "email", "sentry", "embraceio", "http", "azuremonitor", "cloudamqp", "pagerduty",
}

func conditionGroupsAttribute(description string, conditionKinds map[string]schema.SingleNestedAttribute) schema.ListNestedAttribute {
	kinds := slices.Sorted(maps.Keys(conditionKinds))
	return schema.ListNestedAttribute{
		MarkdownDescription: description,
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
					MarkdownDescription: "Conditions in the group. Each sets exactly one of " + quotedList(kinds) + ".",
					Required:            true,
					Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
					NestedObject:        schema.NestedAttributeObject{Attributes: exactlyOneKind(conditionKinds, kinds)},
				},
			},
		},
	}
}

func quotedList(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "`" + w + "`"
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

func conditionAttributes() map[string]schema.SingleNestedAttribute {
	return map[string]schema.SingleNestedAttribute{
		"severity": {
			MarkdownDescription: "Matches the alert severity. `SEV0` is the most severe and `SIGNAL` the least.",
			Optional:            true,
			Attributes: map[string]schema.Attribute{
				"operator": schema.StringAttribute{
					MarkdownDescription: "One of `equal_to`, `less_than`, `greater_than`, `less_than_or_equal`, `greater_than_or_equal`. `greater_than` means more severe, so `less_than_or_equal` with `SEV3` matches SEV3, SEV4, SEV5 and SIGNAL.",
					Required:            true,
					Validators: []validator.String{stringvalidator.OneOf(
						"equal_to", "less_than", "greater_than", "less_than_or_equal", "greater_than_or_equal",
					)},
				},
				"severity": schema.StringAttribute{
					MarkdownDescription: "One of `SEV0` to `SEV5`, or `SIGNAL`.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.OneOf("SEV0", "SEV1", "SEV2", "SEV3", "SEV4", "SEV5", "SIGNAL")},
				},
			},
		},
		"field": {
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
					Validators:          []validator.String{stringvalidator.OneOf("contains", "exact_match", "not_contains", "matches_regex")},
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
		"provider": {
			MarkdownDescription: "Matches alerts from any of the given alert sources.",
			Optional:            true,
			Attributes: map[string]schema.Attribute{
				"providers": schema.SetAttribute{
					MarkdownDescription: "Alert source types, such as `datadog`, `grafana` or `opsgenie`.",
					ElementType:         types.StringType,
					Required:            true,
					Validators: []validator.Set{
						setvalidator.SizeAtLeast(1),
						setvalidator.ValueStringsAre(stringvalidator.OneOf(alertProviders...)),
					},
				},
			},
		},
		"time": {
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

func exactlyOneKind(conditionKinds map[string]schema.SingleNestedAttribute, kinds []string) map[string]schema.Attribute {
	others := make([]path.Expression, 0, len(kinds)-1)
	for _, name := range kinds[1:] {
		others = append(others, path.MatchRelative().AtParent().AtName(name))
	}
	attrs := make(map[string]schema.Attribute, len(kinds))
	for i, name := range kinds {
		kind := conditionKinds[name]
		if i == 0 {
			kind.Validators = []validator.Object{objectvalidator.ExactlyOneOf(others...)}
		}
		attrs[name] = kind
	}
	return attrs
}

type conditionNode struct {
	Type       string            `json:"type"`
	Operator   string            `json:"operator"`
	Conditions []json.RawMessage `json:"conditions"`
}

type leafJSON struct {
	Type string `json:"type"`
	Rule any    `json:"rule"`
}

type severityRuleJSON struct {
	Operator string `json:"operator"`
	Severity string `json:"severity"`
}

type fieldRuleJSON struct {
	FieldPath       string `json:"fieldPath"`
	Operator        string `json:"operator"`
	ConditionValue  string `json:"conditionValue"`
	IsCaseSensitive *bool  `json:"isCaseSensitive,omitempty"`
}

type providerRuleJSON struct {
	Operator  string   `json:"operator"`
	Providers []string `json:"providers"`
}

type timeRuleJSON struct {
	Timezone     string          `json:"timezone"`
	Operator     string          `json:"operator"`
	TimeInterval json.RawMessage `json:"timeInterval"`
}

type dailyIntervalJSON struct {
	StartHourOffset string `json:"startHourOffset"`
	EndHourOffset   string `json:"endHourOffset"`
}

type weekRangeJSON struct {
	StartDay        int    `json:"startDay"`
	EndDay          int    `json:"endDay"`
	StartHourOffset string `json:"startHourOffset"`
	EndHourOffset   string `json:"endHourOffset"`
}

type weeklyIntervalJSON struct {
	WeekRanges []weekRangeJSON `json:"weekRanges"`
}

const (
	dayInBetween  = "day_in_between"
	weekInBetween = "week_in_between"
)

var errUnsupportedConditionShape = errors.New("the provider supports the condition shapes the app builds: " +
	"plain conditions, or groups of plain conditions joined by OR. Rewrite the conditions in the app into that shape")

type conditionLeaf interface {
	toJSON() (json.RawMessage, error)
}

func conditionGroupsToJSON[L conditionLeaf](groups []conditionGroupModel[L]) ([]json.RawMessage, string, error) {
	if len(groups) == 0 {
		return nil, "", nil
	}
	items, operator := []json.RawMessage{}, logicalOR
	for _, g := range groups {
		leaves := make([]json.RawMessage, 0, len(g.Conditions))
		for _, c := range g.Conditions {
			raw, err := c.toJSON()
			if err != nil {
				return nil, "", err
			}
			leaves = append(leaves, raw)
		}
		if len(groups) == 1 {
			items, operator = leaves, g.Operator.ValueString()
			break
		}
		group, err := json.Marshal(conditionNode{Type: "group", Operator: g.Operator.ValueString(), Conditions: leaves})
		if err != nil {
			return nil, "", err
		}
		items = append(items, group)
	}
	return items, operator, nil
}

func (c conditionModel) toJSON() (json.RawMessage, error) {
	switch {
	case c.Severity != nil:
		return json.Marshal(leafJSON{Type: "severity", Rule: severityRuleJSON{
			Operator: c.Severity.Operator.ValueString(),
			Severity: c.Severity.Severity.ValueString(),
		}})
	case c.Field != nil:
		return json.Marshal(leafJSON{Type: "field", Rule: fieldRuleJSON{
			FieldPath:       c.Field.FieldPath.ValueString(),
			Operator:        c.Field.Operator.ValueString(),
			ConditionValue:  c.Field.Value.ValueString(),
			IsCaseSensitive: c.Field.CaseSensitive.ValueBoolPointer(),
		}})
	case c.Provider != nil:
		rule := providerRuleJSON{Operator: "one_of"}
		for _, p := range c.Provider.Providers {
			rule.Providers = append(rule.Providers, p.ValueString())
		}
		return json.Marshal(leafJSON{Type: "provider", Rule: rule})
	case c.Time != nil:
		rule := timeRuleJSON{Timezone: c.Time.Timezone.ValueString()}
		var interval any
		if c.Time.Daily != nil {
			rule.Operator = dayInBetween
			interval = dailyIntervalJSON{StartHourOffset: c.Time.Daily.StartTime.ValueString(), EndHourOffset: c.Time.Daily.EndTime.ValueString()}
		} else {
			rule.Operator = weekInBetween
			weekly := weeklyIntervalJSON{WeekRanges: make([]weekRangeJSON, 0, len(c.Time.Weekly))}
			for _, w := range c.Time.Weekly {
				weekly.WeekRanges = append(weekly.WeekRanges, weekRangeJSON{
					StartDay:        weekdayNumber(w.StartDay),
					EndDay:          weekdayNumber(w.EndDay),
					StartHourOffset: w.StartTime.ValueString(),
					EndHourOffset:   w.EndTime.ValueString(),
				})
			}
			interval = weekly
		}
		raw, err := json.Marshal(interval)
		if err != nil {
			return nil, err
		}
		rule.TimeInterval = raw
		return json.Marshal(leafJSON{Type: "time", Rule: rule})
	}
	return nil, fmt.Errorf("condition sets no condition type")
}

func conditionGroupsFromJSON[L any](items []json.RawMessage, operator string, leafFromJSON func([]byte) (L, error)) ([]conditionGroupModel[L], error) {
	if len(items) == 0 {
		return nil, nil
	}
	leaves, err := mapErr(items, leafFromJSON)
	if err == nil {
		return []conditionGroupModel[L]{{Operator: types.StringValue(operator), Conditions: leaves}}, nil
	}
	if !errors.Is(err, errUnsupportedConditionShape) || (operator != logicalOR && len(items) > 1) {
		return nil, err
	}

	groups := make([]conditionGroupModel[L], 0, len(items))
	for _, item := range items {
		var node conditionNode
		if err := json.Unmarshal(item, &node); err != nil {
			return nil, err
		}
		if node.Type != "group" {
			return nil, errUnsupportedConditionShape
		}
		leaves, err := mapErr(node.Conditions, leafFromJSON)
		if err != nil {
			return nil, err
		}
		groups = append(groups, conditionGroupModel[L]{Operator: types.StringValue(node.Operator), Conditions: leaves})
	}
	return groups, nil
}

func mapErr[T any](items []json.RawMessage, f func([]byte) (T, error)) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, raw := range items {
		v, err := f(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func conditionFromJSON(raw []byte) (conditionModel, error) {
	var head struct {
		Type string          `json:"type"`
		Rule json.RawMessage `json:"rule"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return conditionModel{}, err
	}

	switch head.Type {
	case "severity":
		var v severityRuleJSON
		if err := json.Unmarshal(head.Rule, &v); err != nil {
			return conditionModel{}, err
		}
		return conditionModel{Severity: &severityConditionModel{
			Operator: types.StringValue(v.Operator),
			Severity: types.StringValue(v.Severity),
		}}, nil
	case "field":
		var v fieldRuleJSON
		if err := json.Unmarshal(head.Rule, &v); err != nil {
			return conditionModel{}, err
		}
		return conditionModel{Field: &fieldConditionModel{
			FieldPath:     types.StringValue(v.FieldPath),
			Operator:      types.StringValue(v.Operator),
			Value:         types.StringValue(v.ConditionValue),
			CaseSensitive: types.BoolPointerValue(v.IsCaseSensitive),
		}}, nil
	case "provider":
		var v providerRuleJSON
		if err := json.Unmarshal(head.Rule, &v); err != nil {
			return conditionModel{}, err
		}
		providers := make([]types.String, 0, len(v.Providers))
		for _, p := range v.Providers {
			providers = append(providers, types.StringValue(p))
		}
		return conditionModel{Provider: &providerConditionModel{Providers: providers}}, nil
	case "time":
		var v timeRuleJSON
		if err := json.Unmarshal(head.Rule, &v); err != nil {
			return conditionModel{}, err
		}
		tc := &timeConditionModel{Timezone: types.StringValue(v.Timezone)}
		switch v.Operator {
		case dayInBetween:
			var d dailyIntervalJSON
			if err := json.Unmarshal(v.TimeInterval, &d); err != nil {
				return conditionModel{}, err
			}
			tc.Daily = &dailyTimeRangeModel{StartTime: types.StringValue(d.StartHourOffset), EndTime: types.StringValue(d.EndHourOffset)}
		case weekInBetween:
			var w weeklyIntervalJSON
			if err := json.Unmarshal(v.TimeInterval, &w); err != nil {
				return conditionModel{}, err
			}
			for _, r := range w.WeekRanges {
				tc.Weekly = append(tc.Weekly, weeklyTimeRangeModel{
					StartDay:  weekdayName(r.StartDay),
					EndDay:    weekdayName(r.EndDay),
					StartTime: types.StringValue(r.StartHourOffset),
					EndTime:   types.StringValue(r.EndHourOffset),
				})
			}
		default:
			return conditionModel{}, fmt.Errorf("unsupported time condition operator %q", v.Operator)
		}
		return conditionModel{Time: tc}, nil
	case "group":
		return conditionModel{}, errUnsupportedConditionShape
	}
	return conditionModel{}, fmt.Errorf("unsupported condition type %q", head.Type)
}

func decodeJSON[T any](v any) (T, error) {
	var out T
	raw, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}
