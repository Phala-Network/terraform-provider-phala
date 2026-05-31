package provider

import (
	"context"
	"encoding/json"
	"strings"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &appPreflightDataSource{}

type appPreflightDataSource struct {
	client *phala.Client
}

type appPreflightDataSourceModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Region          types.String `tfsdk:"region"`
	Size            types.String `tfsdk:"size"`
	Image           types.String `tfsdk:"image"`
	KMS             types.String `tfsdk:"kms"`
	NodeID          types.Int64  `tfsdk:"node_id"`
	CustomAppID     types.String `tfsdk:"custom_app_id"`
	Nonce           types.Int64  `tfsdk:"nonce"`
	PublicLogs      types.Bool   `tfsdk:"public_logs"`
	PublicSysinfo   types.Bool   `tfsdk:"public_sysinfo"`
	PublicTCBInfo   types.Bool   `tfsdk:"public_tcbinfo"`
	GatewayEnabled  types.Bool   `tfsdk:"gateway_enabled"`
	SecureTime      types.Bool   `tfsdk:"secure_time"`
	StorageFS       types.String `tfsdk:"storage_fs"`
	DiskSize        types.Int64  `tfsdk:"disk_size"`
	DockerCompose   types.String `tfsdk:"docker_compose"`
	PreLaunchScript types.String `tfsdk:"pre_launch_script"`
	Env             types.Map    `tfsdk:"env"`
	EnvKeys         types.List   `tfsdk:"env_keys"`
	Listed          types.Bool   `tfsdk:"listed"`

	AppID               types.String `tfsdk:"app_id"`
	ComposeHash         types.String `tfsdk:"compose_hash"`
	AppEnvEncryptPubkey types.String `tfsdk:"app_env_encrypt_pubkey"`
	KMSInfoJSON         types.String `tfsdk:"kms_info_json"`
	FMSPC               types.String `tfsdk:"fmspc"`
	DeviceID            types.String `tfsdk:"device_id"`
	OSImageHash         types.String `tfsdk:"os_image_hash"`
	InstanceType        types.String `tfsdk:"instance_type"`
	NodeIDOut           types.Int64  `tfsdk:"matched_node_id"`
	KMSID               types.String `tfsdk:"kms_id"`
	RawJSON             types.String `tfsdk:"raw_json"`
}

func NewAppPreflightDataSource() datasource.DataSource {
	return &appPreflightDataSource{}
}

func (d *appPreflightDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_preflight"
}

func (d *appPreflightDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs Phala Cloud app provision/preflight and returns the compose hash without committing a CVM deployment.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable data source ID (same as compose_hash).",
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
			},
			"compose_hash": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "SHA-256 hash of the normalized app compose file returned by Phala Cloud preflight.",
			},
			"app_env_encrypt_pubkey": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Public key used for app environment encryption.",
			},
			"kms_info_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Raw KMS info object as JSON.",
			},
			"fmspc": schema.StringAttribute{
				Computed: true,
			},
			"device_id": schema.StringAttribute{
				Computed: true,
			},
			"os_image_hash": schema.StringAttribute{
				Computed: true,
			},
			"instance_type": schema.StringAttribute{
				Computed: true,
			},
			"matched_node_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Matched teepod/node ID returned by preflight, when present.",
			},
			"kms_id": schema.StringAttribute{
				Computed: true,
			},
			"raw_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Full provision response as JSON.",
			},
		},
	}
}

func (d *appPreflightDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*phala.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			"Expected *phala.Client while configuring app preflight data source.",
		)
		return
	}

	d.client = client
}

