package provider

import (
	"context"
	"fmt"
	"reflect"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// buildComposeUpdateRequest builds one unified PATCH for every compose change
// of an apply, or returns nil when the compose does not change. Only changed
// keys are sent; the backend keeps the rest of the stored compose.
func buildComposeUpdateRequest(
	ctx context.Context,
	plan, state appResourceModel,
	envKeys []string,
	updateEnvKeys bool,
) (*phala.PatchCVMRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	req := &phala.PatchCVMRequest{}
	changed := false
	if !plan.DockerCompose.Equal(state.DockerCompose) {
		v := plan.DockerCompose.ValueString()
		req.DockerComposeFile = &v
		changed = true
	}
	if !plan.PreLaunchScript.Equal(state.PreLaunchScript) {
		// An empty script removes it.
		v := plan.PreLaunchScript.ValueString()
		req.PreLaunchScript = &v
		changed = true
	}
	if updateEnvKeys {
		req.AllowedEnvs = append([]string{}, envKeys...)
		changed = true
	}
	for _, flag := range []struct {
		plan, state types.Bool
		target      **bool
	}{
		{plan.PublicLogs, state.PublicLogs, &req.PublicLogs},
		{plan.PublicSysinfo, state.PublicSysinfo, &req.PublicSysinfo},
		{plan.PublicTCBInfo, state.PublicTCBInfo, &req.PublicTcbinfo},
	} {
		if !flag.plan.Equal(flag.state) && boolPtrValue(flag.plan) != nil {
			*flag.target = boolPtrValue(flag.plan)
			changed = true
		}
	}

	patch := phala.ComposePatch{
		GatewayEnabled: patchOf(boolPtrValue(plan.GatewayEnabled), boolPtrValue(state.GatewayEnabled)),
		SecureTime:     patchOf(boolPtrValue(plan.SecureTime), boolPtrValue(state.SecureTime)),
	}
	if !reflect.DeepEqual(patch, phala.ComposePatch{}) {
		req.Compose = &patch
		changed = true
	}

	if !changed {
		return nil, diags
	}
	return req, diags
}

// patchCompose sends a compose update to one CVM.
func patchCompose(ctx context.Context, client *phala.Client, cvmID string, req *phala.PatchCVMRequest) error {
	resp, err := client.PatchCVM(ctx, cvmID, req)
	if err != nil {
		return err
	}
	if resp.RequiresOnChainHash {
		return fmt.Errorf("on-chain KMS compose-hash registration required (HTTP 465); this provider supports kms = phala only for now")
	}
	return nil
}

// patchOf sets a changed key and removes one that is no longer configured.
func patchOf[T comparable](desired, current *T) phala.Patch[T] {
	switch {
	case desired == nil && current == nil:
		return phala.Patch[T]{}
	case desired == nil:
		return phala.PatchNull[T]()
	case current != nil && *desired == *current:
		return phala.Patch[T]{}
	default:
		return phala.PatchValue(*desired)
	}
}

func boolPtrValue(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}
