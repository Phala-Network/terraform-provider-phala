package provider

import (
	"context"
	"fmt"
	"strings"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Version zero stored the teepod selector as node_id. Copy every attribute,
// including the selector, without contacting the API during state upgrade.
func legacyNodeUpgrader(current schema.Schema) resource.StateUpgrader {
	old := current
	old.Version = 0
	old.Attributes = make(map[string]schema.Attribute, len(current.Attributes))
	for name, attribute := range current.Attributes {
		if name != "legacy_node_id" {
			old.Attributes[name] = attribute
		}
	}
	old.Attributes["node_id"] = schema.Int64Attribute{Optional: true}
	return resource.StateUpgrader{
		PriorSchema: &old,
		StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
			var values map[string]tftypes.Value
			if err := req.State.Raw.As(&values); err != nil {
				resp.Diagnostics.AddError("Cannot upgrade node placement", err.Error())
				return
			}
			values["legacy_node_id"] = values["node_id"]
			delete(values, "node_id")
			resp.State.Raw = tftypes.NewValue(resp.State.Schema.Type().TerraformType(ctx), values)
		},
	}
}

func (r *appResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	var current resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &current)
	return map[int64]resource.StateUpgrader{0: legacyNodeUpgrader(current.Schema)}
}

func (r *appInstanceResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	var current resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &current)
	return map[int64]resource.StateUpgrader{0: legacyNodeUpgrader(current.Schema)}
}

// A plan without a refresh must not discard an unresolved legacy selector.
func legacyPlacementPending(ctx context.Context, state tfsdk.State, diags *diag.Diagnostics) bool {
	var legacyID types.Int64
	diags.Append(state.GetAttribute(ctx, path.Root("legacy_node_id"), &legacyID)...)
	if diags.HasError() {
		return true
	}
	if !legacyID.IsNull() && !legacyID.IsUnknown() {
		diags.AddError("Node placement migration requires refresh", "Run a plan with refresh enabled to resolve the legacy teepod ID before changing this resource.")
		return true
	}
	return false
}

// node_info.id is the teepod ID; node_info.name is the physical node name.
// Only a matching actual CVM can establish this mapping without guessing.
func migrateLegacyNode(legacyID *types.Int64, node *types.String, cvms []phala.CVMInfo, diags *diag.Diagnostics) bool {
	if legacyID.IsNull() || legacyID.IsUnknown() {
		return true
	}
	var name string
	for _, cvm := range cvms {
		info := cvm.NodeInfo
		if info == nil || info.ID == nil || int64(*info.ID) != legacyID.ValueInt64() || info.Name == nil || strings.TrimSpace(*info.Name) == "" {
			continue
		}
		if name != "" && name != *info.Name {
			diags.AddError("Cannot migrate node placement", fmt.Sprintf("CVMs disagree on the physical node name for legacy teepod ID %d.", legacyID.ValueInt64()))
			return false
		}
		name = *info.Name
	}
	if name == "" {
		diags.AddError("Cannot migrate node placement", fmt.Sprintf("No current CVM reports physical node name for legacy teepod ID %d. The existing state has not been changed; resolve the placement before planning.", legacyID.ValueInt64()))
		return false
	}
	if !node.IsNull() && !node.IsUnknown() && node.ValueString() != name {
		diags.AddError("Cannot migrate node placement", "The stored physical node name does not match the legacy teepod's physical node name.")
		return false
	}
	*node = types.StringValue(name)
	*legacyID = types.Int64Null()
	return true
}
