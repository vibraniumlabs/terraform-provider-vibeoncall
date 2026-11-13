package provider

import (
	"context"
	"net/http"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

const defaultBaseURL = "https://api-prod.vibraniumlabs.ai/api/v1"

var _ provider.Provider = &VibeOnCallProvider{}

type VibeOnCallProvider struct {
	version string
}

type VibeOnCallProviderModel struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

func (p *VibeOnCallProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vibeoncall"
	resp.Version = p.version
}

func (p *VibeOnCallProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Vibe OnCall configuration through the Vibe OnCall public API.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "Vibe OnCall API key. Defaults to the `VIBEONCALL_API_KEY` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Public API base URL. Defaults to `VIBEONCALL_BASE_URL`, then " + defaultBaseURL + ".",
				Optional:            true,
			},
		},
	}
}

func (p *VibeOnCallProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data VibeOnCallProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Unknown API key", "api_key must be known before planning. Set it to a known value or use the VIBEONCALL_API_KEY environment variable.")
	}
	if data.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Unknown base URL", "base_url must be known before planning. Set it to a known value or use the VIBEONCALL_BASE_URL environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := firstNonEmpty(data.APIKey.ValueString(), os.Getenv("VIBEONCALL_API_KEY"))
	if apiKey == "" {
		resp.Diagnostics.AddError("Missing API key", "Set api_key in the provider block or the VIBEONCALL_API_KEY environment variable.")
		return
	}
	baseURL := firstNonEmpty(data.BaseURL.ValueString(), os.Getenv("VIBEONCALL_BASE_URL"), defaultBaseURL)

	c, err := client.NewClientWithResponses(baseURL,
		client.WithHTTPClient(&http.Client{Transport: newRetryTransport(http.DefaultTransport)}),
		client.WithRequestEditorFn(func(ctx context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+apiKey)
			r.Header.Set("User-Agent", "terraform-provider-vibeoncall/"+p.version)
			return nil
		}),
	)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create API client", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *VibeOnCallProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewTeamResource,
		NewTeamMembershipResource,
		NewEscalationPolicyResource,
		NewTeamDefaultEscalationPolicyResource,
		NewScheduleResource,
		NewServiceResource,
	}
}

func (p *VibeOnCallProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewMemberDataSource,
		NewTeamDataSource,
		NewLabelDataSource,
		NewChatChannelDataSource,
		NewScheduleDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &VibeOnCallProvider{version: version}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
