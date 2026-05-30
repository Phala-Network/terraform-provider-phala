package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &appInstanceResource{}
var _ resource.ResourceWithImportState = &appInstanceResource{}

// instanceNamePattern mirrors the cloud-side validation
// (POST /apps/{app_id}/instances rejects names that fail this check).
// Cloud rule: 5–63 chars, must start with a letter, allowed chars are
// letters, digits, and hyphens.
var instanceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{4,62}$`)

type appInstanceResource struct {
	client *phala.Client
}

type appInstanceResourceModel struct {
	ID                types.String `tfsdk:"id"`
	AppID             types.String `tfsdk:"app_id"`
	Name              types.String `tfsdk:"name"`
	NodeID            types.Int64  `tfsdk:"node_id"`
	DockerCompose     types.String `tfsdk:"docker_compose"`
	PreLaunchScript   types.String `tfsdk:"pre_launch_script"`
	Env               types.Map    `tfsdk:"env"`
	EncryptedEnv      types.String `tfsdk:"encrypted_env"`
	ComposeHash       types.String `tfsdk:"compose_hash"`
	WaitForReady      types.Bool   `tfsdk:"wait_for_ready"`
	WaitTimeoutSecond types.Int64  `tfsdk:"wait_timeout_seconds"`
	VMUUID            types.String `tfsdk:"vm_uuid"`
	InstanceID        types.String `tfsdk:"instance_id"`
	Status            types.String `tfsdk:"status"`
	Region            types.String `tfsdk:"region"`
	InstanceType      types.String `tfsdk:"instance_type"`
	Endpoint          types.String `tfsdk:"endpoint"`
	GatewayBaseDomain types.String `tfsdk:"gateway_base_domain"`
	GatewayCname      types.String `tfsdk:"gateway_cname"`
	CreatedAt         types.String `tfsdk:"created_at"`
	Managed           types.Bool   `tfsdk:"managed"`
}

func NewAppInstanceResource() resource.Resource {
	return &appInstanceResource{}
}

func (r *appInstanceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_instance"
}

func (r *appInstanceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a single named CVM instance under an existing phala_app. " +
			"`name` is the stable logical member key (e.g. `consul-0`, `worker-3`) — it survives CVM " +
			"replacement and binds the Terraform resource to a durable slot under the app's replica set. " +
			"Backed by `POST /apps/{app_id}/instances` with a custom instance name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Terraform ID. Format: `<app_id>:<name>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"app_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Phala app identifier (replica set) this instance belongs to.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Stable logical member name (5-63 chars, starts with a letter, " +
					"letters/digits/hyphens only). Immutable; renaming forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"node_id": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Optional target node (teepod) ID for placement. " +
					"Changing this forces replacement.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"docker_compose": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Optional override Docker Compose YAML for this instance. " +
					"When omitted, the backend uses the app's template instance. Updated in place on " +
					"the slot's CVM (via `PATCH /cvms/{uuid}/docker-compose`); `vm_uuid` is preserved. " +
					"Only mutable on managed instances; adopted slots reject per-instance overrides.",
			},
			"pre_launch_script": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Optional pre-launch script content. Updated in place on the slot's " +
					"CVM (via `PATCH /cvms/{uuid}/pre-launch-script`); `vm_uuid` is preserved. " +
					"Only mutable on managed instances; adopted slots reject per-instance overrides.",
			},
			"env": schema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				MarkdownDescription: "Plaintext env vars for this instance. Values are encrypted before API submission, " +
					"but plaintext is stored in Terraform state. Updated in place on the slot's CVM " +
					"(via `PATCH /cvms/{uuid}/envs`) — the `vm_uuid` is preserved, mirroring `phala_app.env`. " +
					"The parent app compose must already allow these env keys. Only settable on instances created " +
					"by this resource (`managed = true`); adopted bootstrap slots reject per-instance env.",
			},
			"encrypted_env": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Optional hex-encoded pre-encrypted env payload — the manual " +
					"alternative to `env` (mutually exclusive with it). Updated in place on the slot's " +
					"CVM via the same `PATCH /cvms/{uuid}/envs` as `env`, preserving `vm_uuid`. Only " +
					"mutable on managed instances; adopted slots reject per-instance overrides.",
			},
			"compose_hash": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Content-addressed pointer to an existing compose revision of the parent " +
					"app: deploy the slot from a compose the app already has, without re-uploading the YAML. " +
					"Mutually exclusive with `docker_compose` (the backend rejects both), and must reference a " +
					"revision that belongs to this app. When omitted, the backend uses `docker_compose` (if set) " +
					"or the app's current revision. Selecting a different revision is a new provisioning input, so " +
					"changing it forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"wait_for_ready": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Wait until the new instance reports `running` before returning.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"wait_timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(600),
				MarkdownDescription: "Wait timeout for create / wait-for-ready, in seconds.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"vm_uuid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current CVM UUID occupying this slot. Changes when the CVM is replaced.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"instance_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Runtime/network identity reported by the cloud.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current CVM status.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"region": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Region of the CVM currently occupying this slot.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"instance_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Instance type (e.g. `tdx.small`) of the underlying CVM.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"endpoint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Primary public endpoint URL of the CVM.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"gateway_base_domain": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Default Phala Cloud gateway DNS suffix for this CVM " +
					"(e.g. `dstack-pha-prod5.phala.network`). Downstream callers compose " +
					"per-port URLs as `https://<app_id>-<port>.<gateway_base_domain>` " +
					"without having to predict the suffix.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"gateway_cname": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Operator-configured CNAME alias for the gateway, if set " +
					"via the Phala Cloud UI. Empty when no custom CNAME is configured.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "CVM creation timestamp (ISO-8601).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"managed": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether this resource created the underlying CVM (true) or " +
					"adopted an existing one — typically the bootstrap CVM owned by `phala_app` " +
					"when `phala_app.name` matches this resource's `name` (false). Adopted " +
					"instances skip the API delete call on destroy; the parent `phala_app` owns " +
					"the CVM lifecycle.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *appInstanceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*phala.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			"Expected *phala.Client while configuring app instance resource.",
		)
		return
	}
	r.client = client
}

