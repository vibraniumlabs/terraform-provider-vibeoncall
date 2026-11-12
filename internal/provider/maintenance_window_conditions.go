package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

type conditionGroupModel struct {
	Operator   types.String     `tfsdk:"operator"`
	Conditions []conditionModel `tfsdk:"conditions"`
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

type conditionNode struct {
	Type       string            `json:"type"`
	Operator   string            `json:"operator"`
	Conditions []json.RawMessage `json:"conditions"`
}

var errUnsupportedConditionShape = errors.New("the provider supports the condition shapes the app builds: " +
	"plain conditions, or groups of plain conditions joined by OR. Rewrite the conditions in the app into that shape")

func conditionGroupsToAPI(groups []conditionGroupModel) ([]client.MaintenanceWindowCondition, string, error) {
	if len(groups) == 0 {
		return nil, "", nil
	}
	items, operator := []json.RawMessage{}, logicalOR
	for _, g := range groups {
		leaves, err := leavesToJSON(g.Conditions)
		if err != nil {
			return nil, "", err
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
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, "", err
	}
	var out []client.MaintenanceWindowCondition
	return out, operator, json.Unmarshal(raw, &out)
}

func leavesToJSON(conditions []conditionModel) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(conditions))
	for _, c := range conditions {
		raw, err := leafToJSON(c)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func leafToJSON(c conditionModel) (json.RawMessage, error) {
	switch {
	case c.Severity != nil:
		var v client.MaintenanceWindowSeverityCondition
		v.Type = client.Severity
		v.Rule.Operator = client.MaintenanceWindowSeverityConditionRuleOperator(c.Severity.Operator.ValueString())
		v.Rule.Severity = c.Severity.Severity.ValueString()
		return json.Marshal(v)
	case c.Field != nil:
		var v client.MaintenanceWindowFieldCondition
		v.Type = client.Field
		v.Rule.FieldPath = c.Field.FieldPath.ValueString()
		v.Rule.Operator = client.MaintenanceWindowFieldConditionRuleOperator(c.Field.Operator.ValueString())
		v.Rule.ConditionValue = c.Field.Value.ValueString()
		if !c.Field.CaseSensitive.IsNull() {
			v.Rule.IsCaseSensitive.Set(c.Field.CaseSensitive.ValueBool())
		}
		return json.Marshal(v)
	case c.Provider != nil:
		var v client.MaintenanceWindowProviderCondition
		v.Type = client.Provider
		v.Rule.Operator = client.OneOf
		for _, p := range c.Provider.Providers {
			v.Rule.Providers = append(v.Rule.Providers, client.MaintenanceWindowProviderConditionRuleProviders(p.ValueString()))
		}
		return json.Marshal(v)
	case c.Time != nil:
		var v client.MaintenanceWindowTimeCondition
		v.Type = client.Time
		v.Rule.Timezone = c.Time.Timezone.ValueString()
		var err error
		if c.Time.Daily != nil {
			v.Rule.Operator = client.DayInBetween
			err = v.Rule.TimeInterval.FromMaintenanceWindowTimeConditionRuleTimeInterval0(client.MaintenanceWindowTimeConditionRuleTimeInterval0{
				StartHourOffset: c.Time.Daily.StartTime.ValueString(),
				EndHourOffset:   c.Time.Daily.EndTime.ValueString(),
			})
		} else {
			v.Rule.Operator = client.WeekInBetween
			var weekly client.MaintenanceWindowTimeConditionRuleTimeInterval1
			weekly.WeekRanges = slices.Grow(weekly.WeekRanges, len(c.Time.Weekly))[:len(c.Time.Weekly)]
			for i, w := range c.Time.Weekly {
				weekly.WeekRanges[i].StartDay = weekdayNumber(w.StartDay)
				weekly.WeekRanges[i].EndDay = weekdayNumber(w.EndDay)
				weekly.WeekRanges[i].StartHourOffset = w.StartTime.ValueString()
				weekly.WeekRanges[i].EndHourOffset = w.EndTime.ValueString()
			}
			err = v.Rule.TimeInterval.FromMaintenanceWindowTimeConditionRuleTimeInterval1(weekly)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}
	return nil, fmt.Errorf("condition sets none of severity, field, provider or time")
}

func conditionGroupsFromAPI(conditions []client.MaintenanceWindowCondition, operator string) ([]conditionGroupModel, error) {
	if len(conditions) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(conditions)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}

	leaves, err := leavesFromJSON(items)
	if err == nil {
		return []conditionGroupModel{{Operator: types.StringValue(operator), Conditions: leaves}}, nil
	}
	if !errors.Is(err, errUnsupportedConditionShape) || (operator != logicalOR && len(items) > 1) {
		return nil, err
	}

	groups := make([]conditionGroupModel, 0, len(items))
	for _, item := range items {
		var node conditionNode
		if err := json.Unmarshal(item, &node); err != nil {
			return nil, err
		}
		if node.Type != "group" {
			return nil, errUnsupportedConditionShape
		}
		leaves, err := leavesFromJSON(node.Conditions)
		if err != nil {
			return nil, err
		}
		groups = append(groups, conditionGroupModel{Operator: types.StringValue(node.Operator), Conditions: leaves})
	}
	return groups, nil
}

func leavesFromJSON(items []json.RawMessage) ([]conditionModel, error) {
	out := make([]conditionModel, 0, len(items))
	for _, raw := range items {
		leaf, err := leafFromJSON(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, leaf)
	}
	return out, nil
}

func leafFromJSON(raw []byte) (conditionModel, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return conditionModel{}, err
	}

	switch head.Type {
	case string(client.Severity):
		var v client.MaintenanceWindowSeverityCondition
		if err := json.Unmarshal(raw, &v); err != nil {
			return conditionModel{}, err
		}
		return conditionModel{Severity: &severityConditionModel{
			Operator: types.StringValue(string(v.Rule.Operator)),
			Severity: types.StringValue(v.Rule.Severity),
		}}, nil
	case string(client.Field):
		var v client.MaintenanceWindowFieldCondition
		if err := json.Unmarshal(raw, &v); err != nil {
			return conditionModel{}, err
		}
		caseSensitive := types.BoolNull()
		if b, err := v.Rule.IsCaseSensitive.Get(); err == nil {
			caseSensitive = types.BoolValue(b)
		}
		return conditionModel{Field: &fieldConditionModel{
			FieldPath:     types.StringValue(v.Rule.FieldPath),
			Operator:      types.StringValue(string(v.Rule.Operator)),
			Value:         types.StringValue(v.Rule.ConditionValue),
			CaseSensitive: caseSensitive,
		}}, nil
	case string(client.Provider):
		var v client.MaintenanceWindowProviderCondition
		if err := json.Unmarshal(raw, &v); err != nil {
			return conditionModel{}, err
		}
		providers := make([]types.String, 0, len(v.Rule.Providers))
		for _, p := range v.Rule.Providers {
			providers = append(providers, types.StringValue(string(p)))
		}
		return conditionModel{Provider: &providerConditionModel{Providers: providers}}, nil
	case string(client.Time):
		var v client.MaintenanceWindowTimeCondition
		if err := json.Unmarshal(raw, &v); err != nil {
			return conditionModel{}, err
		}
		tc := &timeConditionModel{Timezone: types.StringValue(v.Rule.Timezone)}
		switch v.Rule.Operator {
		case client.DayInBetween:
			d, err := v.Rule.TimeInterval.AsMaintenanceWindowTimeConditionRuleTimeInterval0()
			if err != nil {
				return conditionModel{}, err
			}
			tc.Daily = &dailyTimeRangeModel{StartTime: types.StringValue(d.StartHourOffset), EndTime: types.StringValue(d.EndHourOffset)}
		case client.WeekInBetween:
			w, err := v.Rule.TimeInterval.AsMaintenanceWindowTimeConditionRuleTimeInterval1()
			if err != nil {
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
			return conditionModel{}, fmt.Errorf("unsupported time condition operator %q", v.Rule.Operator)
		}
		return conditionModel{Time: tc}, nil
	case "group":
		return conditionModel{}, errUnsupportedConditionShape
	}
	return conditionModel{}, fmt.Errorf("unsupported condition type %q", head.Type)
}