func (d *appPreflightDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config appPreflightDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Name.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Unknown name", "name must be known at plan time.")
	}
	if config.Size.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("size"), "Unknown size", "size must be known at plan time.")
	}
	if config.DockerCompose.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("docker_compose"), "Unknown docker_compose", "docker_compose must be known at plan time.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	state, diags := runAppPreflight(ctx, d.client, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func runAppPreflight(
	ctx context.Context,
	client *phala.Client,
	config appPreflightDataSourceModel,
) (appPreflightDataSourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	provReq, diags := buildAppPreflightProvisionReq(ctx, config)
	if diags.HasError() {
		return config, diags
	}

	provisionResp, err := client.ProvisionCVM(ctx, provReq)
	if err != nil {
		summary, detail := diagnosticFromAPIError("Failed to provision app preflight", err)
		diags.AddError(summary, detail)
		return config, diags
	}
	if strings.TrimSpace(provisionResp.ComposeHash) == "" {
		diags.AddError("Invalid provision response", "compose_hash was empty.")
		return config, diags
	}

	rawJSON, err := json.Marshal(provisionResp)
	if err != nil {
		diags.AddError("Failed to encode provision response", err.Error())
		return config, diags
	}

	var kmsInfoRaw json.RawMessage
	if provisionResp.KMSInfo != nil {
		b, err := json.Marshal(provisionResp.KMSInfo)
		if err != nil {
			diags.AddError("Failed to encode kms_info", err.Error())
			return config, diags
		}
		kmsInfoRaw = b
	}

	state := config
	state.ID = types.StringValue(strings.TrimSpace(provisionResp.ComposeHash))
	state.AppID = nullableString(provisionResp.AppID)
	state.ComposeHash = types.StringValue(strings.TrimSpace(provisionResp.ComposeHash))
	state.AppEnvEncryptPubkey = nullableString(provisionResp.AppEnvEncryptPubkey)
	state.KMSInfoJSON = nullableJSON(kmsInfoRaw)
	state.FMSPC = nullableString(provisionResp.FMSPC)
	state.DeviceID = nullableString(provisionResp.DeviceID)
	state.OSImageHash = nullableString(provisionResp.OSImageHash)
	state.InstanceType = nullableString(provisionResp.InstanceType)
	if provisionResp.NodeID != nil {
		state.NodeIDOut = types.Int64Value(int64(*provisionResp.NodeID))
	} else {
		state.NodeIDOut = types.Int64Null()
	}
	state.KMSID = nullableString(provisionResp.KMSID)
	state.RawJSON = types.StringValue(string(rawJSON))

	return state, diags
}

func buildAppPreflightProvisionReq(ctx context.Context, config appPreflightDataSourceModel) (*phala.ProvisionCVMRequest, diag.Diagnostics) {
	var diags diag.Diagnostics

	identity, identityDiags := resolveProvisionIdentity(config.KMS, config.CustomAppID, config.Nonce)
	diags.Append(identityDiags...)

	nodeID, hasNodeID, nodeDiags := knownOptionalInt64(config.NodeID, "node_id")
	diags.Append(nodeDiags...)
	if hasNodeID && nodeID <= 0 {
		diags.AddError("Invalid node_id", "node_id must be greater than 0.")
	}

	envKeys, hasEnvKeys, envDiags := composeEnvKeysFromAttrs(ctx, config.Env, config.EnvKeys)
	diags.Append(envDiags...)

	listed, listedDiags := boolValueOrDefault(config.Listed, false, "listed")
	diags.Append(listedDiags...)
	if diags.HasError() {
		return nil, diags
	}

	composeFile := buildComposeFile(composeFileFields{
		Name:            config.Name.ValueString(),
		DockerCompose:   config.DockerCompose.ValueString(),
		PreLaunchScript: config.PreLaunchScript,
		PublicLogs:      config.PublicLogs,
		PublicSysinfo:   config.PublicSysinfo,
		PublicTCBInfo:   config.PublicTCBInfo,
		GatewayEnabled:  config.GatewayEnabled,
		SecureTime:      config.SecureTime,
		StorageFS:       config.StorageFS,
		EnvKeys:         envKeys,
		HasEnvKeys:      hasEnvKeys,
	})

	provReq, err := buildProvisionReq(provisionFields{
		Name:           config.Name.ValueString(),
		Size:           config.Size.ValueString(),
		ComposeFile:    composeFile,
		KMS:            identity.KMSType,
		Listed:         listed,
		Region:         config.Region,
		NodeID:         nodeID,
		HasNodeID:      hasNodeID,
		Image:          config.Image,
		CustomAppID:    identity.CustomAppID,
		HasCustomAppID: identity.HasCustomAppID,
		Nonce:          identity.Nonce,
		HasNonce:       identity.HasNonce,
		DiskSize:       config.DiskSize,
	})
	if err != nil {
		diags.AddError("Invalid provision parameters", err.Error())
		return nil, diags
	}
	return provReq, diags
}

func boolValueOrDefault(value types.Bool, fallback bool, fieldName string) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return fallback, diags
	}
	if value.IsUnknown() {
		diags.AddError("Unknown bool value", fieldName+" must be known at apply time.")
		return false, diags
	}
	return value.ValueBool(), diags
}