func (r *appInstanceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appInstanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := ensureAppPrefix(strings.TrimSpace(plan.AppID.ValueString()))
	if appID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("app_id"), "Missing app_id", "app_id must be a non-empty Phala app identifier.")
		return
	}

	name := strings.TrimSpace(plan.Name.ValueString())
	if err := validateInstanceName(name); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", err.Error())
		return
	}

	envCfg, envDiags := parseEnvConfig(
		ctx,
		plan.Env,
		plan.EncryptedEnv,
		types.ListNull(types.StringType),
		types.StringNull(),
		types.StringNull(),
		true,
	)
	resp.Diagnostics.Append(envDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	timeout := waitTimeout(plan.WaitTimeoutSecond)
	deadline := time.Now().Add(timeout)

	// Adopt path: if a CVM with this name already exists under the app, this
	// resource is bound to the bootstrap CVM that phala_app created — we just
	// record it in state without making any new-instance API call. The
	// CVM's lifecycle stays owned by phala_app.
	if existing, ok, err := r.findExistingByName(ctx, appID, name); err != nil {
		resp.Diagnostics.AddError("Failed to look up existing app instance", err.Error())
		return
	} else if ok {
		if envCfg.HasAutoEnv {
			resp.Diagnostics.AddAttributeError(
				path.Root("env"),
				"Cannot apply per-instance env to adopted instance",
				"This phala_app_instance matched an existing CVM owned by phala_app. Set env on the parent phala_app for the bootstrap slot; use phala_app_instance.env only for CVMs created by phala_app_instance.",
			)
			return
		}
		if envCfg.HasManualEncrypted {
			resp.Diagnostics.AddAttributeError(
				path.Root("encrypted_env"),
				"Cannot apply per-instance encrypted_env to adopted instance",
				"This phala_app_instance matched an existing CVM owned by phala_app. Set encrypted_env on the parent phala_app for the bootstrap slot; use phala_app_instance.encrypted_env only for CVMs created by phala_app_instance.",
			)
			return
		}
		merged := existing
		if shouldWait(plan.WaitForReady) {
			ready, err := r.waitForInstanceRunning(ctx, appID, name, deadline)
			if err != nil {
				resp.Diagnostics.AddError("Adopted instance did not become ready", err.Error())
				return
			}
			merged = mergeCVMResponse(existing, ready)
		}
		populateAppInstanceState(&plan, appID, name, merged)
		plan.Managed = types.BoolValue(false)
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}

	body := &phala.CreateAppInstanceRequest{Name: &name}
	if !plan.NodeID.IsNull() && !plan.NodeID.IsUnknown() {
		v := plan.NodeID.ValueInt64()
		if v <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("node_id"), "Invalid node_id", "node_id must be greater than 0.")
			return
		}
		nodeID := int(v)
		body.NodeID = &nodeID
	}
	if !plan.DockerCompose.IsNull() && !plan.DockerCompose.IsUnknown() {
		v := plan.DockerCompose.ValueString()
		body.DockerComposeFile = &v
	}
	if !plan.PreLaunchScript.IsNull() && !plan.PreLaunchScript.IsUnknown() {
		v := plan.PreLaunchScript.ValueString()
		body.PreLaunchScript = &v
	}
	if envCfg.HasAutoEnv {
		pubkey, err := r.loadAppEnvEncryptionPubkey(ctx, appID)
		if err != nil {
			resp.Diagnostics.AddError("Failed to load app encryption key", err.Error())
			return
		}
		if pubkey == "" {
			resp.Diagnostics.AddError("Missing encryption public key", "No CVM under the app returned encrypted_env_pubkey. Use manual encrypted_env mode or wait until the bootstrap CVM exposes the app env encryption key.")
			return
		}
		if err := envCfg.encryptAutoEnv(pubkey); err != nil {
			resp.Diagnostics.AddError("Failed to encrypt env", err.Error())
			return
		}
	}
	if envCfg.HasEffectiveEncrypted {
		v := envCfg.EffectiveEncrypted
		body.EncryptedEnv = &v
	}
	if !plan.ComposeHash.IsNull() && !plan.ComposeHash.IsUnknown() {
		v := plan.ComposeHash.ValueString()
		body.ComposeHash = &v
	}

	created, err := r.client.CreateAppInstance(ctx, appIDWithoutPrefix(appID), body)
	if err != nil {
		// On-chain KMS flows return HTTP 465 with a commit token; surface a clear error
		// rather than a confusing JSON-decode message. The two-phase flow is not yet
		// wired up here because the provider only supports kms = phala today.
		if apiErr, ok := err.(*phala.APIError); ok && apiErr.IsComposePrecondition() {
			resp.Diagnostics.AddError(
				"On-chain KMS not yet supported for phala_app_instance",
				"The cloud API returned HTTP 465 (on-chain commit token required). phala_app_instance "+
					"currently supports only the single-call PHALA KMS flow. Track this gap and use the "+
					"two-phase prepare/commit flow once exposed.",
			)
			return
		}
		summary, detail := diagnosticFromAPIError("Failed to create app instance", err)
		resp.Diagnostics.AddError(summary, detail)
		return
	}

	// The create response carries the freshly-created CVM, but in some flows
	// the response may be partial — confirm by polling the app's CVM list
	// until we find the named replica.
	resolved, err := r.waitForInstance(ctx, appID, name, deadline)
	if err != nil {
		resp.Diagnostics.AddError("Failed to confirm new app instance", err.Error())
		return
	}
	merged := mergeCVMResponse(*created, resolved)

	if shouldWait(plan.WaitForReady) {
		ready, err := r.waitForInstanceRunning(ctx, appID, name, deadline)
		if err != nil {
			resp.Diagnostics.AddError("Instance did not become ready", err.Error())
			return
		}
		merged = mergeCVMResponse(merged, ready)
	}

	populateAppInstanceState(&plan, appID, name, merged)
	plan.Managed = types.BoolValue(true)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *appInstanceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appInstanceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := ensureAppPrefix(strings.TrimSpace(state.AppID.ValueString()))
	name := strings.TrimSpace(state.Name.ValueString())
	if appID == "" || name == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read app instance", err.Error())
		return
	}

	match := findInstanceByName(cvms, name)
	if match == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	populateAppInstanceState(&state, appID, name, *match)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *appInstanceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state appInstanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// env / docker_compose / pre_launch_script update in place on the slot's
	// CVM. Every other field is RequiresReplace, so the framework only routes
	// here when one of those (or the provider-side wait_* knobs) changed. Start
	// from prior state to preserve every computed attribute (vm_uuid, id,
	// gateway_*, created_at, ...) and the managed flag; the per-CVM PATCHes
	// never replace the CVM.
	plan.VMUUID = state.VMUUID
	plan.InstanceID = state.InstanceID
	plan.Status = state.Status
	plan.Region = state.Region
	plan.InstanceType = state.InstanceType
	plan.Endpoint = state.Endpoint
	plan.GatewayBaseDomain = state.GatewayBaseDomain
	plan.GatewayCname = state.GatewayCname
	plan.CreatedAt = state.CreatedAt
	plan.ComposeHash = state.ComposeHash
	plan.Managed = state.Managed

	// `env` (auto) and `encrypted_env` (manual, mutually exclusive with env)
	// both land at PATCH /cvms/{uuid}/envs and update in place. The backend's
	// envs endpoint only requires `encrypted_env`; `env_keys` is optional and,
	// when unchanged, takes the direct-update path — so a bare encrypted_env
	// swap updates in place without replacement. `compose_hash` stays
	// RequiresReplace (it's a content-addressed pointer to a compose/revision,
	// not freely settable alongside docker_compose).
	envChanged := !plan.Env.Equal(state.Env) || !plan.EncryptedEnv.Equal(state.EncryptedEnv)
	composeChanged := !plan.DockerCompose.Equal(state.DockerCompose)
	preLaunchChanged := !plan.PreLaunchScript.Equal(state.PreLaunchScript)

	if !envChanged && !composeChanged && !preLaunchChanged {
		// Only wait_for_ready / wait_timeout_seconds changed — provider-side
		// polling knobs with no cloud effect. Accept into state, no API call.
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}

	// These per-instance overrides are rejected on an adopted (managed=false)
	// slot for the same reason as at Create: the CVM is owned by phala_app,
	// whose own update path should be used. Keep the guard symmetric.
	if !state.Managed.ValueBool() {
		resp.Diagnostics.AddError(
			"Cannot update an adopted instance in place",
			"This phala_app_instance adopted a CVM owned by phala_app (managed = false). "+
				"Update env / docker_compose / pre_launch_script on the parent phala_app for the "+
				"bootstrap slot; per-instance updates are only supported for CVMs created by "+
				"phala_app_instance.",
		)
		return
	}

	appID := ensureAppPrefix(strings.TrimSpace(plan.AppID.ValueString()))
	cvmID := strings.TrimSpace(state.VMUUID.ValueString())
	if cvmID == "" {
		resp.Diagnostics.AddError("Missing CVM identity", "State has no vm_uuid for this instance; cannot target the in-place update.")
		return
	}

	if composeChanged {
		if _, err := r.client.UpdateDockerCompose(ctx, cvmID, plan.DockerCompose.ValueString(), nil); err != nil {
			summary, detail := diagnosticFromAPIError("Failed to update app instance docker compose", err)
			resp.Diagnostics.AddError(summary, detail)
			return
		}
	}

	if preLaunchChanged {
		script := ""
		if !plan.PreLaunchScript.IsNull() && !plan.PreLaunchScript.IsUnknown() {
			script = plan.PreLaunchScript.ValueString()
		}
		if _, err := r.client.UpdatePreLaunchScript(ctx, cvmID, script, nil); err != nil {
			summary, detail := diagnosticFromAPIError("Failed to update app instance pre-launch script", err)
			resp.Diagnostics.AddError(summary, detail)
			return
		}
	}

	if envChanged {
		envCfg, envDiags := parseEnvConfig(
			ctx,
			plan.Env,
			plan.EncryptedEnv,
			types.ListNull(types.StringType),
			types.StringNull(),
			types.StringNull(),
			false,
		)
		resp.Diagnostics.Append(envDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		if envCfg.HasAutoEnv {
			pubkey, err := r.loadAppEnvEncryptionPubkey(ctx, appID)
			if err != nil {
				resp.Diagnostics.AddError("Failed to load app encryption key", err.Error())
				return
			}
			if pubkey == "" {
				resp.Diagnostics.AddError("Missing encryption public key", "No CVM under the app returned encrypted_env_pubkey; cannot encrypt the updated env.")
				return
			}
			if err := envCfg.encryptAutoEnv(pubkey); err != nil {
				resp.Diagnostics.AddError("Failed to encrypt env", err.Error())
				return
			}
		}
		payload, err := envCfg.buildEnvUpdateReq(types.ListNull(types.StringType))
		if err != nil {
			resp.Diagnostics.AddError("Missing encrypted_env", err.Error())
			return
		}
		if _, err := r.client.UpdateCVMEnvs(ctx, cvmID, buildUpdateEnvsRequest(payload)); err != nil {
			if apiErr, ok := err.(*phala.APIError); ok && apiErr.IsComposePrecondition() {
				resp.Diagnostics.AddError(
					"On-chain KMS not yet supported for phala_app_instance",
					"The cloud API returned HTTP 465 (on-chain compose-hash registration required). "+
						"phala_app_instance currently supports only the single-call PHALA KMS flow.",
				)
				return
			}
			summary, detail := diagnosticFromAPIError("Failed to update app instance env", err)
			resp.Diagnostics.AddError(summary, detail)
			return
		}
	}

	// Refresh computed fields (status, etc.) from the now-updated CVM. The
	// PATCHes do not change vm_uuid/name, so identity is preserved. Re-read is
	// best-effort: on failure we keep the carried-over state values.
	name := strings.TrimSpace(plan.Name.ValueString())
	if shouldWait(plan.WaitForReady) {
		deadline := time.Now().Add(waitTimeout(plan.WaitTimeoutSecond))
		if _, err := r.waitForInstanceRunning(ctx, appID, name, deadline); err != nil {
			resp.Diagnostics.AddError("Instance did not return to ready after env update", err.Error())
			return
		}
	}
	if cvm, err := r.fetchCVM(ctx, cvmID); err == nil && cvm != nil {
		refreshed := plan
		populateAppInstanceState(&refreshed, appID, name, *cvm)
		refreshed.Env = plan.Env
		refreshed.EncryptedEnv = plan.EncryptedEnv
		refreshed.Managed = state.Managed
		refreshed.WaitForReady = plan.WaitForReady
		refreshed.WaitTimeoutSecond = plan.WaitTimeoutSecond
		refreshed.NodeID = plan.NodeID
		refreshed.DockerCompose = plan.DockerCompose
		refreshed.PreLaunchScript = plan.PreLaunchScript
		plan = refreshed
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *appInstanceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state appInstanceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := ensureAppPrefix(strings.TrimSpace(state.AppID.ValueString()))
	name := strings.TrimSpace(state.Name.ValueString())
	vmUUID := strings.TrimSpace(state.VMUUID.ValueString())

	// Adopted instances don't own the CVM; the parent phala_app does. Just
	// drop the binding from Terraform state without touching the cloud.
	if !state.Managed.IsNull() && !state.Managed.IsUnknown() && !state.Managed.ValueBool() {
		return
	}

	// Prefer the CVM UUID captured in state. If we don't have one (e.g. partial create),
	// best-effort: look the CVM up by name in the app.
	if vmUUID == "" && appID != "" && name != "" {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil && !isNotFound(err) {
			resp.Diagnostics.AddError("Failed to resolve app instance for delete", err.Error())
			return
		}
		if match := findInstanceByName(cvms, name); match != nil {
			vmUUID = selectReplicaIdentifier(*match)
		}
	}
	if vmUUID == "" {
		// Nothing to delete on the backend; treat as already gone.
		return
	}

	if err := r.client.DeleteCVM(ctx, vmUUID); err != nil && !isNotFound(err) {
		summary, detail := diagnosticFromAPIError("Failed to delete app instance", err)
		resp.Diagnostics.AddError(summary, detail)
		return
	}

	// Best-effort wait for the CVM to disappear from the app's CVM list.
	if appID != "" && name != "" {
		deadline := time.Now().Add(waitTimeout(state.WaitTimeoutSecond))
		_ = r.waitForInstanceDeletion(ctx, appID, name, deadline)
	}
}

func (r *appInstanceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected `<app_id>:<name>`, e.g. `app_abcdef...:consul-0`.",
		)
		return
	}
	appID := ensureAppPrefix(strings.TrimSpace(parts[0]))
	name := strings.TrimSpace(parts[1])

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), appID+":"+name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_id"), appID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	// Default imported instances to managed=true ("destroy on terraform destroy").
	// If you imported the bootstrap CVM owned by phala_app, run
	// `terraform state` to flip this to false so destroying the import
	// doesn't double-delete with phala_app.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("managed"), true)...)
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

