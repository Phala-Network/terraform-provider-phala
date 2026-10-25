package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &cvmEnvResource{}

type cvmEnvResource struct {
	client *phala.Client
}

type cvmEnvResourceModel struct {
	ID                types.String `tfsdk:"id"`
	CVMID             types.String `tfsdk:"cvm_id"`
	Env               types.Map    `tfsdk:"env"`
	WaitForReady      types.Bool   `tfsdk:"wait_for_ready"`
	WaitTimeoutSecond types.Int64  `tfsdk:"wait_timeout_seconds"`
	Status            types.String `tfsdk:"status"`
}

func NewCVMEnvResource() resource.Resource { return &cvmEnvResource{} }

func (r *cvmEnvResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cvm_env"
}

func (r *cvmEnvResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the complete environment value map of an existing CVM separately from its allocation. " +
			"Declare allowed keys with phala_app.env_keys and omit env/encrypted_env on the app and instance resources. " +
			"Values can then reference allocated instance identities without a Terraform dependency cycle. " +
			"This resource never changes the measured key allowlist or compose revision. " +
			"Cloud does not expose plaintext values for drift detection. Destroy only releases Terraform ownership; " +
			"it does not erase the CVM environment or delete/restart the CVM. Set env = {} explicitly to clear values.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Environment resource ID, equal to cvm_id."},
			"cvm_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Target CVM identifier, usually phala_app_instance.vm_uuid.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"env": schema.MapAttribute{
				Required: true, Sensitive: true, ElementType: types.StringType,
				MarkdownDescription: "Complete plaintext environment map. Encrypted before API submission, but stored in Terraform state. All keys must already be allowed by the measured app compose. Do not manage the same CVM's values in another resource.",
			},
			"wait_for_ready": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Wait for the CVM to be running with no operation in progress after the environment update. This is CVM readiness, not application health.",
			},
			"wait_timeout_seconds": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(600),
				MarkdownDescription: "Timeout for waiting for an existing operation and optional post-update readiness.",
			},
			"status": schema.StringAttribute{Computed: true, MarkdownDescription: "Current CVM status."},
		},
	}
}

func (r *cvmEnvResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*phala.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", "Expected *phala.Client while configuring cvm_env resource.")
		return
	}
	r.client = client
}

