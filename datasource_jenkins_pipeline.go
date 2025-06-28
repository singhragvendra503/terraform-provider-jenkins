package main

import (
	"context"
	"fmt"
	"log" // Added for logging the Job Not Found case
	"strings"

	// "net/http" // Potentially needed for gojenkins client, ensuring it's available for client instantiation if not already there.
	// "time"

	"github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure the implementation satisfies the datasource.DataSource interface.
var _ datasource.DataSource = &jenkinsPipelineDataSource{}

// NewJenkinsPipelineDataSource is a helper function to simplify provider development.
func NewJenkinsPipelineDataSource() datasource.DataSource {
	return &jenkinsPipelineDataSource{}
}

// jenkinsPipelineDataSource defines the data source implementation.
type jenkinsPipelineDataSource struct {
	client *gojenkins.Jenkins // Jenkins client instance
}

// jenkinsPipelineDataSourceModel describes the data source data model for a Jenkins Pipeline.
type jenkinsPipelineDataSourceModel struct {
	ID                types.String `tfsdk:"id"`                  // Unique identifier (Jenkins job name)
	Name              types.String `tfsdk:"name"`                // Name of the Jenkins job to look up
	Description       types.String `tfsdk:"description"`         // Description of the job (computed)
	GroovyScript      types.String `tfsdk:"groovy_script"`       // The Jenkinsfile/Groovy script content (computed)
	LastBuildStatus   types.String `tfsdk:"last_build_status"`   // Status of the last build (computed)
	LastBuildDuration types.Int64  `tfsdk:"last_build_duration"` // Duration of the last build in milliseconds (computed)
}

// Metadata returns the data source's metadata.
func (d *jenkinsPipelineDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pipeline" // e.g., jenkins_pipeline
}

// Schema defines the data source's schema.
func (d *jenkinsPipelineDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about an existing Jenkins Pipeline job.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The unique identifier (name) of the Jenkins Pipeline job. Can be used instead of `name`.",
				Optional:            true,
				Computed:            true, // If name is provided, ID is computed
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the Jenkins Pipeline job to retrieve.",
				Optional:            true,
				Computed:            true, // If ID is provided, name is computed
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "The description of the Jenkins Pipeline job.",
				Computed:            true,
			},
			"groovy_script": schema.StringAttribute{
				MarkdownDescription: "The Groovy script content for the pipeline (Jenkinsfile content).",
				Computed:            true,
			},
			"last_build_status": schema.StringAttribute{
				MarkdownDescription: "The status of the last completed build (e.g., SUCCESS, FAILURE).",
				Computed:            true,
			},
			"last_build_duration": schema.Int64Attribute{
				MarkdownDescription: "The duration of the last completed build in milliseconds.",
				Computed:            true,
			},
		},
	}
}

// Configure retrieves the Jenkins client from the provider configuration.
func (d *jenkinsPipelineDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // Provider not configured yet, or no client passed
	}

	client, ok := req.ProviderData.(*gojenkins.Jenkins)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *gojenkins.Jenkins, got: %T. Please report this issue to the provider developer.", req.ProviderData),
		)
		return
	}
	d.client = client
}

// Read retrieves the state of a Jenkins Pipeline job.
func (d *jenkinsPipelineDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config jenkinsPipelineDataSourceModel

	// Get the configuration from Terraform
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var jobName string
	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		jobName = config.ID.ValueString()
	} else if !config.Name.IsNull() && !config.Name.IsUnknown() {
		jobName = config.Name.ValueString()
	} else {
		resp.Diagnostics.AddError(
			"Missing Identifier",
			"One of 'id' or 'name' must be provided to read the Jenkins Pipeline data source.",
		)
		return
	}

	// Get the job details directly. gojenkins.GetJob handles checking existence.
	job, err := d.client.GetJob(ctx, jobName)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "no such job") { // gojenkins might return generic errors for 404
			resp.Diagnostics.AddError(
				"Jenkins Job Not Found",
				fmt.Sprintf("No Jenkins Pipeline job found with name/ID: '%s'. Error: %s", jobName, err.Error()),
			)
			// For a data source, if not found, it's an error. For a resource, it would remove from state.
			return
		}
		resp.Diagnostics.AddError(
			"Jenkins Job Read Error",
			fmt.Sprintf("Failed to get Jenkins job details for '%s': %s", jobName, err.Error()),
		)
		return
	}

	// Get the job configuration XML from Jenkins using the job object
	configXML, err := job.GetConfig(ctx) // FIX: Call GetConfig on the job object, not the client
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Config Read Error",
			fmt.Sprintf("Failed to read Jenkins Pipeline job config for '%s': %s", jobName, err.Error()),
		)
		return
	}

	groovyScript, err := extractGroovyScriptFromXML(configXML)
	if err != nil {
		resp.Diagnostics.AddError(
			"Groovy Script Extraction Error",
			fmt.Sprintf("Failed to extract Groovy script from job '%s' config: %s", jobName, err.Error()),
		)
		groovyScript = "" // Ensure it's not nil, even if error
	}

	description, err := extractDescriptionFromXML(configXML)
	if err != nil {
		resp.Diagnostics.AddError(
			"Description Extraction Error",
			fmt.Sprintf("Failed to extract description from job '%s' config: %s", jobName, err.Error()),
		)
		description = "" // Ensure it's not nil, even if error
	}

	// Get last build information
	var lastBuildStatus string
	var lastBuildDuration int64

	// FIX: Removed `job.Raw.LastCompletedBuild != nil` check.
	// `job.Raw.LastCompletedBuild` is a struct, not a pointer, so it's never nil.
	// The check `Number > 0` is sufficient to determine if a meaningful build exists.
	if job.Raw.LastCompletedBuild.Number > 0 {
		lastBuild, err := job.GetLastCompletedBuild(ctx)
		if err == nil {
			lastBuildStatus = lastBuild.Raw.Result
			lastBuildDuration = int64(lastBuild.Raw.Duration) // FIX: Cast float64 to int64
		} else {
			log.Printf("[WARN] Could not get last completed build details for '%s': %s", jobName, err.Error())
		}
	}

	// Update the state
	config.ID = types.StringValue(job.Raw.Name)
	config.Name = types.StringValue(job.Raw.Name)
	config.Description = types.StringValue(description)
	config.GroovyScript = types.StringValue(groovyScript)
	config.LastBuildStatus = types.StringValue(lastBuildStatus)
	config.LastBuildDuration = types.Int64Value(lastBuildDuration)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)

	log.Printf("[INFO] Jenkins Pipeline data source for '%s' read successfully.", jobName)
}
