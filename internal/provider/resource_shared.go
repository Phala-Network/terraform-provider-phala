package provider

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---------------------------------------------------------------------------
// Shared schema attributes
// ---------------------------------------------------------------------------

// sharedCVMSchemaAttrs returns schema attributes for the phala_app resource.
// Callers may override individual attributes after merging the returned map
// into their own.
func sharedCVMSchemaAttrs() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"name": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Resource name. Force-new.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"region": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Preferred region identifier. Force-new.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"size": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Instance type (e.g. tdx.small).",
		},
		"image": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "OS image name.",
		},
		"kms": schema.StringAttribute{
			Optional: true,
			Computed: true,
			Default:  stringdefault.StaticString("phala"),
			MarkdownDescription: "KMS type for app provisioning (`phala`, `ethereum`, `base`). " +
				"Changing this forces replacement.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"node_id": schema.Int64Attribute{
			Optional: true,
			MarkdownDescription: "Optional target node (teepod) ID for initial placement. " +
				"Changing this forces replacement.",
			PlanModifiers: []planmodifier.Int64{
				int64planmodifier.RequiresReplace(),
			},
		},
		"custom_app_id": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Optional custom app_id for deterministic identity flow. " +
				"Changing this forces replacement.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"nonce": schema.Int64Attribute{
			Optional: true,
			MarkdownDescription: "Optional nonce paired with custom_app_id for PHALA KMS deterministic app_id flow. " +
				"Changing this forces replacement.",
			PlanModifiers: []planmodifier.Int64{
				int64planmodifier.RequiresReplace(),
			},
		},
		"public_logs": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Expose container logs publicly (compose file setting). Changing this triggers compose update/restart.",
		},
		"public_sysinfo": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Expose system info publicly (compose file setting). Changing this triggers compose update/restart.",
		},
		"public_tcbinfo": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Expose TCB attestation info publicly (compose file setting). Changing this triggers compose update/restart.",
		},
		"gateway_enabled": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Enable public gateway routing (compose file setting). Changing this triggers compose update/restart.",
		},
		"secure_time": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Enable secure time mode (compose file setting). Changing this triggers compose update/restart.",
		},
		"storage_fs": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Storage filesystem for deployment (`zfs` or `ext4`). Immutable after initial deployment.",
			PlanModifiers: []planmodifier.String{
				// Carry the computed value from prior state so it does not go
				// "(known after apply)" on unrelated updates — without this,
				// RequiresReplace fires on every in-place change (e.g. a
				// docker_compose edit), forcing a full app replacement.
				stringplanmodifier.UseStateForUnknown(),
				stringplanmodifier.RequiresReplace(),
			},
		},
		"disk_size": schema.Int64Attribute{
			Optional:            true,
			Computed:            true,
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
			Optional:    true,
			ElementType: types.StringType,
			MarkdownDescription: "Per-deployment SSH public keys injected at launch via user_config. " +
				"Force-new because runtime mutation is not exposed in current public API.",
			PlanModifiers: []planmodifier.List{
				listplanmodifier.RequiresReplace(),
			},
		},
		"env": schema.MapAttribute{
			Optional:    true,
			ElementType: types.StringType,
			MarkdownDescription: "Plaintext env vars. Provider auto-derives env_keys and encrypts values " +
				"before API submission. Plaintext still exists in Terraform state. " +
				"Mark sensitive values at the variable level rather than the schema level " +
				"(see Phala-Network/phala-cloud#246: schema-level Sensitive on a Map " +
				"causes Terraform Core to suppress in-place env diffs).",
		},
		"encrypted_env": schema.StringAttribute{
			Optional:            true,
			Sensitive:           true,
			MarkdownDescription: "Hex-encoded encrypted env payload (manual mode).",
		},
		"env_keys": schema.ListAttribute{
			Optional:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Allowed environment variable keys used with encrypted_env/manual mode.",
		},
		"env_compose_hash": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Optional compose hash for phase-2 encrypted env update flow " +
				"(contract-owned KMS; used with env_transaction_hash).",
		},
		"env_transaction_hash": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Optional on-chain transaction hash for phase-2 encrypted env update flow " +
				"(contract-owned KMS; used with env_compose_hash).",
		},
		"listed": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
			MarkdownDescription: "Whether the resource should be publicly listed. Force-new.",
			PlanModifiers: []planmodifier.Bool{
				boolplanmodifier.RequiresReplace(),
			},
		},
		"wait_for_ready": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(true),
			MarkdownDescription: "Wait until status is running after create/update.",
		},
		"wait_timeout_seconds": schema.Int64Attribute{
			Optional:            true,
			Computed:            true,
			Default:             int64default.StaticInt64(600),
			MarkdownDescription: "Wait timeout for async operations.",
		},
		"status": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Current CVM status.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"endpoint": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Primary public endpoint URL.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Shared compose file / provision request builders
