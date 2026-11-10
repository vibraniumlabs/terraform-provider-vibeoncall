package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = &VibeOnCallProvider{}

type VibeOnCallProvider struct {
	version string
}

func (p *VibeOnCallProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vibeoncall"
	resp.Version = p.version
}

func (p *VibeOnCallProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Vibe OnCall configuration through the Vibe OnCall public API.",
	}
}

func (p *VibeOnCallProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
}

func (p *VibeOnCallProvider) Resources(ctx context.Context) []func() resource.Resource {
	return nil
}

func (p *VibeOnCallProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return nil
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &VibeOnCallProvider{version: version}
	}
}
