package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure the implementation satisfies the resource.Resource and resource.ResourceWithImportState interfaces.
var _ resource.Resource = &jenkinsPipelineResource{}
var _ resource.ResourceWithImportState = &jenkinsPipelineResource{}

// NewJenkinsPipelineResource is a helper function to simplify provider development.
func NewJenkinsPipelineResource() resource.Resource {
	return &jenkinsPipelineResource{}
}

// jenkinsPipelineResource defines the resource implementation.
type jenkinsPipelineResource struct {
	client *gojenkins.Jenkins // Jenkins client instance
}

// jenkinsPipelineResourceModel describes the resource data model for a Jenkins Pipeline.
type jenkinsPipelineResourceModel struct {
	ID           types.String `tfsdk:"id"`            // Unique identifier (Jenkins job name)
	Name         types.String `tfsdk:"name"`          // Name of the Jenkins job
	Description  types.String `tfsdk:"description"`   // Description of the job
	GroovyScript types.String `tfsdk:"groovy_script"` // The Jenkinsfile/Groovy script content
	LastUpdated  types.String `tfsdk:"last_updated"`  // Timestamp for tracking changes (computed)
}

// Metadata returns the resource's metadata.
func (r *jenkinsPipelineResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pipeline" // e.g., jenkins_pipeline
}

// Schema defines the resource's schema.
func (r *jenkinsPipelineResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Jenkins Pipeline job.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier (name) of the Jenkins Pipeline job.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(), // ID is known after creation
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the Jenkins Pipeline job.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // Cannot rename a job in Jenkins directly via API
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "A description for the Jenkins Pipeline job.",
				Optional:            true,
				Computed:            true,
			},
			"groovy_script": schema.StringAttribute{
				MarkdownDescription: "The Groovy script content for the pipeline (Jenkinsfile content).",
				Required:            true,
			},
			"last_updated": schema.StringAttribute{
				MarkdownDescription: "Timestamp of the last update to the pipeline.",
				Computed:            true,
			},
		},
	}
}

// Configure retrieves the Jenkins client from the provider configuration.
func (r *jenkinsPipelineResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // Provider not configured yet, or no client passed
	}

	client, ok := req.ProviderData.(*gojenkins.Jenkins)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *gojenkins.Jenkins, got: %T. Please report this issue to the provider developer.", req.ProviderData),
		)
		return
	}
	r.client = client
}

// buildPipelineConfigXML generates the XML configuration for a Jenkins Pipeline job.
func buildPipelineConfigXML(description, groovyScript string) string {
	// This is a basic template for a Pipeline job's config.xml.
	// Plugin versions (e.g., workflow-job, workflow-cps) might need to be adjusted
	// depending on your Jenkins setup, but gojenkins often handles this implicitly.
	// For simplicity, we use common placeholder plugin versions.
	configXML := fmt.Sprintf(`<?xml version='1.1' encoding='UTF-8'?>
<flow-definition plugin="workflow-job@1254.v3f669a_b_a_083a_">
  <description>%s</description>
  <keepDependencies>false</keepDependencies>
  <properties/>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition" plugin="workflow-cps@2807.v39e1503c779e">
    <script><![CDATA[%s]]></script>
    <sandbox>true</sandbox>
  </definition>
  <triggers/>
  <disabled>false</disabled>
</flow-definition>`, description, groovyScript)
	return configXML
}

// extractGroovyScriptFromXML attempts to parse the Groovy script from Jenkins job XML.
func extractGroovyScriptFromXML(xmlConfig string) (string, error) {
	// This is a simple regex-like parsing. A more robust solution might use an XML parser.
	// Look for <script><![CDATA[...]]></script>
	scriptStart := "<script><![CDATA["
	scriptEnd := "]]></script>"

	startIndex := strings.Index(xmlConfig, scriptStart)
	if startIndex == -1 {
		return "", fmt.Errorf("could not find start of Groovy script tag in XML")
	}
	startIndex += len(scriptStart)

	endIndex := strings.Index(xmlConfig[startIndex:], scriptEnd)
	if endIndex == -1 {
		return "", fmt.Errorf("could not find end of Groovy script tag in XML")
	}
	endIndex += startIndex

	return xmlConfig[startIndex:endIndex], nil
}