// ---------------------------------------------------------------------------

// composeFileFields holds the inputs needed to build a compose_file map for
// the /cvms/provision API.
type composeFileFields struct {
	Name            string
	DockerCompose   string
	PreLaunchScript types.String
	PublicLogs      types.Bool
	PublicSysinfo   types.Bool
	PublicTCBInfo   types.Bool
	GatewayEnabled  types.Bool
	SecureTime      types.Bool
	StorageFS       types.String
	EnvKeys         []string
	HasEnvKeys      bool
}

func buildComposeFile(f composeFileFields) map[string]any {
	cf := map[string]any{
		"name":                f.Name,
		"docker_compose_file": f.DockerCompose,
	}
	if !f.PreLaunchScript.IsNull() && !f.PreLaunchScript.IsUnknown() {
		cf["pre_launch_script"] = f.PreLaunchScript.ValueString()
	}
	if !f.PublicLogs.IsNull() && !f.PublicLogs.IsUnknown() {
		cf["public_logs"] = f.PublicLogs.ValueBool()
	}
	if !f.PublicSysinfo.IsNull() && !f.PublicSysinfo.IsUnknown() {
		cf["public_sysinfo"] = f.PublicSysinfo.ValueBool()
	}
	if !f.PublicTCBInfo.IsNull() && !f.PublicTCBInfo.IsUnknown() {
		cf["public_tcbinfo"] = f.PublicTCBInfo.ValueBool()
	}
	if !f.GatewayEnabled.IsNull() && !f.GatewayEnabled.IsUnknown() {
		cf["gateway_enabled"] = f.GatewayEnabled.ValueBool()
	}
	if !f.SecureTime.IsNull() && !f.SecureTime.IsUnknown() {
		cf["secure_time"] = f.SecureTime.ValueBool()
	}
	if !f.StorageFS.IsNull() && !f.StorageFS.IsUnknown() {
		cf["storage_fs"] = f.StorageFS.ValueString()
	}
	if f.HasEnvKeys || len(f.EnvKeys) > 0 {
		cf["allowed_envs"] = f.EnvKeys
	}
	return cf
}

func buildComposeFileUpdateRequest(f composeFileFields, updateEnvVars bool) map[string]any {
	req := buildComposeFile(f)
	if updateEnvVars {
		req["update_env_vars"] = true
	}
	return req
}

// provisionFields holds the inputs needed to build a /cvms/provision request.
type provisionFields struct {
	Name              string
	Size              string
	ComposeFile       map[string]any
	KMS               string
	Listed            bool
	Region            types.String
	NodeID            int64
	HasNodeID         bool
	Image             types.String
	CustomAppID       string
	HasCustomAppID    bool
	Nonce             int64
	HasNonce          bool
	DiskSize          types.Int64
	SSHAuthorizedKeys []string
}

