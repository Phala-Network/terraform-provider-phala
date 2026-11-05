package provider

import (
	"context"
	"reflect"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// This file maps the app compose settings beyond the docker compose file,
// scripts and visibility flags. The backend decides which of them an update
// may change (docs: CVM configuration update API, section 3); runner,
// bash_script and requirements.gpu_policy have no update path, so changing
// them replaces the app. Every other setting is sent as a JSON Merge Patch
// through the unified PATCH /cvms/{id}, which keeps compose keys Terraform
// does not manage, such as requirements.launch_token_hash.

// composeConfigSchemaAttrs returns the phala_app attributes for these settings.
func composeConfigSchemaAttrs() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"port_policy": schema.SingleNestedAttribute{
			Optional: true,
			MarkdownDescription: "Which guest ports the dstack gateway forwards (compose `port_policy`, dstack OS 0.5.9+). " +
				"Changing it triggers compose update/restart.",
			Attributes: map[string]schema.Attribute{
				"restrict_mode": schema.BoolAttribute{
					Optional:            true,
					MarkdownDescription: "Forward only the listed ports. Unset means `false`.",
				},
				"ports": schema.ListNestedAttribute{
					Optional:            true,
					MarkdownDescription: "Ports the gateway forwards.",
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"port": schema.Int64Attribute{
								Required:            true,
								MarkdownDescription: "Guest port.",
							},
							"pp": schema.BoolAttribute{
								Optional:            true,
								MarkdownDescription: "Send the PROXY protocol header to this port. Unset means `false`.",
							},
						},
					},
				},
			},
		},
		"swap_size": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Guest swap size, e.g. `2G`. Changing it triggers compose update/restart.",
		},
		"storage_discard": schema.BoolAttribute{
			Optional:            true,
			MarkdownDescription: "Pass discard (TRIM) through to the data disk. Changing it triggers compose update/restart.",
		},
		"snapshotter": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Container snapshotter, `overlayfs` or `stargz`. Only with `runner = \"nerdctl-compose\"`. " +
				"Changing it triggers compose update/restart.",
		},
		"event_log_version": schema.Int64Attribute{
			Optional: true,
			MarkdownDescription: "Event log format version, `1` or `2`. Relying parties that verify this app must support the chosen version. " +
				"Changing it triggers compose update/restart.",
		},
		"runner": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "How the guest runs the app: `docker-compose` (default), `nerdctl-compose` (dstack OS 0.6.0+) or `bash`. " +
				"Runners other than `docker-compose` need the `compose_advanced` feature flag. Changing this forces replacement.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"bash_script": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Script the `bash` runner executes instead of a Docker Compose file; set `docker_compose = \"\"` with it. " +
				"Changing this forces replacement.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"requirements": schema.SingleNestedAttribute{
			Optional: true,
			MarkdownDescription: "Guest requirements (compose `requirements`, dstack OS 0.6.0+). Only the keys you set are written to the compose. " +
				"Changing a key other than `gpu_policy` triggers compose update/restart.",
			// gpu_policy has no update path. The check sits on the parent because
			// nested modifiers do not run when the whole block is removed.
			PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplaceIf(
				gpuPolicyChanged,
				"Changing requirements.gpu_policy forces replacement.",
				"Changing `requirements.gpu_policy` forces replacement.",
			)},
			Attributes: map[string]schema.Attribute{
				"gpu_policy": schema.SingleNestedAttribute{
					Optional: true,
					MarkdownDescription: "GPU policy the guest enforces. Needs the `compose_gpu_policy` feature flag. " +
						"Changing this forces replacement.",
					Attributes: map[string]schema.Attribute{
						"allow_debug":         schema.BoolAttribute{Optional: true},
						"allow_devtools":      schema.BoolAttribute{Optional: true},
						"allow_insecure_boot": schema.BoolAttribute{Optional: true},
						"attest_gpu":          schema.BoolAttribute{Optional: true},
						"rego":                schema.StringAttribute{Optional: true},
					},
				},
				"health_check": schema.BoolAttribute{
					Optional:            true,
					MarkdownDescription: "Report app health to the host. With `runner = \"bash\"` it needs `health_status_file`.",
				},
				"health_status_file": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "File the guest reads the app's health from.",
				},
				"platforms": schema.ListAttribute{
					Optional:            true,
					ElementType:         types.StringType,
					MarkdownDescription: "Platforms the app may run on. Unset allows any; an empty list allows none.",
				},
				"tdx_measure_acpi_tables": schema.BoolAttribute{
					Optional:            true,
					MarkdownDescription: "Measure the ACPI tables into the TDX RTMRs.",
				},
				"ephemeral_data_disk": schema.BoolAttribute{
					Optional: true,
					MarkdownDescription: "Format the data disk on every boot (dstack OS 0.6.3+). " +
						"Turning it on for an existing app also needs `confirm_data_wipe = true`.",
				},
			},
		},
		"init_scripts": schema.SetAttribute{
			Optional:    true,
			ElementType: types.StringType,
			MarkdownDescription: "Platform init scripts every CVM of the app runs on boot: `docker_cleanup` (erases all container images " +
				"and volumes on every boot) and `env_merge` (feature-flagged). Changing it triggers compose update/restart; " +
				"turning `docker_cleanup` on for an existing app also needs `confirm_data_wipe = true`. Removing the attribute removes every script.",
		},
		"confirm_data_wipe": schema.BoolAttribute{
			Optional: true,
			MarkdownDescription: "Confirms an update that turns on `init_scripts` `docker_cleanup` or `requirements.ephemeral_data_disk`, " +
				"which erase data on every boot. Not needed on create.",
		},
		"annotations": schema.MapAttribute{
			Optional:    true,
			ElementType: types.StringType,
			MarkdownDescription: "Host-side key/value metadata the dstack VMM stores per VM (dstack VMM 0.6.1+). " +
				"Not measured: tenants must not rely on it for security. Needs the `cvm_annotations` feature flag. " +
				"Applied to the app's bootstrap CVM at create, and to the app's existing CVMs when the attribute changes, by " +
				"replacing the whole map without a restart; host tools that read annotations when the CVM starts see the change " +
				"after the next restart. CVMs added later, such as `phala_app_instance` slots, do not inherit them. " +
				"Removing the attribute clears the annotations on the existing CVMs.",
		},
		"launch_token": schema.StringAttribute{
			Optional:  true,
			Computed:  true,
			Sensitive: true,
			MarkdownDescription: "App launch token (at least 32 bytes). Every CVM of the app must present it on boot; the compose carries " +
				"its hash in `requirements.launch_token_hash`. Set it, or set `generate_launch_token = true` to have Phala Cloud " +
				"generate one, which is stored here. Phala Cloud and the node operator can read the token, so it only protects " +
				"against parties who know the public compose. Changing it rotates the token and updates every CVM's compose; " +
				"removing it removes the hash and then the token.",
		},
		"generate_launch_token": schema.BoolAttribute{
			Optional: true,
			MarkdownDescription: "Have Phala Cloud generate the launch token when `launch_token` is not set. " +
				"The generated token is kept until you set `launch_token` or turn this off.",
		},
	}
}

