package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &appResource{}
var _ resource.ResourceWithImportState = &appResource{}
var _ resource.ResourceWithValidateConfig = &appResource{}
var _ resource.ResourceWithModifyPlan = &appResource{}

type appResource struct {
	client *APIClient
}

type appResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	AppID              types.String `tfsdk:"app_id"`
	Name               types.String `tfsdk:"name"`
	Region             types.String `tfsdk:"region"`
	Size               types.String `tfsdk:"size"`
	Image              types.String `tfsdk:"image"`
	KMS                types.String `tfsdk:"kms"`
	NodeID             types.Int64  `tfsdk:"node_id"`
	CustomAppID        types.String `tfsdk:"custom_app_id"`
	Nonce              types.Int64  `tfsdk:"nonce"`
	PublicLogs         types.Bool   `tfsdk:"public_logs"`
	PublicSysinfo      types.Bool   `tfsdk:"public_sysinfo"`
	PublicTCBInfo      types.Bool   `tfsdk:"public_tcbinfo"`
	GatewayEnabled     types.Bool   `tfsdk:"gateway_enabled"`
	SecureTime         types.Bool   `tfsdk:"secure_time"`
	StorageFS          types.String `tfsdk:"storage_fs"`
	DiskSize           types.Int64  `tfsdk:"disk_size"`
	DockerCompose      types.String `tfsdk:"docker_compose"`
	PreLaunchScript    types.String `tfsdk:"pre_launch_script"`
	SSHAuthorizedKeys  types.List   `tfsdk:"ssh_authorized_keys"`
	Env                types.Map    `tfsdk:"env"`
	EncryptedEnv       types.String `tfsdk:"encrypted_env"`
	EnvKeys            types.List   `tfsdk:"env_keys"`
	EnvComposeHash     types.String `tfsdk:"env_compose_hash"`
	EnvTransactionHash types.String `tfsdk:"env_transaction_hash"`
	Listed             types.Bool   `tfsdk:"listed"`
	WaitForReady       types.Bool   `tfsdk:"wait_for_ready"`
	WaitTimeoutSecond  types.Int64  `tfsdk:"wait_timeout_seconds"`
	Status             types.String `tfsdk:"status"`
	PrimaryCVMID       types.String `tfsdk:"primary_cvm_id"`
	CVMIDs             types.List   `tfsdk:"cvm_ids"`
	Instances          types.List   `tfsdk:"instances"`
	Endpoint           types.String `tfsdk:"endpoint"`
	GatewayBaseDomain  types.String `tfsdk:"gateway_base_domain"`
	GatewayCname       types.String `tfsdk:"gateway_cname"`
	Members            types.List   `tfsdk:"members"`
}

type appInstanceModel struct {
	ID                types.String `tfsdk:"id"`
	AppID             types.String `tfsdk:"app_id"`
	Name              types.String `tfsdk:"name"`
	VMUUID            types.String `tfsdk:"vm_uuid"`
	InstanceID        types.String `tfsdk:"instance_id"`
	Status            types.String `tfsdk:"status"`
	Region            types.String `tfsdk:"region"`
	InstanceType      types.String `tfsdk:"instance_type"`
	Endpoint          types.String `tfsdk:"endpoint"`
	GatewayBaseDomain types.String `tfsdk:"gateway_base_domain"`
	GatewayCname      types.String `tfsdk:"gateway_cname"`
	CreatedAt         types.String `tfsdk:"created_at"`
}

func appInstanceAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":                  types.StringType,
		"app_id":              types.StringType,
		"name":                types.StringType,
		"vm_uuid":             types.StringType,
		"instance_id":         types.StringType,
		"status":              types.StringType,
		"region":              types.StringType,
		"instance_type":       types.StringType,
		"endpoint":            types.StringType,
		"gateway_base_domain": types.StringType,
		"gateway_cname":       types.StringType,
		"created_at":          types.StringType,
	}
}

func appInstanceObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: appInstanceAttrTypes()}
}

type appAPIResponse struct {
	ID         json.RawMessage  `json:"id"`
	Name       string           `json:"name"`
	AppID      string           `json:"app_id"`
	CurrentCVM *cvmAPIResponse  `json:"current_cvm"`
	CVMs       []cvmAPIResponse `json:"cvms"`
	CVMCount   *int64           `json:"cvm_count"`
}

type appFetchResult struct {
	App                *appAPIResponse
	CVMs               []cvmAPIResponse
	ReplicaListWarning error
}