func buildProvisionReq(f provisionFields) (*phala.ProvisionCVMRequest, error) {
	kms := kmsPayloadValue(f.KMS)
	listed := f.Listed
	req := &phala.ProvisionCVMRequest{
		Name:         f.Name,
		InstanceType: f.Size,
		KMSType:      &kms,
		Listed:       &listed,
	}

	// Build ComposeFile from the map representation.
	if f.ComposeFile != nil {
		cf := &phala.ComposeFile{}
		if v, ok := f.ComposeFile["name"].(string); ok {
			cf.Name = v
		}
		if v, ok := f.ComposeFile["docker_compose_file"].(string); ok {
			cf.DockerComposeFile = v
		}
		if v, ok := f.ComposeFile["pre_launch_script"].(string); ok {
			cf.PreLaunchScript = &v
		}
		if v, ok := f.ComposeFile["gateway_enabled"].(bool); ok {
			cf.GatewayEnabled = &v
		}
		if v, ok := f.ComposeFile["encrypted_env"].(string); ok {
			cf.EncryptedEnv = &v
		}
		if keys, ok := f.ComposeFile["allowed_envs"].([]string); ok && len(keys) > 0 {
			cf.AllowedEnvs = keys
		}
		if v, ok := f.ComposeFile["public_logs"].(bool); ok {
			cf.PublicLogs = &v
		}
		if v, ok := f.ComposeFile["public_sysinfo"].(bool); ok {
			cf.PublicSysinfo = &v
		}
		if v, ok := f.ComposeFile["public_tcbinfo"].(bool); ok {
			cf.PublicTcbinfo = &v
		}
		if v, ok := f.ComposeFile["secure_time"].(bool); ok {
			cf.SecureTime = &v
		}
		if v, ok := f.ComposeFile["storage_fs"].(string); ok && v != "" {
			cf.StorageFS = &v
		}
		req.ComposeFile = cf
	}

	if !f.Region.IsNull() && !f.Region.IsUnknown() {
		region := f.Region.ValueString()
		req.Region = &region
	}
	if f.HasNodeID {
		teepodID := int(f.NodeID)
		req.TeepodID = &teepodID
	}
	if !f.Image.IsNull() && !f.Image.IsUnknown() {
		img := f.Image.ValueString()
		req.Image = &img
	}
	if f.HasCustomAppID {
		req.CustomAppID = &f.CustomAppID
	}
	if f.HasNonce {
		req.Nonce = &f.Nonce
	}
	if !f.DiskSize.IsNull() && !f.DiskSize.IsUnknown() {
		ds := int(f.DiskSize.ValueInt64())
		req.DiskSize = &ds
	}
	if len(f.SSHAuthorizedKeys) > 0 {
		req.SSHAuthorizedKeys = f.SSHAuthorizedKeys
	}
	return req, nil
}

// commitCreateResult holds the fields needed from the commit response.
type commitCreateResult struct {
	AppID  string
	VMUUID string
}

// commitAndCreate calls POST /cvms to finalize a provision request.
// Returns the essential fields from the commit response.
func commitAndCreate(
	ctx context.Context,
	client *phala.Client,
	provResp *phala.ProvisionCVMResponse,
	encryptedEnv string,
	hasEncryptedEnv bool,
	envKeys []string,
) (*commitCreateResult, error) {
	commitReq := &phala.CommitCVMProvisionRequest{
		AppID:       provResp.AppID,
		ComposeHash: provResp.ComposeHash,
	}
	if hasEncryptedEnv {
		commitReq.EncryptedEnv = &encryptedEnv
	}
	if len(envKeys) > 0 {
		commitReq.EnvKeys = envKeys
	}

	createResp, err := client.CommitCVMProvision(ctx, commitReq)
	if err != nil {
		return nil, err
	}
	return &commitCreateResult{
		AppID:  provResp.AppID,
		VMUUID: createResp.CvmID(),
	}, nil
}

// ---------------------------------------------------------------------------
// Shared env encryption validation & preparation
// ---------------------------------------------------------------------------

