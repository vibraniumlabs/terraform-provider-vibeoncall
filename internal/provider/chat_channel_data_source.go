package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var _ datasource.DataSourceWithConfigure = &ChatChannelDataSource{}

func NewChatChannelDataSource() datasource.DataSource {
	return &ChatChannelDataSource{}
}

type ChatChannelDataSource struct {
	client *client.ClientWithResponses
}

type ChatChannelDataSourceModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	MicrosoftTeamsTeamID types.String `tfsdk:"microsoft_teams_team_id"`
}

func (d *ChatChannelDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_chat_channel"
}

func (d *ChatChannelDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a channel by name on the Slack workspace or Microsoft Teams tenant the organization is connected to.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Channel name, without `#`. Matched exactly, ignoring case.",
				Required:            true,
				Validators:          []validator.String{noSurroundingWhitespace},
			},
			"microsoft_teams_team_id": schema.StringAttribute{
				MarkdownDescription: "ID of the Microsoft Teams team the channel belongs to: the `groupId` in the team's link. Required on Microsoft Teams, rejected on Slack.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Platform channel ID, referenced by maintenance window announcement channels and escalation policy channel targets.",
				Computed:            true,
			},
		},
	}
}

func (d *ChatChannelDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *ChatChannelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ChatChannelDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := data.Name.ValueString()
	var matches []client.ChatChannel
	limit := int32(listPageSize)
	for offset := int32(0); ; offset += limit {
		res, err := d.client.ListChatChannelsWithResponse(ctx, &client.ListChatChannelsParams{
			Search:               &name,
			MicrosoftTeamsTeamId: data.MicrosoftTeamsTeamID.ValueStringPointer(),
			Limit:                &limit,
			Offset:               &offset,
		})
		if err != nil {
			resp.Diagnostics.AddError("List chat channels failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			resp.Diagnostics.AddError("List chat channels failed", apiError(res.StatusCode(), res.Body))
			return
		}
		for _, c := range res.JSON200.Channels {
			if strings.EqualFold(c.Name, name) {
				matches = append(matches, c)
			}
		}
		if !res.JSON200.HasMore {
			break
		}
	}

	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("Chat channel not found", fmt.Sprintf("No channel is named %q.", name))
		return
	case 1:
	default:
		resp.Diagnostics.AddError("Multiple chat channels found", fmt.Sprintf("%d channels are named %q.", len(matches), name))
		return
	}

	data.ID = types.StringValue(matches[0].Id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