func NewAppResource() resource.Resource {
	return &appResource{}
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *appResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := sharedCVMSchemaAttrs()
	// App-specific overrides and additions.
	attrs["id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "Terraform ID (same as app_id).",
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
	attrs["app_id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "Phala app identifier.",
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
	attrs["primary_cvm_id"] = schema.StringAttribute{
		Computed: true,
		MarkdownDescription: "Bootstrap CVM identifier — the CVM created by `phala_app` itself, which " +
			"in members (MIG) mode is also the slot whose name equals `phala_app.name`. " +
			"This is the only CVM that `phala_app` mutates directly.",
	}
	attrs["cvm_ids"] = schema.ListAttribute{
		Computed:            true,
		ElementType:         types.StringType,
		MarkdownDescription: "Identifiers of every CVM currently attached to this app (bootstrap plus any `phala_app_instance`s).",
	}
	attrs["instances"] = schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Computed per-instance view of CVMs currently attached to this app.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id":                  schema.StringAttribute{Computed: true},
				"app_id":              schema.StringAttribute{Computed: true},
				"name":                schema.StringAttribute{Computed: true},
				"vm_uuid":             schema.StringAttribute{Computed: true},
				"instance_id":         schema.StringAttribute{Computed: true},
				"status":              schema.StringAttribute{Computed: true},
				"region":              schema.StringAttribute{Computed: true},
				"instance_type":       schema.StringAttribute{Computed: true},
				"endpoint":            schema.StringAttribute{Computed: true},
				"gateway_base_domain": schema.StringAttribute{Computed: true},
				"gateway_cname":       schema.StringAttribute{Computed: true},
				"created_at":          schema.StringAttribute{Computed: true},
			},
		},
	}
	attrs["gateway_base_domain"] = schema.StringAttribute{
		Computed: true,
		MarkdownDescription: "Phala Cloud gateway base domain serving this app (e.g. " +
			"`dstack-pha-prod5.phala.network`). Compose public URLs as " +
			"`https://<app_id>-<port>.<gateway_base_domain>` " +
			"without having to predict the value. Sourced from the cloud's " +
			"`CVMGatewayInfo.base_domain` on the primary CVM.",
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
	attrs["gateway_cname"] = schema.StringAttribute{
		Computed: true,
		MarkdownDescription: "Operator-configured CNAME alias for this app's gateway, if one has " +
			"been set via the cloud UI. Empty when not configured. Sourced from " +
			"`CVMGatewayInfo.cname` on the primary CVM.",
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
	attrs["members"] = schema.ListAttribute{
		Optional:    true,
		ElementType: types.StringType,
		MarkdownDescription: "Optional list of stable slot names this app's replica set is composed of (MIG-style usage). " +
			"When set, `name` must be one of these values, and `replicas` must be unset or 1 — the " +
			"legacy anonymous-replica path is incompatible with named slots. Downstream " +
			"`phala_app_instance` resources should derive their `for_each` from this attribute so the " +
			"slot list is the single source of truth.",
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Phala Cloud App (app_id + shared compose/env + replica count).",
		Attributes:          attrs,
	}
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	if client, ok := req.ProviderData.(*APIClient); ok {
		r.client = client
	}
}

// ValidateConfig surfaces the MIG-mode invariants at plan time so users see
// errors before any cloud API calls are made.
func (r *appResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg appResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateMembersAndName(ctx, cfg)...)
}