// gpuPolicyChanged reports whether requirements.gpu_policy differs between
// state and plan, counting a removed requirements block as a null policy.
func gpuPolicyChanged(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
	if req.PlanValue.IsUnknown() {
		// Decided once the value is known: the final plan at apply time
		// then requires replacement, which Terraform reports as an error.
		return
	}
	state, plan := gpuPolicyOf(req.StateValue), gpuPolicyOf(req.PlanValue)
	if state == nil || plan == nil {
		resp.RequiresReplace = state != plan
		return
	}
	resp.RequiresReplace = !state.Equal(plan)
}

// gpuPolicyOf returns the set gpu_policy of a requirements block, or nil.
func gpuPolicyOf(requirements types.Object) attr.Value {
	if requirements.IsNull() || requirements.IsUnknown() {
		return nil
	}
	policy := requirements.Attributes()["gpu_policy"]
	if policy == nil || policy.IsNull() {
		return nil
	}
	return policy
}

type portPolicyModel struct {
	RestrictMode types.Bool `tfsdk:"restrict_mode"`
	Ports        types.List `tfsdk:"ports"`
}

type portAttrModel struct {
	Port types.Int64 `tfsdk:"port"`
	PP   types.Bool  `tfsdk:"pp"`
}

type requirementsModel struct {
	GPUPolicy            types.Object `tfsdk:"gpu_policy"`
	HealthCheck          types.Bool   `tfsdk:"health_check"`
	HealthStatusFile     types.String `tfsdk:"health_status_file"`
	Platforms            types.List   `tfsdk:"platforms"`
	TDXMeasureACPITables types.Bool   `tfsdk:"tdx_measure_acpi_tables"`
	EphemeralDataDisk    types.Bool   `tfsdk:"ephemeral_data_disk"`
}