// findExistingByName looks up a CVM by name under an existing app. Used to
// detect the bootstrap CVM created by phala_app (when phala_app.name matches
// this resource's name): in that case we adopt the existing CVM instead of
// POSTing a new one. Returns (cvm, true) on match, (zero, false) on no
// match, or an error.
func (r *appInstanceResource) findExistingByName(ctx context.Context, appID, name string) (phala.CVMInfo, bool, error) {
	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil {
		if isNotFound(err) {
			return phala.CVMInfo{}, false, nil
		}
		return phala.CVMInfo{}, false, err
	}
	if match := findInstanceByName(cvms, name); match != nil {
		return *match, true, nil
	}
	return phala.CVMInfo{}, false, nil
}

func (r *appInstanceResource) fetchAppCVMs(ctx context.Context, appID string) ([]phala.CVMInfo, error) {
	rawItems, err := r.client.GetAppCVMs(ctx, appIDWithoutPrefix(appID))
	if err != nil {
		return nil, err
	}
	items := make([]phala.CVMInfo, 0, len(rawItems))
	for i := range rawItems {
		items = append(items, normalizeCVMInfo(rawItems[i]))
	}
	return normalizeCVMInfos(items), nil
}

func (r *appInstanceResource) fetchCVM(ctx context.Context, id string) (*phala.CVMInfo, error) {
	return r.client.GetCVMInfo(ctx, id)
}