// extractDescriptionFromXML attempts to parse the description from Jenkins job XML.
func extractDescriptionFromXML(xmlConfig string) (string, error) {
	descStart := "<description>"
	descEnd := "</description>"

	startIndex := strings.Index(xmlConfig, descStart)
	if startIndex == -1 {
		return "", fmt.Errorf("could not find start of description tag in XML")
	}
	startIndex += len(descStart)

	endIndex := strings.Index(xmlConfig[startIndex:], descEnd)
	if endIndex == -1 {
		return "", fmt.Errorf("could not find end of description tag in XML")
	}
	endIndex += startIndex

	return xmlConfig[startIndex:endIndex], nil
}

// Create a new Jenkins Pipeline job.
func (r *jenkinsPipelineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan jenkinsPipelineResourceModel

	// Get the plan (desired state) from Terraform
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobName := plan.Name.ValueString()
	description := plan.Description.ValueString()
	groovyScript := plan.GroovyScript.ValueString()

	// Construct the Jenkins job XML
	configXML := buildPipelineConfigXML(description, groovyScript)

	// Check if job already exists (idempotency)
	exists, err := r.client.JobExists(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Client Error",
			fmt.Sprintf("Failed to check if job '%s' exists: %s", jobName, err.Error()),
		)
		return
	}
	if exists {
		resp.Diagnostics.AddError(
			"Job Already Exists",
			fmt.Sprintf("Jenkins job '%s' already exists. Consider importing it or using a different name.", jobName),
		)
		return
	}

	// Create the job in Jenkins
	_, err = r.client.CreateJob(ctx, configXML, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Creation Error",
			fmt.Sprintf("Failed to create Jenkins Pipeline job '%s': %s", jobName, err.Error()),
		)
		return
	}

	// Read back the created job to ensure consistency and get actual state
	job, err := r.client.GetJob(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Read Error",
			fmt.Sprintf("Failed to read created Jenkins Pipeline job '%s': %s", jobName, err.Error()),
		)
		// Even if read fails, we might still have created the job, so don't return early if it's just a read back issue.
		// However, it's better to fail and let user retry apply if state is inconsistent.
		return
	}

	// Update the plan with the actual state from Jenkins
	plan.ID = types.StringValue(job.Raw.Name) // Jenkins job name is its ID
	plan.Name = types.StringValue(job.Raw.Name)
	plan.Description = types.StringValue(job.Raw.Description)
	plan.GroovyScript = types.StringValue(groovyScript) // We assume the script content is as provided
	plan.LastUpdated = types.StringValue(time.Now().Format(time.RFC3339))

	// Set the state in Terraform
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

	log.Printf("[INFO] Jenkins Pipeline job '%s' created successfully.", jobName)
}

