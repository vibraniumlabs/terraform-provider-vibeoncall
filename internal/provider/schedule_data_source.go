package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var _ datasource.DataSourceWithConfigure = &ScheduleDataSource{}

func NewScheduleDataSource() datasource.DataSource {
	return &ScheduleDataSource{}
}

type ScheduleDataSource struct {
	client *client.ClientWithResponses
}

type ScheduleDataSourceModel struct {
	Name      types.String               `tfsdk:"name"`
	TeamID    types.String               `tfsdk:"team_id"`
	ID        types.String               `tfsdk:"id"`
	TimeZone  types.String               `tfsdk:"time_zone"`
	TeamIDs   types.Set                  `tfsdk:"team_ids"`
	Rotations []scheduleRotationRefModel `tfsdk:"rotations"`
}

type scheduleRotationRefModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (d *ScheduleDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_schedule"
}

func (d *ScheduleDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a schedule by name, for example to page one that Terraform does not manage from `vibeoncall_escalation_policy`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Schedule name. Matched exactly.",
				Required:            true,
				Validators:          []validator.String{noSurroundingWhitespace},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "Numeric ID of a team that owns the schedule. Narrows the search to that team's schedules.",
				Optional:            true,
				Validators:          []validator.String{numericID},
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric schedule ID.",
				Computed:            true,
			},
			"time_zone": schema.StringAttribute{
				MarkdownDescription: "IANA time zone of the schedule.",
				Computed:            true,
			},
			"team_ids": schema.SetAttribute{
				MarkdownDescription: "Numeric IDs of the owning teams. Null for a global schedule.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"rotations": schema.ListNestedAttribute{
				MarkdownDescription: "Rotations in display order.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.StringAttribute{MarkdownDescription: "Numeric rotation ID.", Computed: true},
						"name": schema.StringAttribute{MarkdownDescription: "Rotation name.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *ScheduleDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *ScheduleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ScheduleDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := &client.ListSchedulesParams{}
	limit := int32(listPageSize)
	params.Limit = &limit
	if !data.TeamID.IsNull() {
		teamID, ok := int32ID(data.TeamID, "team_id", &resp.Diagnostics)
		if !ok {
			return
		}
		params.TeamId = &teamID
	}

	name := data.Name.ValueString()
	for offset := int32(0); ; {
		params.Offset = &offset
		res, err := d.client.ListSchedulesWithResponse(ctx, params)
		if err != nil {
			resp.Diagnostics.AddError("List schedules failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			resp.Diagnostics.AddError("List schedules failed", apiError(res.StatusCode(), res.Body))
			return
		}
		for _, s := range res.JSON200.Schedules {
			if s.Name == name {
				setScheduleDataSourceState(&data, &s)
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
				return
			}
		}
		if !res.JSON200.HasMore || len(res.JSON200.Schedules) == 0 {
			break
		}
		offset += int32(len(res.JSON200.Schedules))
	}
	detail := fmt.Sprintf("No schedule is named %q.", name)
	if params.TeamId != nil {
		detail = fmt.Sprintf("Team %d owns no schedule named %q.", *params.TeamId, name)
	}
	resp.Diagnostics.AddError("Schedule not found", detail)
}

func setScheduleDataSourceState(m *ScheduleDataSourceModel, s *client.PublicScheduleResponse) {
	m.ID = itoa(int(s.Id))
	m.TimeZone = types.StringValue(s.Timezone)
	m.TeamIDs = teamIDSet(s.TeamIds)
	m.Rotations = make([]scheduleRotationRefModel, len(s.Rotations))
	for i, rot := range s.Rotations {
		m.Rotations[i] = scheduleRotationRefModel{ID: itoa(int(rot.Id)), Name: types.StringValue(rot.Name)}
	}
}