type gpuPolicyModel struct {
	AllowDebug        types.Bool   `tfsdk:"allow_debug"`
	AllowDevtools     types.Bool   `tfsdk:"allow_devtools"`
	AllowInsecureBoot types.Bool   `tfsdk:"allow_insecure_boot"`
	AttestGPU         types.Bool   `tfsdk:"attest_gpu"`
	Rego              types.String `tfsdk:"rego"`
}

// composeConfig is the Go form of the compose settings in this file. A nil
// field is unset in configuration.
type composeConfig struct {
	PortPolicy      *phala.ComposePortPolicy
	SwapSize        *string
	StorageDiscard  *bool
	Snapshotter     *string
	EventLogVersion *int
	Runner          *string
	BashScript      *string
	Requirements    *phala.ComposeRequirements
	InitScripts     []phala.InitScriptSlug
	HasInitScripts  bool
}

func readComposeConfig(ctx context.Context, m appResourceModel) (composeConfig, diag.Diagnostics) {
	var diags diag.Diagnostics
	cfg := composeConfig{
		SwapSize:       stringPtrValue(m.SwapSize),
		StorageDiscard: boolPtrValue(m.StorageDiscard),
		Snapshotter:    stringPtrValue(m.Snapshotter),
		Runner:         stringPtrValue(m.Runner),
		BashScript:     stringPtrValue(m.BashScript),
	}
	if v := int64PtrValue(m.EventLogVersion); v != nil {
		n := int(*v)
		cfg.EventLogVersion = &n
	}

	if !m.PortPolicy.IsNull() && !m.PortPolicy.IsUnknown() {
		var pp portPolicyModel
		diags.Append(m.PortPolicy.As(ctx, &pp, objectAsOptions)...)
		var ports []portAttrModel
		if !pp.Ports.IsNull() && !pp.Ports.IsUnknown() {
			diags.Append(pp.Ports.ElementsAs(ctx, &ports, false)...)
		}
		// The backend always writes both keys (defaults false and []), so the
		// explicit form hashes the same and replaces the stored policy whole.
		policy := &phala.ComposePortPolicy{
			Ports:        make([]phala.ComposePortAttr, 0, len(ports)),
			RestrictMode: boolPtrOr(pp.RestrictMode, false),
		}
		for _, p := range ports {
			policy.Ports = append(policy.Ports, phala.ComposePortAttr{
				Port: int(p.Port.ValueInt64()),
				PP:   p.PP.ValueBool(),
			})
		}
		cfg.PortPolicy = policy
	}

	if !m.Requirements.IsNull() && !m.Requirements.IsUnknown() {
		var rm requirementsModel
		diags.Append(m.Requirements.As(ctx, &rm, objectAsOptions)...)
		req := &phala.ComposeRequirements{
			HealthCheck:          boolPtrValue(rm.HealthCheck),
			HealthStatusFile:     stringPtrValue(rm.HealthStatusFile),
			TDXMeasureACPITables: boolPtrValue(rm.TDXMeasureACPITables),
			EphemeralDataDisk:    boolPtrValue(rm.EphemeralDataDisk),
		}
		if !rm.Platforms.IsNull() && !rm.Platforms.IsUnknown() {
			platforms := []string{}
			diags.Append(rm.Platforms.ElementsAs(ctx, &platforms, false)...)
			req.Platforms = platforms
		}
		if !rm.GPUPolicy.IsNull() && !rm.GPUPolicy.IsUnknown() {
			var gm gpuPolicyModel
			diags.Append(rm.GPUPolicy.As(ctx, &gm, objectAsOptions)...)
			req.GPUPolicy = &phala.ComposeGPUPolicy{
				AllowDebug:        boolPtrValue(gm.AllowDebug),
				AllowDevtools:     boolPtrValue(gm.AllowDevtools),
				AllowInsecureBoot: boolPtrValue(gm.AllowInsecureBoot),
				AttestGPU:         boolPtrValue(gm.AttestGPU),
				Rego:              stringPtrValue(gm.Rego),
			}
		}
		cfg.Requirements = req
	}

	if !m.InitScripts.IsNull() && !m.InitScripts.IsUnknown() {
		var slugs []string
		diags.Append(m.InitScripts.ElementsAs(ctx, &slugs, false)...)
		cfg.InitScripts = make([]phala.InitScriptSlug, 0, len(slugs))
		for _, s := range slugs {
			cfg.InitScripts = append(cfg.InitScripts, phala.InitScriptSlug(s))
		}
		cfg.HasInitScripts = true
	}
	return cfg, diags
}

