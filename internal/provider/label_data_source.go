package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var _ datasource.DataSourceWithConfigure = &LabelDataSource{}

func NewLabelDataSource() datasource.DataSource {
	return &LabelDataSource{}
}

type LabelDataSource struct {
	client *client.ClientWithResponses
}

type LabelDataSourceModel struct {
	ID    types.String `tfsdk:"id"`
	Name  types.String `tfsdk:"name"`
	Color types.String `tfsdk:"color"`
}

func (d *LabelDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_label"
}

func (d *LabelDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a label by name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Label name. Matched exactly.",
				Required:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric label ID.",
				Computed:            true,
			},
			"color": schema.StringAttribute{
				MarkdownDescription: "Hex color code, e.g. `#FF0000`.",
				Computed:            true,
			},
		},
	}
}

func (d *LabelDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *LabelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data LabelDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	res, err := d.client.ListLabelsWithResponse(ctx)
	if err != nil {
		resp.Diagnostics.AddError("List labels failed", err.Error())
		return
	}
	if res.JSON200 == nil {
		resp.Diagnostics.AddError("List labels failed", apiError(res.StatusCode(), res.Body))
		return
	}

	name := data.Name.ValueString()
	for _, l := range res.JSON200.Labels {
		if l.Name == name {
			data.ID = types.StringValue(strconv.Itoa(int(l.Id)))
			data.Color = types.StringValue(l.Color)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
	}
	resp.Diagnostics.AddError("Label not found", fmt.Sprintf("No label is named %q.", name))
}
