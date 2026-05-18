package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
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
	client *APIClient
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
					"When omitted, the backend uses the app's template instance. Changing forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"pre_launch_script": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional pre-launch script content. Changing forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"env": schema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				MarkdownDescription: "Plaintext env vars for this instance. Values are encrypted before API submission, " +
					"but plaintext is stored in Terraform state. Changing forces replacement. The parent app compose " +
					"must already allow these env keys.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"encrypted_env": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Optional hex-encoded encrypted env payload to seed at create time.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"compose_hash": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Optional explicit compose hash. When omitted the backend resolves it " +
					"from `docker_compose` (if provided) or the app's current revision. Changing forces replacement.",
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
	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			"Expected *APIClient while configuring app instance resource.",
		)
		return
	}
	r.client = client
}

// createAppInstanceRequest mirrors the cloud `POST /apps/{app_id}/instances`
// body. We do not depend on the Go SDK's CreateAppInstanceRequest because the
// `Name` field is still in-flight in the SDK (phala-cloud#263 / phala-cloud-monorepo#1386).
type createAppInstanceRequest struct {
	Name              string  `json:"name"`
	NodeID            *int64  `json:"node_id,omitempty"`
	DockerComposeFile *string `json:"docker_compose_file,omitempty"`
	PreLaunchScript   *string `json:"pre_launch_script,omitempty"`
	EncryptedEnv      *string `json:"encrypted_env,omitempty"`
	ComposeHash       *string `json:"compose_hash,omitempty"`
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

	body := createAppInstanceRequest{Name: name}
	if !plan.NodeID.IsNull() && !plan.NodeID.IsUnknown() {
		v := plan.NodeID.ValueInt64()
		if v <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("node_id"), "Invalid node_id", "node_id must be greater than 0.")
			return
		}
		body.NodeID = &v
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

	var created cvmAPIResponse
	createPath := appPath(appID) + "/instances"
	if err := r.client.PostJSON(ctx, createPath, body, &created); err != nil {
		// On-chain KMS flows return HTTP 465 with a commit token; surface a clear error
		// rather than a confusing JSON-decode message. The two-phase flow is not yet
		// wired up here because the provider only supports kms = phala today.
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == 465 {
			resp.Diagnostics.AddError(
				"On-chain KMS not yet supported for phala_app_instance",
				"The cloud API returned HTTP 465 (on-chain commit token required). phala_app_instance "+
					"currently supports only the single-call PHALA KMS flow. Track this gap and use the "+
					"two-phase prepare/commit flow once exposed.",
			)
			return
		}
		resp.Diagnostics.AddError("Failed to create app instance", err.Error())
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
	merged := mergeCVMResponse(created, resolved)

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

func (r *appInstanceResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All meaningful inputs are RequiresReplace. The only updatable fields are
	// wait_for_ready / wait_timeout_seconds, which control provider-side polling
	// only — accept the new plan into state without making any API calls.
	resp.Diagnostics.AddError(
		"phala_app_instance has no in-place update path",
		"All input fields are RequiresReplace. If this Update was reached, the framework will recreate the resource instead.",
	)
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

	if err := r.client.Delete(ctx, cvmPath(vmUUID)); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Failed to delete app instance", err.Error())
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
func (r *appInstanceResource) findExistingByName(ctx context.Context, appID, name string) (cvmAPIResponse, bool, error) {
	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil {
		if isNotFound(err) {
			return cvmAPIResponse{}, false, nil
		}
		return cvmAPIResponse{}, false, err
	}
	if match := findInstanceByName(cvms, name); match != nil {
		return *match, true, nil
	}
	return cvmAPIResponse{}, false, nil
}

func (r *appInstanceResource) fetchAppCVMs(ctx context.Context, appID string) ([]cvmAPIResponse, error) {
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

func (r *appInstanceResource) fetchCVM(ctx context.Context, id string) (*cvmAPIResponse, error) {
	var out cvmAPIResponse
	if err := r.client.GetJSON(ctx, cvmPath(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *appInstanceResource) loadAppEnvEncryptionPubkey(ctx context.Context, appID string) (string, error) {
	cvms, err := r.fetchAppCVMs(ctx, appID)
	if err != nil {
		return "", err
	}
	for _, cvm := range cvms {
		if pubkey := cvm.envEncryptionPubkey(); pubkey != "" {
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
		if pubkey := detail.envEncryptionPubkey(); pubkey != "" {
			return pubkey, nil
		}
	}
	return "", nil
}

func (r *appInstanceResource) waitForInstance(ctx context.Context, appID, name string, deadline time.Time) (cvmAPIResponse, error) {
	for {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil && !isRetryable(err) && !isNotFound(err) {
			return cvmAPIResponse{}, err
		}
		if match := findInstanceByName(cvms, name); match != nil {
			return *match, nil
		}
		if time.Now().After(deadline) {
			return cvmAPIResponse{}, fmt.Errorf("timed out waiting for app %q instance %q to appear in CVM list", appID, name)
		}
		select {
		case <-ctx.Done():
			return cvmAPIResponse{}, ctx.Err()
		case <-time.After(pollInterval(2 * time.Second)):
		}
	}
}

func (r *appInstanceResource) waitForInstanceRunning(ctx context.Context, appID, name string, deadline time.Time) (cvmAPIResponse, error) {
	for {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil && !isRetryable(err) && !isNotFound(err) {
			return cvmAPIResponse{}, err
		}
		if match := findInstanceByName(cvms, name); match != nil {
			if strings.EqualFold(strings.TrimSpace(match.Status), "running") && !match.inProgress() {
				return *match, nil
			}
			if stablePowerState(match.Status) == "stopped" && !match.inProgress() {
				return cvmAPIResponse{}, fmt.Errorf("instance %q entered terminal stopped state: %s", name, describeReplicaState(*match))
			}
		}
		if time.Now().After(deadline) {
			return cvmAPIResponse{}, fmt.Errorf("timed out waiting for app %q instance %q to become running", appID, name)
		}
		select {
		case <-ctx.Done():
			return cvmAPIResponse{}, ctx.Err()
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

func findInstanceByName(cvms []cvmAPIResponse, name string) *cvmAPIResponse {
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
func mergeCVMResponse(base, extra cvmAPIResponse) cvmAPIResponse {
	if base.Name == "" {
		base.Name = extra.Name
	}
	if extra.Status != "" {
		base.Status = extra.Status
	}
	base.InProgress = extra.InProgress
	base.Progress = extra.Progress
	if base.AppID == "" {
		base.AppID = extra.AppID
	}
	if base.VMUUID == "" {
		base.VMUUID = extra.VMUUID
	}
	if base.InstanceID == "" {
		base.InstanceID = extra.InstanceID
	}
	if base.CreatedAt == "" {
		base.CreatedAt = extra.CreatedAt
	}
	if base.instanceType() == "" && extra.instanceType() != "" {
		// Preserve the richer Resource block from extra.
		base.Resource = extra.Resource
		base.InstanceType = extra.InstanceType
	}
	if base.region() == "" && extra.region() != "" {
		base.NodeInfo = extra.NodeInfo
		base.Node = extra.Node
	}
	if base.endpoint() == "" && extra.endpoint() != "" {
		base.Endpoints = extra.Endpoints
		base.PublicURLs = extra.PublicURLs
	}
	if len(base.ID) == 0 {
		base.ID = extra.ID
	}
	return base
}

func populateAppInstanceState(state *appInstanceResourceModel, appID, name string, cvm cvmAPIResponse) {
	state.ID = types.StringValue(appID + ":" + name)
	state.AppID = types.StringValue(appID)
	state.Name = types.StringValue(name)

	state.VMUUID = nullableString(cvm.VMUUID)
	state.InstanceID = nullableString(cvm.InstanceID)
	state.Status = nullableString(cvm.Status)
	state.Region = nullableString(cvm.region())
	state.InstanceType = nullableString(cvm.instanceType())
	state.Endpoint = nullableString(cvm.endpoint())
	state.CreatedAt = nullableString(cvm.CreatedAt)
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
