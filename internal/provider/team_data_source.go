package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var _ datasource.DataSourceWithConfigure = &TeamDataSource{}

func NewTeamDataSource() datasource.DataSource {
	return &TeamDataSource{}
}

type TeamDataSource struct {
	client *client.ClientWithResponses
}

type TeamDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	ShortID     types.String `tfsdk:"short_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

func (d *TeamDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (d *TeamDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a team by name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Team name. Matched exactly.",
				Required:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric team ID, referenced as `team_id` by other resources.",
				Computed:            true,
			},
			"short_id": schema.StringAttribute{
				MarkdownDescription: "Team short ID, e.g. `VP-TEAM-…`.",
				Computed:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Team description.",
				Computed:            true,
			},
		},
	}
}

func (d *TeamDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *TeamDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TeamDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := data.Name.ValueString()
	var matches []client.TeamSummary
	limit := int32(listPageSize)
	for offset := int32(0); ; offset += limit {
		res, err := d.client.ListTeamsWithResponse(ctx, &client.ListTeamsParams{Limit: &limit, Offset: &offset})
		if err != nil {
			resp.Diagnostics.AddError("List teams failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			resp.Diagnostics.AddError("List teams failed", apiError(res.StatusCode(), res.Body))
			return
		}
		for _, t := range res.JSON200.Teams {
			if t.Name == name && !slices.ContainsFunc(matches, func(m client.TeamSummary) bool { return m.Id == t.Id }) {
				matches = append(matches, t)
			}
		}
		if !res.JSON200.HasMore {
			break
		}
	}

	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("Team not found", fmt.Sprintf("No team is named %q.", name))
		return
	case 1:
	default:
		resp.Diagnostics.AddError("Multiple teams found", fmt.Sprintf("%d teams are named %q.", len(matches), name))
		return
	}

	t := matches[0]
	data.ID = types.StringValue(strconv.Itoa(int(t.Id)))
	data.ShortID = types.StringValue(t.ShortId)
	data.Description = stringFromNullable(t.Description)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