func (r *appInstanceResource) loadAppEnvEncryptionPubkey(ctx context.Context, appID string) (string, error) {
	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil {
		return "", err
	}
	for i := range cvms {
		if pubkey := cvmInfoEnvEncryptionPubkey(&cvms[i]); pubkey != "" {
			return pubkey, nil
		}
	}
	for _, cvm := range cvms {
		identifier := selectReplicaIdentifier(cvm)
		if identifier == "" {
			continue
		}
		detail, err := r.fetchCVM(ctx, identifier)
		if err != nil {
			if isNotFound(err) || isRetryable(err) {
				continue
			}
			return "", err
		}
		if pubkey := cvmInfoEnvEncryptionPubkey(detail); pubkey != "" {
			return pubkey, nil
		}
	}
	return "", nil
}

func (r *appInstanceResource) waitForInstance(ctx context.Context, appID, name string, deadline time.Time) (phala.CVMInfo, error) {
	for {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil && !isRetryable(err) && !isNotFound(err) {
			return phala.CVMInfo{}, err
		}
		if match := findInstanceByName(cvms, name); match != nil {
			return *match, nil
		}
		if time.Now().After(deadline) {
			return phala.CVMInfo{}, fmt.Errorf("timed out waiting for app %q instance %q to appear in CVM list", appID, name)
		}
		select {
		case <-ctx.Done():
			return phala.CVMInfo{}, ctx.Err()
		case <-time.After(pollInterval(2 * time.Second)):
		}
	}
}