// Read retrieves the current state of a Jenkins Pipeline job.
func (r *jenkinsPipelineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state jenkinsPipelineResourceModel

	// Get the current state from Terraform
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobName := state.ID.ValueString() // Use ID from state to read

	// Check if the job exists in Jenkins
	exists, err := r.client.JobExists(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Client Error",
			fmt.Sprintf("Failed to check if job '%s' exists during read: %s", jobName, err.Error()),
		)
		return
	}

	if !exists {
		// Job no longer exists in Jenkins, remove from Terraform state
		resp.State.RemoveResource(ctx)
		log.Printf("[INFO] Jenkins Pipeline job '%s' not found, removing from state.", jobName)
		return
	}

	// Get the job configuration XML from Jenkins
	configXML, err := r.client.GetJobConfig(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Config Read Error",
			fmt.Sprintf("Failed to read Jenkins Pipeline job config for '%s': %s", jobName, err.Error()),
		)
		return
	}

	// Extract Groovy script and description from the XML
	groovyScript, err := extractGroovyScriptFromXML(configXML)
	if err != nil {
		resp.Diagnostics.AddError(
			"Groovy Script Extraction Error",
			fmt.Sprintf("Failed to extract Groovy script from job '%s' config: %s", jobName, err.Error()),
		)
		// Continue even if extraction fails, to at least set other known values.
		groovyScript = "" // Set to empty string to avoid nil pointer
	}

	description, err := extractDescriptionFromXML(configXML)
	if err != nil {
		resp.Diagnostics.AddError(
			"Description Extraction Error",
			fmt.Sprintf("Failed to extract description from job '%s' config: %s", jobName, err.Error()),
		)
		description = ""
	}

	// Update the state with the actual data from Jenkins
	state.Name = types.StringValue(jobName) // Ensure name is consistent
	state.Description = types.StringValue(description)
	state.GroovyScript = types.StringValue(groovyScript)
	state.LastUpdated = types.StringValue(time.Now().Format(time.RFC3339))

	// Set the state in Terraform
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

	log.Printf("[INFO] Jenkins Pipeline job '%s' read successfully.", jobName)
}

// Update an existing Jenkins Pipeline job.
func (r *jenkinsPipelineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan jenkinsPipelineResourceModel
	var state jenkinsPipelineResourceModel

	// Get the plan (desired state) and current state from Terraform
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobName := state.ID.ValueString() // Use ID from state for update target
	newDescription := plan.Description.ValueString()
	newGroovyScript := plan.GroovyScript.ValueString()

	// Construct the updated Jenkins job XML
	updatedConfigXML := buildPipelineConfigXML(newDescription, newGroovyScript)

	// Update the job in Jenkins
	_, err := r.client.UpdateJob(ctx, jobName, updatedConfigXML)
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Update Error",
			fmt.Sprintf("Failed to update Jenkins Pipeline job '%s': %s", jobName, err.Error()),
		)
		return
	}

	// Read back the updated job to ensure consistency and get actual state
	job, err := r.client.GetJob(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Read Error After Update",
			fmt.Sprintf("Failed to read updated Jenkins Pipeline job '%s': %s", jobName, err.Error()),
		)
		return
	}

	// Update the state with the actual state from Jenkins
	state.Description = types.StringValue(job.Raw.Description)
	state.GroovyScript = types.StringValue(newGroovyScript) // Assume script is updated as provided
	state.LastUpdated = types.StringValue(time.Now().Format(time.RFC3339))

	// Set the state in Terraform
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

	log.Printf("[INFO] Jenkins Pipeline job '%s' updated successfully.", jobName)
}

// Delete a Jenkins Pipeline job.
func (r *jenkinsPipelineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state jenkinsPipelineResourceModel

	// Get the current state from Terraform
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobName := state.ID.ValueString()

	// Check if job exists before attempting to delete (idempotency)
	exists, err := r.client.JobExists(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Client Error",
			fmt.Sprintf("Failed to check if job '%s' exists before deletion: %s", jobName, err.Error()),
		)
		return
	}
	if !exists {
		log.Printf("[INFO] Jenkins Pipeline job '%s' not found (already deleted).", jobName)
		return // Job is already gone, nothing to do
	}

	// Delete the job from Jenkins
	_, err = r.client.DeleteJob(ctx, jobName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Jenkins Job Deletion Error",
			fmt.Sprintf("Failed to delete Jenkins Pipeline job '%s': %s", jobName, err.Error()),
		)
		return
	}

	log.Printf("[INFO] Jenkins Pipeline job '%s' deleted successfully.", jobName)
	// Terraform automatically removes the resource from state if no diagnostics are added.
}

// ImportState allows importing existing Jenkins Pipeline jobs into Terraform state.
func (r *jenkinsPipelineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The imported ID is the Jenkins job name.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