// applyTo writes the settings into a create request.
func (c composeConfig) applyTo(req *phala.ProvisionCVMRequest) {
	cf := req.ComposeFile
	cf.PortPolicy = c.PortPolicy
	cf.SwapSize = c.SwapSize
	cf.StorageDiscard = c.StorageDiscard
	cf.Snapshotter = c.Snapshotter
	cf.EventLogVersion = c.EventLogVersion
	cf.Runner = c.Runner
	cf.BashScript = c.BashScript
	cf.Requirements = c.Requirements
	if c.HasInitScripts {
		req.InitScripts = c.InitScripts
	}
}

// composePatchFrom returns the merge patch that turns the current settings into
// the desired ones. Runner, bash_script and gpu_policy force replacement, so
// they never reach an update.
func composePatchFrom(desired, current composeConfig, launchTokenHash phala.Patch[string]) phala.ComposePatch {
	patch := phala.ComposePatch{
		SwapSize:        patchOf(desired.SwapSize, current.SwapSize),
		StorageDiscard:  patchOf(desired.StorageDiscard, current.StorageDiscard),
		Snapshotter:     patchOf(desired.Snapshotter, current.Snapshotter),
		EventLogVersion: patchOf(desired.EventLogVersion, current.EventLogVersion),
	}
	if !reflect.DeepEqual(desired.PortPolicy, current.PortPolicy) {
		if desired.PortPolicy == nil {
			patch.PortPolicy = phala.PatchNull[phala.ComposePortPolicy]()
		} else {
			patch.PortPolicy = phala.PatchValue(*desired.PortPolicy)
		}
	}
	if req, ok := requirementsPatchFrom(desired.Requirements, current.Requirements, launchTokenHash); ok {
		patch.Requirements = phala.PatchValue(req)
	}
	return patch
}

// requirementsPatchFrom patches requirements per key, so keys Terraform does
// not set (the stored gpu_policy, the launch token hash) stay as they are.
func requirementsPatchFrom(desired, current *phala.ComposeRequirements, launchTokenHash phala.Patch[string]) (phala.ComposeRequirementsPatch, bool) {
	if desired == nil {
		desired = &phala.ComposeRequirements{}
	}
	if current == nil {
		current = &phala.ComposeRequirements{}
	}
	patch := phala.ComposeRequirementsPatch{
		EphemeralDataDisk:    patchOf(desired.EphemeralDataDisk, current.EphemeralDataDisk),
		HealthCheck:          patchOf(desired.HealthCheck, current.HealthCheck),
		HealthStatusFile:     patchOf(desired.HealthStatusFile, current.HealthStatusFile),
		TDXMeasureACPITables: patchOf(desired.TDXMeasureACPITables, current.TDXMeasureACPITables),
		LaunchTokenHash:      launchTokenHash,
	}
	if !reflect.DeepEqual(desired.Platforms, current.Platforms) {
		if desired.Platforms == nil {
			patch.Platforms = phala.PatchNull[[]string]()
		} else {
			patch.Platforms = phala.PatchValue(desired.Platforms)
		}
	}
	return patch, !reflect.DeepEqual(patch, phala.ComposeRequirementsPatch{})
}

// composeConfigChanged reports whether a compose setting in this file differs
// between plan and state, or is unknown in configuration.
func composeConfigChanged(plan, state, config appResourceModel) bool {
	pairs := [][3]attr.Value{
		{config.PortPolicy, plan.PortPolicy, state.PortPolicy},
		{config.SwapSize, plan.SwapSize, state.SwapSize},
		{config.StorageDiscard, plan.StorageDiscard, state.StorageDiscard},
		{config.Snapshotter, plan.Snapshotter, state.Snapshotter},
		{config.EventLogVersion, plan.EventLogVersion, state.EventLogVersion},
		{config.Requirements, plan.Requirements, state.Requirements},
		{config.InitScripts, plan.InitScripts, state.InitScripts},
		{config.LaunchToken, plan.LaunchToken, state.LaunchToken},
	}
	for _, p := range pairs {
		if p[0].IsUnknown() || p[1].IsUnknown() || !p[1].Equal(p[2]) {
			return true
		}
	}
	return false
}

var objectAsOptions = basetypes.ObjectAsOptions{}

func stringPtrValue(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

func boolPtrOr(v types.Bool, fallback bool) *bool {
	if p := boolPtrValue(v); p != nil {
		return p
	}
	return &fallback
}

func int64PtrValue(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	n := v.ValueInt64()
	return &n
}