// envConfig holds parsed environment variable configuration shared between
// Create and Update for both resource types.
type envConfig struct {
	EnvVars               map[string]string
	ManualEnvKeys         []string
	ManualEncryptedEnv    string
	HasManualEncrypted    bool
	EnvComposeHash        string
	EnvTransactionHash    string
	HasAutoEnv            bool
	EffectiveEnvKeys      []string
	EffectiveEncrypted    string
	HasEffectiveEncrypted bool
}

// parseEnvConfig extracts and validates environment variable configuration
// from plan attributes. It is shared between resource_app and resource_cvm.
func parseEnvConfig(
	ctx context.Context,
	env types.Map,
	encryptedEnv types.String,
	envKeys types.List,
	envComposeHash types.String,
	envTransactionHash types.String,
	forCreate bool,
) (*envConfig, diag.Diagnostics) {
	var diags diag.Diagnostics

	envVars, d := mapValueAsStrings(ctx, env, "env")
	diags.Append(d...)
	manualKeys, d := listValueAsStrings(ctx, envKeys, "env_keys")
	diags.Append(d...)
	manualEncrypted, hasManualEncrypted, d := knownOptionalString(encryptedEnv, "encrypted_env")
	diags.Append(d...)
	composeHash, _, d := knownOptionalString(envComposeHash, "env_compose_hash")
	diags.Append(d...)
	txHash, _, d := knownOptionalString(envTransactionHash, "env_transaction_hash")
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	if forCreate && (strings.TrimSpace(composeHash) != "" || strings.TrimSpace(txHash) != "") {
		diags.AddError(
			"Invalid create configuration",
			"env_compose_hash and env_transaction_hash are update-only fields.",
		)
		return nil, diags
	}

	hasAutoEnv := !env.IsNull()
	if hasAutoEnv && (hasManualEncrypted || len(manualKeys) > 0) {
		diags.AddError(
			"Conflicting env configuration",
			"Use either env (auto encryption) or encrypted_env/env_keys (manual mode), not both.",
		)
		return nil, diags
	}

	if hasAutoEnv && !forCreate {
		diags.Append(validateEncryptedEnvConfig(true, true, composeHash, txHash)...)
	} else {
		diags.Append(validateEncryptedEnvConfig(hasManualEncrypted, len(manualKeys) > 0, composeHash, txHash)...)
	}
	if diags.HasError() {
		return nil, diags
	}

	if hasManualEncrypted {
		trimmed := strings.TrimSpace(manualEncrypted)
		if trimmed == "" {
			diags.AddError("Invalid encrypted_env", "encrypted_env cannot be empty when set.")
			return nil, diags
		}
		if _, err := hex.DecodeString(trimmed); err != nil {
			diags.AddError("Invalid encrypted_env", fmt.Sprintf("encrypted_env must be a valid hex string: %v", err))
			return nil, diags
		}
	}

	cfg := &envConfig{
		EnvVars:               envVars,
		ManualEnvKeys:         manualKeys,
		ManualEncryptedEnv:    manualEncrypted,
		HasManualEncrypted:    hasManualEncrypted,
		EnvComposeHash:        composeHash,
		EnvTransactionHash:    txHash,
		HasAutoEnv:            hasAutoEnv,
		EffectiveEnvKeys:      manualKeys,
		EffectiveEncrypted:    manualEncrypted,
		HasEffectiveEncrypted: hasManualEncrypted,
	}
	if hasAutoEnv {
		cfg.EffectiveEnvKeys = sortedEnvKeys(envVars)
	}
	return cfg, diags
}

// encryptAutoEnv encrypts the plaintext env vars using the given public key
// and updates the envConfig's effective fields in-place.
func (cfg *envConfig) encryptAutoEnv(pubkey string) error {
	if !cfg.HasAutoEnv {
		return nil
	}
	encrypted, err := encryptEnvMap(cfg.EnvVars, pubkey)
	if err != nil {
		return err
	}
	cfg.EffectiveEncrypted = encrypted
	cfg.HasEffectiveEncrypted = true
	return nil
}