// ModifyPlan blocks mutable cloud-side updates to phala_app in members
// (MIG) mode. The provider's existing app-update path mutates one CVM at
// a time, and the legacy fan-out / scale-down logic is intentionally
// disabled in members mode (it would silently delete a named slot). So
// until the cloud exposes an app-revision-aware update endpoint that
// preserves named slot identity, the safe answer is: refuse the plan
// rather than apply it half-way.
//
// We read individual attributes (not the whole struct) so this works on
// fresh-Create plans too, where Computed nested fields are still Unknown
// and would fail whole-struct deserialization.
func (r *appResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Skip on create (no prior state) and destroy (no plan).
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	stateMembers, ok := planMembersFromAttribute(ctx, req.State, &resp.Diagnostics)
	if !ok {
		return
	}
	planMembers, ok := planMembersFromAttribute(ctx, req.Plan, &resp.Diagnostics)
	if !ok {
		return
	}

	stateHasMembers := membersListSet(stateMembers)
	planHasMembers := membersListSet(planMembers)

	// Block removing the members attribute via in-place update. The CVMs
	// owned by phala_app_instance resources remain on the cloud, but
	// phala_app would revert to the single-CVM update model, and the
	// next apply would mutate only the bootstrap slot — leaving the
	// orphaned phala_app_instance slots on the old revision. Require
	// destroy + recreate for this transition.
	if stateHasMembers && !planHasMembers {
		resp.Diagnostics.AddAttributeError(
			path.Root("members"),
			"Cannot leave members mode in-place",
			"This phala_app was previously declared with `members` (MIG mode). "+
				"Removing the attribute in place is not supported because the cloud-side "+
				"CVMs created via phala_app_instance would be orphaned and the next "+
				"update would only touch the bootstrap slot. Destroy and recreate the "+
				"app instead.",
		)
	}
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateMembersAndName(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	identity, diags := resolveProvisionIdentity(plan.KMS, plan.CustomAppID, plan.Nonce)
	resp.Diagnostics.Append(diags...)
	nodeID, hasNodeID, diags := knownOptionalInt64(plan.NodeID, "node_id")
	resp.Diagnostics.Append(diags...)
	if hasNodeID && nodeID <= 0 {
		resp.Diagnostics.AddError("Invalid node_id", "node_id must be greater than 0.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	sshAuthorizedKeys, diags := listValueAsStrings(ctx, plan.SSHAuthorizedKeys, "ssh_authorized_keys")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	envCfg, envDiags := parseEnvConfig(ctx, plan.Env, plan.EncryptedEnv, plan.EnvKeys, plan.EnvComposeHash, plan.EnvTransactionHash, true)
	resp.Diagnostics.Append(envDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	composeFile := buildComposeFile(composeFileFields{
		Name:            plan.Name.ValueString(),
		DockerCompose:   plan.DockerCompose.ValueString(),
		PreLaunchScript: plan.PreLaunchScript,
		PublicLogs:      plan.PublicLogs,
		PublicSysinfo:   plan.PublicSysinfo,
		PublicTCBInfo:   plan.PublicTCBInfo,
		GatewayEnabled:  plan.GatewayEnabled,
		SecureTime:      plan.SecureTime,
		StorageFS:       plan.StorageFS,
		EnvKeys:         envCfg.EffectiveEnvKeys,
		HasEnvKeys:      len(envCfg.EffectiveEnvKeys) > 0,
	})

	provReq, err := buildProvisionReq(provisionFields{
		Name:              plan.Name.ValueString(),
		Size:              plan.Size.ValueString(),
		ComposeFile:       composeFile,
		KMS:               identity.KMSType,
		Listed:            plan.Listed.ValueBool(),
		Region:            plan.Region,
		NodeID:            nodeID,
		HasNodeID:         hasNodeID,
		Image:             plan.Image,
		CustomAppID:       identity.CustomAppID,
		HasCustomAppID:    identity.HasCustomAppID,
		Nonce:             identity.Nonce,
		HasNonce:          identity.HasNonce,
		DiskSize:          plan.DiskSize,
		SSHAuthorizedKeys: sshAuthorizedKeys,
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid provision parameters", err.Error())
		return
	}

	var provResp provisionResponse
	if err := r.client.PostJSON(ctx, "/cvms/provision", provReq, &provResp); err != nil {
		resp.Diagnostics.AddError("Failed to provision app", err.Error())
		return
	}
	if provResp.ComposeHash == "" {
		resp.Diagnostics.AddError("Invalid provision response", "compose_hash was empty.")
		return
	}
	if strings.TrimSpace(provResp.AppID) == "" {
		resp.Diagnostics.AddError(
			"Unsupported KMS flow",
			"Provision did not return app_id. This usually means onchain KMS flow is required and is not yet supported by this provider.",
		)
		return
	}
	if envCfg.HasAutoEnv {
		if strings.TrimSpace(provResp.AppEnvEncryptPubkey) == "" {
			resp.Diagnostics.AddError(
				"Missing encryption public key",
				"Provision response did not include app_env_encrypt_pubkey required for env auto-encryption.",
			)
			return
		}
		if err := envCfg.encryptAutoEnv(provResp.AppEnvEncryptPubkey); err != nil {
			resp.Diagnostics.AddError("Failed to encrypt env", err.Error())
			return
		}
	}

	createResp, err := commitAndCreate(ctx, r.client, provResp, envCfg.EffectiveEncrypted, envCfg.HasEffectiveEncrypted, envCfg.EffectiveEnvKeys)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create initial app CVM", err.Error())
		return
	}

	appID := ensureAppPrefix(nonEmpty(createResp.AppID, provResp.AppID))
	if strings.TrimSpace(appID) == "" {
		resp.Diagnostics.AddError("Invalid create response", "Missing app_id in create/provision response.")
		return
	}

	if shouldWait(plan.WaitForReady) {
		if err := r.waitForAppReady(ctx, appID, 1, waitTimeout(plan.WaitTimeoutSecond)); err != nil {
			resp.Diagnostics.AddError("App did not become ready", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(appID)
	plan.AppID = types.StringValue(appID)

	fetched, err := r.fetchAppAndCVMs(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read app after create", err.Error())
		return
	}
	appendReplicaListWarning(&resp.Diagnostics, fetched.ReplicaListWarning)
	resp.Diagnostics.Append(r.populateState(ctx, &plan, fetched.App, fetched.CVMs)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := appIDFromState(state)
	if appID == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	fetched, err := r.fetchAppAndCVMs(ctx, appID)
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read app", err.Error())
		return
	}
	appendReplicaListWarning(&resp.Diagnostics, fetched.ReplicaListWarning)
	resp.Diagnostics.Append(r.populateState(ctx, &state, fetched.App, fetched.CVMs)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan appResourceModel
	var state appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateMembersAndName(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := appIDFromState(state)
	if appID == "" {
		resp.Diagnostics.AddError("Missing app ID", "Cannot update app without a persisted app_id.")
		return
	}
	plan.Image = inheritOptionalString(plan.Image, state.Image)
	plan.PublicLogs = inheritOptionalBool(plan.PublicLogs, state.PublicLogs)
	plan.PublicSysinfo = inheritOptionalBool(plan.PublicSysinfo, state.PublicSysinfo)
	plan.PublicTCBInfo = inheritOptionalBool(plan.PublicTCBInfo, state.PublicTCBInfo)
	plan.GatewayEnabled = inheritOptionalBool(plan.GatewayEnabled, state.GatewayEnabled)
	plan.SecureTime = inheritOptionalBool(plan.SecureTime, state.SecureTime)
	plan.StorageFS = inheritOptionalString(plan.StorageFS, state.StorageFS)

	desiredImage := plan.Image
	imageChanged := !plan.Image.Equal(state.Image)
	planSettings := composeSettingsValues{plan.PublicLogs, plan.PublicSysinfo, plan.PublicTCBInfo, plan.GatewayEnabled, plan.SecureTime}
	stateSettings := composeSettingsValues{state.PublicLogs, state.PublicSysinfo, state.PublicTCBInfo, state.GatewayEnabled, state.SecureTime}
	settingsChanged := planSettings.changed(stateSettings)

	diskSizeChanged, diskDiags := diskSizeValidation(plan.DiskSize, state.DiskSize)
	resp.Diagnostics.Append(diskDiags...)

	envCfg, envDiags := parseEnvConfig(ctx, plan.Env, plan.EncryptedEnv, plan.EnvKeys, plan.EnvComposeHash, plan.EnvTransactionHash, false)
	resp.Diagnostics.Append(envDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	desiredComposeEnvKeys, desiredComposeEnvKeysKnown, envKeyDiags := composeEnvKeysFromAttrs(ctx, plan.Env, plan.EnvKeys)
	resp.Diagnostics.Append(envKeyDiags...)
	currentComposeEnvKeys, currentComposeEnvKeysKnown, envKeyDiags := composeEnvKeysFromAttrs(ctx, state.Env, state.EnvKeys)
	resp.Diagnostics.Append(envKeyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	composeEnvKeys := currentComposeEnvKeys
	composeHasEnvKeys := currentComposeEnvKeysKnown
	if desiredComposeEnvKeysKnown {
		composeEnvKeys = desiredComposeEnvKeys
		composeHasEnvKeys = true
	}
	updateComposeEnvKeys := desiredComposeEnvKeysKnown && !equalStringSlices(desiredComposeEnvKeys, currentComposeEnvKeys)

	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch current app CVMs", err.Error())
		return
	}
	// In members mode the bootstrap is the slot whose name == phala_app.name;
	// every other CVM is a phala_app_instance slot. In single-CVM mode the
	// bootstrap is the only CVM. Either way it's the right target for the
	// per-CVM provision call and for env-encryption pubkey lookup.
	bootstrapID := selectPrimaryIdentifier(plan.PrimaryCVMID, state.PrimaryCVMID, cvms, plan.Name.ValueString())
	if bootstrapID == "" {
		resp.Diagnostics.AddError("No CVM found for app", "App has no CVMs available for update operations.")
		return
	}

	membersMode := appHasMembers(plan) || appHasMembers(state)
	envChanged := !plan.Env.Equal(state.Env) ||
		!plan.EncryptedEnv.Equal(state.EncryptedEnv) ||
		!plan.EnvKeys.Equal(state.EnvKeys) ||
		!plan.EnvComposeHash.Equal(state.EnvComposeHash) ||
		!plan.EnvTransactionHash.Equal(state.EnvTransactionHash)

	// Encrypt auto-env up-front (once per Update). The app-rooted KMS
	// pubkey is shared across all CVMs in one app, so encrypting against
	// the bootstrap's pubkey produces bytes accepted by every CVM under
	// the same app_id.
	if envChanged && envCfg.HasAutoEnv {
		current, err := r.fetchCVM(ctx, bootstrapID)
		if err != nil {
			resp.Diagnostics.AddError("Failed to load app encryption key", err.Error())
			return
		}
		pubkey := current.envEncryptionPubkey()
		if pubkey == "" {
			resp.Diagnostics.AddError(
				"Missing encryption public key",
				"Bootstrap CVM response did not include encrypted_env_pubkey. Use manual encrypted_env/env_keys mode.",
			)
			return
		}
		if err := envCfg.encryptAutoEnv(pubkey); err != nil {
			resp.Diagnostics.AddError("Failed to encrypt env", err.Error())
			return
		}
	}

	if membersMode {
		// Multi-CVM path: revision-based propagation for compose-body
		// changes, per-CVM fan-out for env values / image / resources.
		if diags := r.applyMembersModeUpdate(ctx, applyMembersModeArgs{
			appID:           appID,
			bootstrapID:     bootstrapID,
			vmUUIDs:         collectVMUUIDs(cvms),
			plan:            plan,
			state:           state,
			envCfg:          envCfg,
			envChanged:      envChanged,
			imageChanged:    imageChanged,
			diskSizeChanged: diskSizeChanged,
			composeEnvKeys:  composeEnvKeys,
			composeHasKeys:  composeHasEnvKeys,
			updateEnvKeys:   updateComposeEnvKeys,
			settingsChanged: settingsChanged,
		}); diags.HasError() {
			resp.Diagnostics.Append(diags...)
			return
		}
	} else {
		// Single-CVM path: PATCH the bootstrap directly for each changed
		// field. No fan-out because there's only one CVM. This is the
		// minimal-API-call route that's worked since pre-0.3.
		if diags := r.applySingleCVMUpdate(ctx, applySingleCVMArgs{
			bootstrapID:     bootstrapID,
			plan:            plan,
			state:           state,
			envCfg:          envCfg,
			envChanged:      envChanged,
			imageChanged:    imageChanged,
			diskSizeChanged: diskSizeChanged,
			composeEnvKeys:  composeEnvKeys,
			composeHasKeys:  composeHasEnvKeys,
			updateEnvKeys:   updateComposeEnvKeys,
			settingsChanged: settingsChanged,
		}); diags.HasError() {
			resp.Diagnostics.Append(diags...)
			return
		}
	}

	if shouldWait(plan.WaitForReady) {
		if err := r.waitForAppReady(ctx, appID, len(cvms), waitTimeout(plan.WaitTimeoutSecond)); err != nil {
			resp.Diagnostics.AddError("App did not become ready", err.Error())
			return
		}
	}

	fetched, err := r.fetchAppAndCVMs(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read app after update", err.Error())
		return
	}
	appendReplicaListWarning(&resp.Diagnostics, fetched.ReplicaListWarning)
	resp.Diagnostics.Append(r.populateState(ctx, &plan, fetched.App, fetched.CVMs)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !shouldWait(plan.WaitForReady) {
		plan.Status = state.Status
		if imageChanged {
			plan.Image = desiredImage
		}
		if settingsChanged {
			plan.PublicLogs = planSettings.PublicLogs
			plan.PublicSysinfo = planSettings.PublicSysinfo
			plan.PublicTCBInfo = planSettings.PublicTCBInfo
			plan.GatewayEnabled = planSettings.GatewayEnabled
			plan.SecureTime = planSettings.SecureTime
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state appResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := appIDFromState(state)
	if appID == "" {
		return
	}

	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Failed to list app CVMs before delete", fmt.Sprintf("Cannot safely delete app without knowing its CVMs: %v. Retry or delete manually.", err))
		return
	}
	for _, cvm := range cvms {
		identifier := selectReplicaIdentifier(cvm)
		if identifier == "" {
			continue
		}
		if err := r.client.Delete(ctx, cvmPath(identifier)); err != nil && !isNotFound(err) {
			resp.Diagnostics.AddError("Failed to delete app replica", err.Error())
			return
		}
	}

	deleteTimeout := waitTimeout(state.WaitTimeoutSecond)
	confirmed, err := r.waitForAppDeletion(ctx, appID, time.Now().Add(deleteTimeout), 2*time.Second)
	if err != nil {
		title := "Delete verification failed"
		if ctx.Err() != nil {
			title = "Delete wait interrupted"
		}
		resp.Diagnostics.AddError(title, err.Error())
		return
	}
	if confirmed {
		return
	}

	resp.Diagnostics.AddError(
		"App deletion not confirmed",
		fmt.Sprintf("Delete requests succeeded but app replicas still exist after %s. Resources may be orphaned — verify manually and run terraform import if needed.", deleteTimeout),
	)
}

func (r *appResource) ImportState(
	ctx context.Context,
	req resource.ImportStateRequest,
	resp *resource.ImportStateResponse,
) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *appResource) fetchAppAndCVMs(ctx context.Context, appID string) (*appFetchResult, error) {
	app := &appAPIResponse{}
	if err := r.client.GetJSON(ctx, appPath(appID), app); err != nil {
		return nil, err
	}
	if strings.TrimSpace(app.AppID) == "" {
		app.AppID = ensureAppPrefix(appID)
	}
	cvms := normalizeCVMInfos(app.CVMs)
	var replicaListWarning error
	if len(cvms) == 0 {
		listed, err := r.fetchAppCVMs(ctx, app.AppID)
		if err != nil {
			if isRetryable(err) {
				replicaListWarning = err
			} else {
				return nil, err
			}
		} else {
			cvms = listed
		}
	}
	return &appFetchResult{
		App:                app,
		CVMs:               cvms,
		ReplicaListWarning: replicaListWarning,
	}, nil
}

func (r *appResource) fetchAppCVMs(ctx context.Context, appID string) ([]cvmAPIResponse, error) {
	var rawItems []map[string]any
	if err := r.client.GetJSON(ctx, appPath(appID)+"/cvms", &rawItems); err != nil {
		return nil, err
	}
	items := make([]cvmAPIResponse, 0, len(rawItems))
	for _, raw := range rawItems {
		items = append(items, normalizeCVMFromAny(raw))
	}
	return normalizeCVMInfos(items), nil
}

func (r *appResource) fetchCVM(ctx context.Context, id string) (*cvmAPIResponse, error) {
	var out cvmAPIResponse
	if err := r.client.GetJSON(ctx, cvmPath(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *appResource) waitForAppDeletion(ctx context.Context, appID string, deadline time.Time, pollBase time.Duration) (bool, error) {
	for time.Now().Before(deadline) {
		refreshed, err := r.fetchAppCVMs(ctx, appID)
		if err != nil {
			if isNotFound(err) {
				return true, nil
			}
			if isRetryable(err) {
				select {
				case <-ctx.Done():
					return false, ctx.Err()
				case <-time.After(pollInterval(pollBase)):
					continue
				}
			}
			return false, err
		}
		if len(refreshed) == 0 {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(pollInterval(pollBase)):
		}
	}
	return false, nil
}

func (r *appResource) waitForAppReady(ctx context.Context, appID string, replicas int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil {
			if isRetryable(err) || isNotFound(err) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollInterval(3 * time.Second)):
					continue
				}
			}
			return err
		}

		if len(cvms) < replicas {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval(3 * time.Second)):
			}
			continue
		}

		if failed, summary := stoppedReplicaFailure(cvms); failed {
			return fmt.Errorf("app %q failed to become ready: %s", appID, summary)
		}

		allRunning := true
		for _, cvm := range cvms {
			if !strings.EqualFold(strings.TrimSpace(cvm.Status), "running") || cvm.inProgress() {
				allRunning = false
				break
			}
		}
		if allRunning {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval(3 * time.Second)):
		}
	}
	return fmt.Errorf("timeout after %s waiting for app %q replicas to become running", timeout, appID)
}

func stoppedReplicaFailure(cvms []cvmAPIResponse) (bool, string) {
	failures := make([]string, 0, len(cvms))
	for _, cvm := range cvms {
		if stablePowerState(cvm.Status) != "stopped" || cvm.inProgress() {
			continue
		}
		failures = append(failures, describeReplicaState(cvm))
	}
	if len(failures) == 0 {
		return false, ""
	}
	sort.Strings(failures)
	return true, "replica entered terminal stopped state: " + strings.Join(failures, ", ")
}

func describeReplicaState(cvm cvmAPIResponse) string {
	id := strings.TrimSpace(cvm.VMUUID)
	if id == "" {
		id = strings.TrimSpace(cvm.InstanceID)
	}
	if id == "" {
		id = cvm.idString()
	}
	if id == "" {
		id = "<unknown>"
	}

	status := strings.TrimSpace(cvm.Status)
	if status == "" {
		status = "unknown"
	}

	if cvm.Progress != nil && strings.TrimSpace(cvm.Progress.Target) != "" {
		return fmt.Sprintf("%s(status=%s,target=%s)", id, status, strings.TrimSpace(cvm.Progress.Target))
	}
	return fmt.Sprintf("%s(status=%s)", id, status)
}

func (r *appResource) populateState(
	ctx context.Context,
	state *appResourceModel,
	app *appAPIResponse,
	cvms []cvmAPIResponse,
) diag.Diagnostics {
	var diags diag.Diagnostics

	if len(cvms) > 0 {
		// Reset computed replica-derived fields only when we have fresh CVM data.
		state.DiskSize = types.Int64Null()
		state.Status = types.StringNull()
		state.Endpoint = types.StringNull()
		state.GatewayBaseDomain = types.StringNull()
		state.GatewayCname = types.StringNull()
		state.PrimaryCVMID = types.StringNull()
		state.Instances = types.ListNull(appInstanceObjectType())
		emptyIDs, listDiags := types.ListValueFrom(ctx, types.StringType, []string{})
		diags.Append(listDiags...)
		if !diags.HasError() {
			state.CVMIDs = emptyIDs
		}
	}

	appID := ensureAppPrefix(nonEmpty(app.AppID, stringFromRawJSON(app.ID), state.ID.ValueString()))
	if appID != "" {
		state.ID = types.StringValue(appID)
		state.AppID = types.StringValue(appID)
	}

	configuredName := ""
	if !state.Name.IsNull() && !state.Name.IsUnknown() {
		configuredName = strings.TrimSpace(state.Name.ValueString())
	}
	if app.Name != "" && configuredName == "" {
		state.Name = types.StringValue(app.Name)
	}

	preferredName := configuredName
	if preferredName == "" && !state.Name.IsNull() && !state.Name.IsUnknown() {
		preferredName = strings.TrimSpace(state.Name.ValueString())
	}
	primary := selectPrimaryCVM(cvms, "", preferredName)
	if primary != nil {
		primaryID := selectReplicaIdentifier(*primary)
		if primaryID != "" && r.client != nil {
			if detailed, err := r.fetchCVM(ctx, primaryID); err == nil && detailed != nil {
				primary = detailed
			}
		}

		if v := primary.instanceType(); v != "" {
			state.Size = types.StringValue(v)
		}
		if primary.DiskSize != nil {
			state.DiskSize = types.Int64Value(*primary.DiskSize)
		}
		if primary.Resource != nil && primary.Resource.DiskInGB != nil {
			state.DiskSize = types.Int64Value(*primary.Resource.DiskInGB)
		}
		if region := primary.region(); region != "" && !state.Region.IsNull() && !state.Region.IsUnknown() {
			state.Region = types.StringValue(region)
		}
		if image := primary.osImageName(); image != "" {
			// The cloud splits the OS image into two response fields
			// (`os.name` + `os.os_image_hash`), but users frequently set
			// `image` to the combined `<name>-<short-hash>` form printed by
			// the `phala images` CLI. Overwriting state with the bare name
			// would trip Terraform Core's post-apply consistency check on
			// every Create with combined-form input. Preserve the user's
			// form whenever it still refers to the same image; only fall
			// back to the bare name when we can't prove a match.
			prior := state.Image.ValueString()
			if !primary.imageMatchesUserForm(prior) {
				state.Image = types.StringValue(image)
			}
		}
		state.PublicLogs = nullableBool(primary.publicLogsValue())
		state.PublicSysinfo = nullableBool(primary.publicSysinfoValue())
		state.PublicTCBInfo = nullableBool(primary.publicTCBInfoValue())
		state.GatewayEnabled = nullableBool(primary.gatewayEnabledValue())
		state.SecureTime = nullableBool(primary.secureTimeValue())
		state.StorageFS = nullableString(primary.storageFSValue())
		state.Status = nullableString(primary.Status)
		state.Endpoint = nullableString(primary.endpoint())
		state.GatewayBaseDomain = nullableString(primary.gatewayBaseDomain())
		state.GatewayCname = nullableString(primary.gatewayCname())
		if primary.Listed != nil {
			state.Listed = types.BoolValue(*primary.Listed)
		}
		if primaryID != "" {
			state.PrimaryCVMID = types.StringValue(primaryID)
			if state.DockerCompose.IsNull() || state.DockerCompose.IsUnknown() {
				compose, err := r.client.GetText(ctx, cvmPath(primaryID)+"/docker-compose.yml")
				if err == nil {
					state.DockerCompose = types.StringValue(normalizeTextBody(compose))
				}
			}
			// The backend injects a default pre-launch script even when the user
			// did not set pre_launch_script. Keep null/unknown state null so an
			// optional unmanaged field does not become provider-managed drift.
			if !state.PreLaunchScript.IsNull() && !state.PreLaunchScript.IsUnknown() {
				preLaunchScript, err := r.client.GetText(ctx, cvmPath(primaryID)+"/pre-launch-script")
				if err == nil {
					state.PreLaunchScript = nullableString(normalizeTextBody(preLaunchScript))
				}
			}
		}
	}

	cvmIDs := make([]string, 0, len(cvms))
	for _, cvm := range cvms {
		if id := selectReplicaIdentifier(cvm); id != "" {
			cvmIDs = append(cvmIDs, id)
		}
	}
	if len(cvms) == 0 {
		// Transient empty response from the cloud (common right after submit)
		// — keep existing CVMIDs/Instances rather than wiping them. The next
		// refresh will reconcile. If state was already empty, leave it empty.
		existingIDs, _ := listValueAsStrings(ctx, state.CVMIDs, "cvm_ids")
		if len(existingIDs) > 0 {
			return diags
		}
		state.Instances = types.ListNull(appInstanceObjectType())
		emptyIDs, listDiags := types.ListValueFrom(ctx, types.StringType, []string{})
		diags.Append(listDiags...)
		if !diags.HasError() {
			state.CVMIDs = emptyIDs
		}
		return diags
	}
	sort.Strings(cvmIDs)
	listValue, listDiags := types.ListValueFrom(ctx, types.StringType, cvmIDs)
	diags.Append(listDiags...)
	if !diags.HasError() {
		state.CVMIDs = listValue
	}
	instancesValue, instanceDiags := buildAppInstances(ctx, cvms)
	diags.Append(instanceDiags...)
	if !diags.HasError() {
		state.Instances = instancesValue
	}

	return diags
}

func appendReplicaListWarning(diags *diag.Diagnostics, err error) {
	if err == nil {
		return
	}

	diags.AddWarning(
		"App replica details temporarily unavailable",
		fmt.Sprintf("Using existing replica-derived state because listing app replicas failed: %v", err),
	)
}

func composeEnvKeysFromAttrs(ctx context.Context, env types.Map, envKeys types.List) ([]string, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	if !env.IsNull() && !env.IsUnknown() {
		envMap, mapDiags := mapValueAsStrings(ctx, env, "env")
		diags.Append(mapDiags...)
		if diags.HasError() {
			return nil, false, diags
		}
		return sortedEnvKeys(envMap), true, diags
	}

	if !envKeys.IsNull() && !envKeys.IsUnknown() {
		keys, listDiags := listValueAsStrings(ctx, envKeys, "env_keys")
		diags.Append(listDiags...)
		if diags.HasError() {
			return nil, false, diags
		}
		sort.Strings(keys)
		return keys, true, diags
	}

	return nil, false, diags
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validateMembersAndName enforces the MIG-mode invariant for phala_app:
// if `members` is set, `name` must be one of `members`. Otherwise the
// bootstrap CVM has nothing to adopt it and becomes an unreferenced extra
// under the app.
//
// Returns Terraform diagnostics; callers should bail if any error is added.
func validateMembersAndName(ctx context.Context, plan appResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if plan.Members.IsNull() || plan.Members.IsUnknown() {
		return diags
	}
	members, listDiags := listValueAsStrings(ctx, plan.Members, "members")
	diags.Append(listDiags...)
	if diags.HasError() {
		return diags
	}
	if len(members) == 0 {
		diags.AddAttributeError(
			path.Root("members"),
			"Invalid members",
			"members must be a non-empty list of slot names when set.",
		)
		return diags
	}

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		diags.AddAttributeError(
			path.Root("name"),
			"Missing name for MIG mode",
			"name must be known at apply time and must equal one of the values in members.",
		)
		return diags
	}
	planName := strings.TrimSpace(plan.Name.ValueString())
	memberSet := make(map[string]struct{}, len(members))
	for _, m := range members {
		memberSet[m] = struct{}{}
	}
	if _, ok := memberSet[planName]; !ok {
		diags.AddAttributeError(
			path.Root("name"),
			"name must be one of members",
			fmt.Sprintf(
				"phala_app.name = %q but is not in members = %v. "+
					"In MIG mode the app's bootstrap CVM must match one of the declared slot names — "+
					"otherwise it becomes an unreferenced extra. Set name to one of the members "+
					"(typically members[0]).",
				planName, members,
			),
		)
	}

	return diags
}

// appHasMembers reports whether the given app model is in MIG (members) mode.
// Null/unknown/empty all mean "no members" — treated as the legacy
// anonymous-replicas path.
func appHasMembers(m appResourceModel) bool {
	if m.Members.IsNull() || m.Members.IsUnknown() {
		return false
	}
	return len(m.Members.Elements()) > 0
}

// membersListSet is the same predicate as appHasMembers but operates on a
// raw types.List so we can use it from ModifyPlan, which reads attributes
// individually to avoid full-model deserialization issues with Computed
// Unknowns at plan time.
func membersListSet(v types.List) bool {
	if v.IsNull() || v.IsUnknown() {
		return false
	}
	return len(v.Elements()) > 0
}

// planMembersFromAttribute pulls just the `members` attribute out of a
// Plan or State without deserializing the whole resource. Returns
// (list, true) on success; on diag error, appends to diags and returns
// (zero, false).
func planMembersFromAttribute(
	ctx context.Context,
	src interface {
		GetAttribute(ctx context.Context, p path.Path, target interface{}) diag.Diagnostics
	},
	diags *diag.Diagnostics,
) (types.List, bool) {
	var v types.List
	diags.Append(src.GetAttribute(ctx, path.Root("members"), &v)...)
	if diags.HasError() {
		return types.ListNull(types.StringType), false
	}
	return v, true
}

func appIDFromState(state appResourceModel) string {
	if !state.AppID.IsNull() && !state.AppID.IsUnknown() && strings.TrimSpace(state.AppID.ValueString()) != "" {
		return ensureAppPrefix(state.AppID.ValueString())
	}
	if !state.ID.IsNull() && !state.ID.IsUnknown() && strings.TrimSpace(state.ID.ValueString()) != "" {
		return ensureAppPrefix(state.ID.ValueString())
	}
	return ""
}

func selectPrimaryIdentifier(planPrimary, statePrimary types.String, cvms []cvmAPIResponse, preferredName string) string {
	if !planPrimary.IsNull() && !planPrimary.IsUnknown() && strings.TrimSpace(planPrimary.ValueString()) != "" {
		return planPrimary.ValueString()
	}
	if !statePrimary.IsNull() && !statePrimary.IsUnknown() && strings.TrimSpace(statePrimary.ValueString()) != "" {
		return statePrimary.ValueString()
	}
	primary := selectPrimaryCVM(cvms, "", preferredName)
	if primary == nil {
		return ""
	}
	return selectReplicaIdentifier(*primary)
}

func selectPrimaryCVM(cvms []cvmAPIResponse, preferredSourceVMUUID string, preferredName string) *cvmAPIResponse {
	if preferredSourceVMUUID != "" {
		for i := range cvms {
			if strings.EqualFold(strings.TrimSpace(cvms[i].VMUUID), strings.TrimSpace(preferredSourceVMUUID)) {
				return &cvms[i]
			}
		}
	}
	if strings.TrimSpace(preferredName) != "" {
		for i := range cvms {
			if strings.EqualFold(strings.TrimSpace(cvms[i].Name), strings.TrimSpace(preferredName)) {
				return &cvms[i]
			}
		}
	}
	for i := range cvms {
		if strings.EqualFold(strings.TrimSpace(cvms[i].Status), "running") {
			return &cvms[i]
		}
	}
	if len(cvms) == 0 {
		return nil
	}
	return &cvms[0]
}

func selectReplicaIdentifier(cvm cvmAPIResponse) string {
	if strings.TrimSpace(cvm.VMUUID) != "" {
		return strings.TrimSpace(cvm.VMUUID)
	}
	if id := cvm.idString(); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	if strings.TrimSpace(cvm.InstanceID) != "" {
		return strings.TrimSpace(cvm.InstanceID)
	}
	return ""
}

func buildAppInstances(ctx context.Context, cvms []cvmAPIResponse) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	ordered := append([]cvmAPIResponse(nil), cvms...)
	sort.SliceStable(ordered, func(i, j int) bool {
		leftCreated := strings.TrimSpace(ordered[i].CreatedAt)
		rightCreated := strings.TrimSpace(ordered[j].CreatedAt)
		if leftCreated != rightCreated {
			if leftCreated == "" {
				return false
			}
			if rightCreated == "" {
				return true
			}
			return leftCreated < rightCreated
		}

		leftID := selectReplicaIdentifier(ordered[i])
		rightID := selectReplicaIdentifier(ordered[j])
		if leftID != rightID {
			return leftID < rightID
		}
		return ordered[i].InstanceID < ordered[j].InstanceID
	})

	out := make([]appInstanceModel, 0, len(ordered))
	for _, cvm := range ordered {
		out = append(out, appInstanceModel{
			ID:                nullableString(selectReplicaIdentifier(cvm)),
			AppID:             nullableString(cvm.AppID),
			Name:              nullableString(cvm.Name),
			VMUUID:            nullableString(cvm.VMUUID),
			InstanceID:        nullableString(cvm.InstanceID),
			Status:            nullableString(cvm.Status),
			Region:            nullableString(cvm.region()),
			InstanceType:      nullableString(cvm.instanceType()),
			Endpoint:          nullableString(cvm.endpoint()),
			GatewayBaseDomain: nullableString(cvm.gatewayBaseDomain()),
			GatewayCname:      nullableString(cvm.gatewayCname()),
			CreatedAt:         nullableString(cvm.CreatedAt),
		})
	}
	value, valueDiags := types.ListValueFrom(ctx, appInstanceObjectType(), out)
	diags.Append(valueDiags...)
	if diags.HasError() {
		return types.ListNull(appInstanceObjectType()), diags
	}
	return value, diags
}

func normalizeCVMInfos(cvms []cvmAPIResponse) []cvmAPIResponse {
	out := make([]cvmAPIResponse, 0, len(cvms))
	for _, cvm := range cvms {
		if strings.TrimSpace(cvm.AppID) == "" &&
			strings.TrimSpace(cvm.VMUUID) == "" &&
			strings.TrimSpace(cvm.Name) == "" &&
			strings.TrimSpace(cvm.Status) == "" &&
			len(cvm.ID) == 0 {
			continue
		}
		out = append(out, cvm)
	}
	return out
}

func normalizeCVMFromAny(raw map[string]any) cvmAPIResponse {
	b, err := json.Marshal(raw)
	if err != nil {
		return cvmAPIResponse{}
	}
	var out cvmAPIResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return cvmAPIResponse{}
	}
	out.AppID = ensureAppPrefix(out.AppID)

	// The API sometimes nests CVM fields inside a "hosted" sub-object.
	// Unmarshal it separately and use as fallback for any empty top-level fields.
	hostedRaw, ok := raw["hosted"].(map[string]any)
	if !ok {
		return out
	}
	hb, err := json.Marshal(hostedRaw)
	if err != nil {
		return out
	}
	var hosted cvmAPIResponse
	if err := json.Unmarshal(hb, &hosted); err != nil {
		return out
	}

	if out.Name == "" {
		out.Name = hosted.Name
	}
	if out.Status == "" {
		out.Status = hosted.Status
	}
	if out.AppID == "" {
		out.AppID = ensureAppPrefix(hosted.AppID)
	}
	if out.InstanceID == "" {
		out.InstanceID = hosted.InstanceID
	}
	if out.VMUUID == "" {
		out.VMUUID = hosted.idString()
	}
	if out.BaseImage == "" {
		out.BaseImage = hosted.BaseImage
	}
	if out.StorageFS == "" {
		out.StorageFS = hosted.StorageFS
	}
	if out.PublicLogs == nil {
		out.PublicLogs = hosted.PublicLogs
	}
	if out.PublicSysinfo == nil {
		out.PublicSysinfo = hosted.PublicSysinfo
	}
	if out.PublicTCBInfo == nil {
		out.PublicTCBInfo = hosted.PublicTCBInfo
	}
	if out.GatewayEnabled == nil {
		out.GatewayEnabled = hosted.GatewayEnabled
	}
	if out.SecureTime == nil {
		out.SecureTime = hosted.SecureTime
	}
	if len(out.ID) == 0 {
		out.ID = hosted.ID
	}
	if appURL := stringFromAny(hostedRaw["app_url"]); appURL != "" && len(out.Endpoints) == 0 {
		out.Endpoints = append(out.Endpoints, struct {
			App string `json:"app"`
		}{App: appURL})
	}

	return out
}

func appPath(id string) string {
	trimmed := strings.TrimSpace(id)
	if strings.HasPrefix(trimmed, "app_") {
		trimmed = strings.TrimPrefix(trimmed, "app_")
	}
	return "/apps/" + url.PathEscape(trimmed)
}

func stringFromRawJSON(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func stringFromAny(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return ""
	}
}
