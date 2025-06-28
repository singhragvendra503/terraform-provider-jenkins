package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/bndr/gojenkins" // Jenkins API client
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// Ensure the provider implements the required interfaces.
var _ provider.Provider = &jenkinsProvider{}

// jenkinsProvider defines the provider implementation.
type jenkinsProvider struct {
	version string
}

// jenkinsProviderModel describes the provider data model.
type jenkinsProviderModel struct {
	// Jenkins URL (e.g., "http://localhost:8080")
	URL types.String `tfsdk:"url"`
	// Jenkins Username
	Username types.String `tfsdk:"username"`
	// Jenkins API Token (NOT your password)
	APIToken types.String `tfsdk:"api_token"`
}

// Metadata returns the provider's metadata.
func (p *jenkinsProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "jenkins" // The name users will use in `required_providers`
	resp.Version = p.version
}

// Schema defines the provider-level configuration schema.
func (p *jenkinsProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The `jenkins` provider manages resources within a Jenkins CI/CD server.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				MarkdownDescription: "The URL of the Jenkins server (e.g., `http://localhost:8080`).",
				Required:            true,
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "The Jenkins username for authentication.",
				Required:            true,
			},
			"api_token": schema.StringAttribute{
				MarkdownDescription: "The Jenkins API token for authentication. This is highly sensitive.",
				Required:            true,
				Sensitive:           true, // Marks the attribute as sensitive, so it's not shown in logs
			},
		},
	}
}

// Configure initializes the Jenkins client based on provider configuration.
func (p *jenkinsProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data jenkinsProviderModel

	// Read provider configuration from Terraform plan
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Validate configuration
	if data.URL.IsUnknown() || data.URL.IsNull() {
		resp.Diagnostics.AddError(
			"Missing Jenkins URL Configuration",
			"The provider is not configured with a Jenkins URL. Set the 'url' attribute in the provider configuration.",
		)
	}
	if data.Username.IsUnknown() || data.Username.IsNull() {
		resp.Diagnostics.AddError(
			"Missing Jenkins Username Configuration",
			"The provider is not configured with a Jenkins username. Set the 'username' attribute in the provider configuration.",
		)
	}
	if data.APIToken.IsUnknown() || data.APIToken.IsNull() {
		resp.Diagnostics.AddError(
			"Missing Jenkins API Token Configuration",
			"The provider is not configured with a Jenkins API Token. Set the 'api_token' attribute in the provider configuration.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Initialize the Jenkins client
	jenkins := gojenkins.CreateJenkins(
		&http.Client{},
		data.URL.ValueString(),
		data.Username.ValueString(),
		data.APIToken.ValueString(),
	)

	// Test connection to Jenkins
	_, err := jenkins.GetQueue(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Connect to Jenkins",
			fmt.Sprintf("Unable to connect to Jenkins at %s with provided credentials: %s. Please check your URL, username, and API token.", data.URL.ValueString(), err.Error()),
		)
		return
	}

	// Store the client in the provider data for resources and data sources to use
	resp.ResourceData = jenkins
	resp.DataSourceData = jenkins

	log.Printf("[INFO] Jenkins provider configured successfully for URL: %s", data.URL.ValueString())
}

// Resources returns a list of functions that construct resource implementations.
func (p *jenkinsProvider) Resources(ctx context.Context, req provider.ResourcesRequest, resp *provider.ResourcesResponse) {
	resp.Resources = []func() resource.Resource{
		NewJenkinsPipelineResource, // Our custom Jenkins Pipeline resource
	}
}

// DataSources returns a list of functions that construct data source implementations.
func (p *jenkinsProvider) DataSources(ctx context.Context, req provider.DataSourcesRequest, resp *provider.DataSourcesResponse) {
	resp.DataSources = []func() datasource.DataSource{
		NewJenkinsPipelineDataSource, // Our custom Jenkins Pipeline data source
	}
}

// New creates a new instance of the Jenkins provider.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &jenkinsProvider{
			version: version,
		}
	}
}

// main is the entry point for the Terraform Jenkins provider.
func main() {
	// Set the provider name and version.
	providerName := "jenkins"
	version := "1.0.0" // Consider setting this from build flags in production

	// Serve the provider using the Terraform Plugin Protocol v6.
	err := tfprotov6.Serve(
		providerName,
		func() tfprotov6.ProviderServer {
			return provider.NewProtocol6Server(New(version)())
		},
	)

	if err != nil {
		log.Fatalf("Error serving Jenkins provider: %s", err.Error())
	}
}