func (r *cvmEnvResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan cvmEnvResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	diags, _ := r.apply(ctx, &plan, true)
	resp.Diagnostics.Append(diags...)
	// Retain the binding after an accepted update even if readiness fails.
	if !plan.ID.IsNull() && !plan.ID.IsUnknown() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *cvmEnvResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state cvmEnvResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.Status = state.Status
	diags, accepted := r.apply(ctx, &plan, !plan.Env.Equal(state.Env))
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() || accepted {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *cvmEnvResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state cvmEnvResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetCVMInfo(ctx, state.CVMID.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Failed to read CVM environment target", err.Error())
		return
	}
	// Plaintext is deliberately unavailable from Cloud. Preserve the map
	// supplied by the operator; never claim that it was read back remotely.
	state.Status = types.StringValue(current.Status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *cvmEnvResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Environment configuration has no independent server-side object to
	// delete. Like cvm_power, releasing ownership does not mutate the CVM.
}

func (r *cvmEnvResource) apply(ctx context.Context, plan *cvmEnvResourceModel, write bool) (diag.Diagnostics, bool) {
	var diags diag.Diagnostics
	accepted := false
	if plan.CVMID.IsUnknown() || strings.TrimSpace(plan.CVMID.ValueString()) == "" {
		diags.AddAttributeError(path.Root("cvm_id"), "Invalid cvm_id", "cvm_id must be non-empty and known at apply time.")
	}
	if plan.Env.IsNull() {
		diags.AddAttributeError(path.Root("env"), "Missing env", "env must be a complete map; use {} to clear values.")
	}
	for key := range plan.Env.Elements() {
		if key != strings.TrimSpace(key) {
			diags.AddAttributeError(path.Root("env"), "Invalid env key", "Environment keys must not have leading or trailing whitespace.")
		}
	}
	if plan.WaitForReady.IsUnknown() {
		diags.AddAttributeError(path.Root("wait_for_ready"), "Unknown readiness setting", "wait_for_ready must be known at apply time.")
	}
	env, envDiags := mapValueAsStrings(ctx, plan.Env, "env")
	diags.Append(envDiags...)
	if !plan.WaitTimeoutSecond.IsNull() && (plan.WaitTimeoutSecond.IsUnknown() || plan.WaitTimeoutSecond.ValueInt64() <= 0) {
		diags.AddAttributeError(path.Root("wait_timeout_seconds"), "Invalid timeout", "wait_timeout_seconds must be positive and known at apply time.")
	}
	if diags.HasError() {
		return diags, accepted
	}
	ctx, cancel := context.WithTimeout(ctx, waitTimeout(plan.WaitTimeoutSecond))
	defer cancel()
	id := plan.CVMID.ValueString()
	var current *phala.CVMInfo
	var err error
	if write {
		current, err = r.idleTarget(ctx, id)
	} else {
		current, err = r.client.GetCVMInfo(ctx, id)
	}
	if err != nil {
		diags.AddError("Failed to wait for CVM operation", err.Error())
		return diags, accepted
	}
	plan.Status = types.StringValue(current.Status)
	if write {
		compose, err := r.client.GetCVMComposeFile(ctx, id)
		if err != nil {
			diags.AddError("Failed to read measured environment allowlist", err.Error())
			return diags, accepted
		}
		allowed := make(map[string]bool, len(compose.AllowedEnvs))
		for _, key := range compose.AllowedEnvs {
			allowed[key] = true
		}
		for _, key := range sortedEnvKeys(env) {
			if !allowed[key] {
				diags.AddAttributeError(path.Root("env"), "Environment key is not allowed", fmt.Sprintf("Key %q is not in the measured allowlist. Declare it in phala_app.env_keys first; phala_cvm_env cannot change compose measurements.", key))
			}
		}
		if diags.HasError() {
			return diags, accepted
		}
		pubkey := cvmInfoEnvEncryptionPubkey(current)
		if pubkey == "" {
			diags.AddError("Missing encryption public key", "The CVM did not expose its environment encryption public key.")
			return diags, accepted
		}
		encrypted, err := encryptEnvMap(env, pubkey)
		if err != nil {
			diags.AddError("Failed to encrypt env", err.Error())
			return diags, accepted
		}
		// Only values are changed: never send env_keys, compose_hash, or a
		// reconstructed compose document (including for v3 manifests).
		result, err := r.client.UpdateCVMEnvs(ctx, id, &phala.UpdateEnvsRequest{EncryptedEnv: encrypted})
		if err != nil {
			summary, detail := diagnosticFromAPIError("Failed to update CVM environment", err)
			diags.AddError(summary, detail)
			return diags, accepted
		}
		if result.Status == phala.UpdateStatusPreconditionRequired || (result.RequiresOnChainHash != nil && *result.RequiresOnChainHash) {
			diags.AddError("Unexpected compose precondition", "A value-only environment update must not require a measured compose change.")
			return diags, accepted
		}
		accepted = true
		plan.ID = types.StringValue(id)
		plan.Status = types.StringValue("updating")
	}
	if shouldWait(plan.WaitForReady) {
		if err := (&appResource{client: r.client}).waitForCVMReadyByID(ctx, id, time.Now().Add(waitTimeout(plan.WaitTimeoutSecond))); err != nil {
			diags.AddError("CVM did not become ready after environment update", err.Error())
			return diags, accepted
		}
		plan.Status = types.StringValue("running")
	}
	return diags, accepted
}

func (r *cvmEnvResource) idleTarget(ctx context.Context, id string) (*phala.CVMInfo, error) {
	for {
		current, err := r.client.GetCVMInfo(ctx, id)
		if err != nil {
			return nil, err
		}
		if !cvmInfoInProgress(current) {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval(3 * time.Second)):
		}
	}
}