func (r *appInstanceResource) waitForInstanceRunning(ctx context.Context, appID, name string, deadline time.Time) (phala.CVMInfo, error) {
	for {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil && !isRetryable(err) && !isNotFound(err) {
			return phala.CVMInfo{}, err
		}
		if match := findInstanceByName(cvms, name); match != nil {
			if strings.EqualFold(strings.TrimSpace(match.Status), "running") && !cvmInfoInProgress(match) {
				return *match, nil
			}
			if stablePowerState(match.Status) == "stopped" && !cvmInfoInProgress(match) {
				return phala.CVMInfo{}, fmt.Errorf("instance %q entered terminal stopped state: %s", name, describeReplicaState(match))
			}
		}
		if time.Now().After(deadline) {
			return phala.CVMInfo{}, fmt.Errorf("timed out waiting for app %q instance %q to become running", appID, name)
		}
		select {
		case <-ctx.Done():
			return phala.CVMInfo{}, ctx.Err()
		case <-time.After(pollInterval(3 * time.Second)):
		}
	}
}

func (r *appInstanceResource) waitForInstanceDeletion(ctx context.Context, appID, name string, deadline time.Time) error {
	for {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			if !isRetryable(err) {
				return err
			}
		}
		if findInstanceByName(cvms, name) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for app %q instance %q to disappear", appID, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval(2 * time.Second)):
		}
	}
}

