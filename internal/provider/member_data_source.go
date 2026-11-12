package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var _ datasource.DataSourceWithConfigure = &MemberDataSource{}

func NewMemberDataSource() datasource.DataSource {
	return &MemberDataSource{}
}

type MemberDataSource struct {
	client *client.ClientWithResponses
}

type MemberDataSourceModel struct {
	ID       types.String `tfsdk:"id"`
	Email    types.String `tfsdk:"email"`
	FullName types.String `tfsdk:"full_name"`
	Status   types.String `tfsdk:"status"`
}

func (d *MemberDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (d *MemberDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a member of the organization by email. Members are invited or provisioned outside Terraform.",
		Attributes: map[string]schema.Attribute{
			"email": schema.StringAttribute{
				MarkdownDescription: "Email address of the member. Matched exactly, ignoring case.",
				Required:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric user ID, referenced as `user_id` by team memberships.",
				Computed:            true,
			},
			"full_name": schema.StringAttribute{
				MarkdownDescription: "Full name of the member.",
				Computed:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Account status in the organization: `active`, `pending`, or `revoked`.",
				Computed:            true,
			},
		},
	}
}

func (d *MemberDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *MemberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data MemberDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	email := data.Email.ValueString()
	var matches []client.Member
	limit := int32(listPageSize)
	for offset := int32(0); ; offset += limit {
		res, err := d.client.ListMembersWithResponse(ctx, &client.ListMembersParams{Search: &email, Limit: &limit, Offset: &offset})
		if err != nil {
			resp.Diagnostics.AddError("List members failed", err.Error())
			return
		}
		if res.JSON200 == nil {
			resp.Diagnostics.AddError("List members failed", apiError(res.StatusCode(), res.Body))
			return
		}
		for _, m := range res.JSON200.Members {
			if strings.EqualFold(m.Email, email) {
				matches = append(matches, m)
			}
		}
		if !res.JSON200.HasMore {
			break
		}
	}

	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("Member not found", fmt.Sprintf("No member has the email %q.", email))
		return
	case 1:
	default:
		resp.Diagnostics.AddError("Multiple members found", fmt.Sprintf("%d members have the email %q.", len(matches), email))
		return
	}

	m := matches[0]
	data.ID = types.StringValue(strconv.Itoa(int(m.Id)))
	data.FullName = stringFromNullable(m.FullName)
	data.Status = types.StringNull()
	if status, err := m.Status.Get(); err == nil {
		data.Status = types.StringValue(string(status))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