// buildEnvUpdateReq constructs the payload for PATCH /cvms/{id}/envs.
func (cfg *envConfig) buildEnvUpdateReq(envKeysList types.List) (map[string]any, error) {
	envReq := map[string]any{}
	if cfg.HasAutoEnv {
		envReq["encrypted_env"] = cfg.EffectiveEncrypted
		envReq["env_keys"] = cfg.EffectiveEnvKeys
	} else {
		if !cfg.HasManualEncrypted {
			return nil, fmt.Errorf("updating env_keys or phase-2 fields requires encrypted_env to be set")
		}
		envReq["encrypted_env"] = cfg.ManualEncryptedEnv
		if !envKeysList.IsNull() && !envKeysList.IsUnknown() {
			envReq["env_keys"] = cfg.ManualEnvKeys
		}
	}
	if strings.TrimSpace(cfg.EnvComposeHash) != "" {
		envReq["compose_hash"] = cfg.EnvComposeHash
	}
	if strings.TrimSpace(cfg.EnvTransactionHash) != "" {
		envReq["transaction_hash"] = cfg.EnvTransactionHash
	}
	return envReq, nil
}

// ---------------------------------------------------------------------------
// Shared compose settings change detection
// ---------------------------------------------------------------------------

// composeSettingsValues bundles the bool fields that control compose runtime
// settings, used for change detection between plan and state.
type composeSettingsValues struct {
	PublicLogs     types.Bool
	PublicSysinfo  types.Bool
	PublicTCBInfo  types.Bool
	GatewayEnabled types.Bool
	SecureTime     types.Bool
}

func (v composeSettingsValues) changed(other composeSettingsValues) bool {
	return !v.PublicLogs.Equal(other.PublicLogs) ||
		!v.PublicSysinfo.Equal(other.PublicSysinfo) ||
		!v.PublicTCBInfo.Equal(other.PublicTCBInfo) ||
		!v.GatewayEnabled.Equal(other.GatewayEnabled) ||
		!v.SecureTime.Equal(other.SecureTime)
}

// ---------------------------------------------------------------------------
// Shared polling with jitter
// ---------------------------------------------------------------------------

// pollInterval returns a base duration with added jitter (±25%) to avoid
// thundering-herd effects when multiple resources poll concurrently.
func pollInterval(base time.Duration) time.Duration {
	jitter := time.Duration(rand.Int64N(int64(base) / 2))
	return base - base/4 + jitter
}

// ---------------------------------------------------------------------------
// Shared plan-to-state fallback helpers
// ---------------------------------------------------------------------------

// inheritOptionalString copies the state value into plan when plan is
// null/unknown. Used for optional+computed fields in Update.
func inheritOptionalString(plan, state types.String) types.String {
	if plan.IsNull() || plan.IsUnknown() {
		return state
	}
	return plan
}

// inheritOptionalBool copies the state value into plan when plan is
// null/unknown.
func inheritOptionalBool(plan, state types.Bool) types.Bool {
	if plan.IsNull() || plan.IsUnknown() {
		return state
	}
	return plan
}

// diskSizeValidation checks whether a disk_size update is valid (can only
// grow). Returns changed bool and any diagnostics.
func diskSizeValidation(plan, state types.Int64) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	changed := !plan.IsNull() && !plan.IsUnknown() && !plan.Equal(state)
	if changed &&
		!state.IsNull() && !state.IsUnknown() &&
		plan.ValueInt64() < state.ValueInt64() {
		diags.AddError(
			"Invalid disk_size update",
			fmt.Sprintf("disk_size can only grow (current=%d, requested=%d).",
				state.ValueInt64(), plan.ValueInt64()),
		)
	}
	return changed, diags
}