func findInstanceByName(cvms []phala.CVMInfo, name string) *phala.CVMInfo {
	target := strings.TrimSpace(name)
	if target == "" {
		return nil
	}
	for i := range cvms {
		if strings.EqualFold(strings.TrimSpace(cvms[i].Name), target) {
			return &cvms[i]
		}
	}
	return nil
}

// mergeCVMResponse fills empty fields in `base` from `extra` so we can combine
// the eager response from POST /apps/{id}/instances with the steady-state
// row returned later by GET /apps/{id}/cvms.
func mergeCVMResponse(base, extra phala.CVMInfo) phala.CVMInfo {
	if base.Name == "" {
		base.Name = extra.Name
	}
	if extra.Status != "" {
		base.Status = extra.Status
	}
	base.InProgress = extra.InProgress
	base.Progress = extra.Progress
	if cvmInfoAppID(&base) == "" && cvmInfoAppID(&extra) != "" {
		base.AppID = extra.AppID
	}
	if cvmInfoVMUUID(&base) == "" && cvmInfoVMUUID(&extra) != "" {
		base.VMUUID = extra.VMUUID
	}
	if cvmInfoInstanceID(&base) == "" && cvmInfoInstanceID(&extra) != "" {
		base.InstanceID = extra.InstanceID
	}
	if base.CreatedAt == nil && extra.CreatedAt != nil {
		base.CreatedAt = extra.CreatedAt
	}
	if cvmInfoInstanceType(&base) == "" && cvmInfoInstanceType(&extra) != "" {
		// Preserve the richer Resource block from extra.
		base.Resource = extra.Resource
	}
	if cvmInfoRegion(&base) == "" && cvmInfoRegion(&extra) != "" {
		base.NodeInfo = extra.NodeInfo
	}
	if cvmInfoEndpoint(&base) == "" && cvmInfoEndpoint(&extra) != "" {
		base.Endpoints = extra.Endpoints
		base.PublicURLs = extra.PublicURLs
	}
	// Gateway info is typically missing on the create-time POST response
	// and only appears after the cloud's steady-state read. Take it from
	// `extra` whenever `base` doesn't already carry it so populateState
	// can publish gateway_base_domain / gateway_cname on first apply.
	if cvmInfoGatewayBaseDomain(&base) == "" && cvmInfoGatewayBaseDomain(&extra) != "" {
		base.Gateway = extra.Gateway
	}
	if strings.TrimSpace(base.ID) == "" {
		base.ID = extra.ID
	}
	return base
}

