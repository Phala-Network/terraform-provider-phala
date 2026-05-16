package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &appPreflightResource{}

type appPreflightResource struct {
	client *APIClient
}

func NewAppPreflightResource() resource.Resource {
	return &appPreflightResource{}
}

func (r *appPreflightResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_preflight"
}

func (r *appPreflightResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs Phala Cloud app provision/preflight and stores the resulting compose hash as Terraform state. No remote object is created.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable resource ID (same as compose_hash).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "App/CVM name included in the app compose.",
			},
			"region": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Preferred region identifier.",
			},
			"size": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Instance type (e.g. tdx.small).",
			},
			"image": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "OS image name.",
			},
			"kms": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "KMS type for app provisioning. Defaults to phala when omitted.",
			},
			"node_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional target node (teepod) ID for placement.",
			},
			"custom_app_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional custom app_id for deterministic identity flow.",
			},
			"nonce": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional nonce paired with custom_app_id for PHALA KMS deterministic app_id flow.",
			},
			"public_logs": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Expose container logs publicly.",
			},
			"public_sysinfo": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Expose system info publicly.",
			},
			"public_tcbinfo": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Expose TCB attestation info publicly.",
			},
			"gateway_enabled": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Enable public gateway routing.",
			},
			"secure_time": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Enable secure time mode.",
			},
			"storage_fs": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Storage filesystem for deployment (`zfs` or `ext4`).",
			},
			"disk_size": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Disk size in GB.",
			},
			"docker_compose": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Docker Compose YAML content.",
			},
			"pre_launch_script": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional pre-launch script content.",
			},
			"ssh_authorized_keys": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Per-deployment SSH public keys injected at launch via user_config.",
			},
			"env": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Plaintext env vars. Only keys enter the app compose allowed_envs list.",
			},
			"env_keys": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Allowed environment variable keys used when env values are not provided.",
			},
			"listed": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the resource should be publicly listed. Defaults to false when omitted.",
			},
			"app_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Preflight app identifier returned by Phala Cloud.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"compose_hash": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "SHA-256 hash of the normalized app compose file returned by Phala Cloud preflight.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"app_env_encrypt_pubkey": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Public key used for app environment encryption.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"kms_info_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Raw KMS info object as JSON.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"fmspc": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"device_id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"os_image_hash": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"instance_type": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"matched_node_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Matched teepod/node ID returned by preflight, when present.",
			},
			"kms_id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"raw_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Full provision response as JSON.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *appPreflightResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			"Expected *APIClient while configuring app preflight resource.",
		)
		return
	}

	r.client = client
}

func (r *appPreflightResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appPreflightDataSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, diags := runAppPreflight(ctx, r.client, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appPreflightResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appPreflightDataSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appPreflightResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan appPreflightDataSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, diags := runAppPreflight(ctx, r.client, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appPreflightResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

func (r *appPreflightResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddAttributeError(
		path.Root("id"),
		"Import is not supported",
		"phala_app_preflight is a generated preflight artifact with no remote object to import.",
	)
}