func populateAppInstanceState(state *appInstanceResourceModel, appID, name string, cvm phala.CVMInfo) {
	state.ID = types.StringValue(appID + ":" + name)
	state.AppID = types.StringValue(appID)
	state.Name = types.StringValue(name)

	state.VMUUID = nullableString(cvmInfoVMUUID(&cvm))
	state.InstanceID = nullableString(cvmInfoInstanceID(&cvm))
	state.Status = nullableString(cvm.Status)
	state.Region = nullableString(cvmInfoRegion(&cvm))
	state.InstanceType = nullableString(cvmInfoInstanceType(&cvm))
	state.Endpoint = nullableString(cvmInfoEndpoint(&cvm))
	state.GatewayBaseDomain = nullableString(cvmInfoGatewayBaseDomain(&cvm))
	state.GatewayCname = nullableString(cvmInfoGatewayCname(&cvm))
	createdAt := ""
	if cvm.CreatedAt != nil {
		createdAt = *cvm.CreatedAt
	}
	state.CreatedAt = nullableString(createdAt)
}

func validateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !instanceNamePattern.MatchString(name) {
		return fmt.Errorf("name must be 5-63 characters, start with a letter, and contain only letters, digits, and hyphens (got %q)", name)
	}
	return nil
}

// stablePowerState (used above) is defined in resource_cvm_power.go.
