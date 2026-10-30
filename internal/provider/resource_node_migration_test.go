package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestLegacyNodeStateUpgradeReadAndPlan(t *testing.T) {
	for _, kind := range []string{"app", "app_instance"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			cvm := `{"name":"worker-1","vm_uuid":"vm-aaa","app_id":"app_test","instance_id":"inst-1","status":"running","resource":{"instance_type":"tdx.small"},"node_info":{"id":18,"name":"physical-node"}}`
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/apps/test":
					fmt.Fprintf(w, `{"app_id":"app_test","name":"worker-1","cvms":[%s]}`, cvm)
				case "/api/v1/apps/test/cvms":
					fmt.Fprintf(w, `[%s]`, cvm)
				default:
					http.NotFound(w, r)
				}
			}))
			defer httpServer.Close()
			server := providerserver.NewProtocol6(New("test")())()
			old := `{"id":"app_test","app_id":"app_test","name":"worker-1","size":"tdx.small","kms":"phala","node_id":18,"env":{"KEEP":"existing"},"docker_compose":"services: {}"}`
			if kind == "app_instance" {
				old = `{"id":"app_test:worker-1","app_id":"app_test","name":"worker-1","node_id":18,"vm_uuid":"vm-aaa","managed":true,"env":{"KEEP":"existing"}}`
			}
			typeName := "phala_" + kind
			upgrade, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{TypeName: typeName, Version: 0, RawState: &tfprotov6.RawState{JSON: []byte(old)}})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, upgrade.Diagnostics)
			provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_key": tftypes.String, "api_prefix": tftypes.String, "api_version": tftypes.String, "timeout_seconds": tftypes.Number}}
			prov, _ := tfprotov6.NewDynamicValue(provType, tftypes.NewValue(provType, map[string]tftypes.Value{"api_key": tftypes.NewValue(tftypes.String, "phat_test_key"), "api_prefix": tftypes.NewValue(tftypes.String, httpServer.URL+"/api/v1"), "api_version": tftypes.NewValue(tftypes.String, nil), "timeout_seconds": tftypes.NewValue(tftypes.Number, 5)}))
			configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &prov})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, configured.Diagnostics)
			unrefreshed, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, PriorState: upgrade.UpgradedState, ProposedNewState: upgrade.UpgradedState, Config: upgrade.UpgradedState})
			if err != nil {
				t.Fatal(err)
			}
			blocked := false
			for _, d := range unrefreshed.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError && d.Summary == "Node placement migration requires refresh" {
					blocked = true
				}
			}
			if !blocked {
				t.Fatal("unresolved legacy placement must block planning without refresh")
			}
			read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: upgrade.UpgradedState})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, read.Diagnostics)
			var objectType tftypes.Object
			if kind == "app" {
				objectType = envUpdateAppObjectType()
			} else {
				objectType = instanceObjectType()
			}
			value, err := read.NewState.Unmarshal(objectType)
			if err != nil {
				t.Fatal(err)
			}
			var attrs map[string]tftypes.Value
			if err := value.As(&attrs); err != nil {
				t.Fatal(err)
			}
			var node, id string
			if err := attrs["node"].As(&node); err != nil {
				t.Fatal(err)
			}
			if err := attrs["id"].As(&id); err != nil {
				t.Fatal(err)
			}
			if node != "physical-node" || !attrs["legacy_node_id"].IsNull() {
				t.Fatalf("node %q legacy %v", node, attrs["legacy_node_id"])
			}
			wantID := "app_test"
			if kind == "app_instance" {
				wantID = "app_test:worker-1"
			}
			if id != wantID {
				t.Fatalf("identity changed: %s", id)
			}
			var env map[string]tftypes.Value
			if err := attrs["env"].As(&env); err != nil {
				t.Fatal(err)
			}
			var kept string
			if err := env["KEEP"].As(&kept); err != nil || kept != "existing" {
				t.Fatalf("env not preserved: %v (%v)", env, err)
			}
			if kind == "app_instance" {
				var managed bool
				if err := attrs["managed"].As(&managed); err != nil || !managed {
					t.Fatalf("managed not preserved: %v (%v)", managed, err)
				}
			}
			// A previously automatic selector must remain automatic even when a CVM reports its current node.
			autoUpgrade, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{TypeName: typeName, Version: 0, RawState: &tfprotov6.RawState{JSON: []byte(strings.Replace(old, `"node_id":18`, `"node_id":null`, 1))}})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, autoUpgrade.Diagnostics)
			autoRead, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: autoUpgrade.UpgradedState})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, autoRead.Diagnostics)
			autoValue, err := autoRead.NewState.Unmarshal(objectType)
			if err != nil {
				t.Fatal(err)
			}
			var autoAttrs map[string]tftypes.Value
			if err := autoValue.As(&autoAttrs); err != nil {
				t.Fatal(err)
			}
			if !autoAttrs["node"].IsNull() {
				t.Fatalf("automatic placement was pinned: %v", autoAttrs["node"])
			}
			// A CVM on a different teepod cannot establish the old selector's name.
			cvm = strings.Replace(cvm, `"id":18`, `"id":19`, 1)
			failed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: upgrade.UpgradedState})
			if err != nil {
				t.Fatal(err)
			}
			foundError := false
			for _, d := range failed.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError && d.Summary == "Cannot migrate node placement" {
					foundError = true
				}
			}
			if !foundError {
				t.Fatal("expected an error for an unresolved legacy selector")
			}
			cvm = ""
			missing, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: upgrade.UpgradedState})
			if err != nil {
				t.Fatal(err)
			}
			foundError = false
			for _, d := range missing.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError && d.Summary == "Cannot migrate node placement" {
					foundError = true
				}
			}
			if !foundError {
				t.Fatal("missing CVM must not remove an unresolved legacy resource from state")
			}
			for _, explicit := range []bool{false, true} {
				config := make(map[string]tftypes.Value, len(attrs))
				proposed := make(map[string]tftypes.Value, len(attrs))
				for k, v := range attrs {
					config[k], proposed[k] = v, v
				}
				config["legacy_node_id"] = tftypes.NewValue(tftypes.Number, nil)
				if !explicit {
					config["node"] = tftypes.NewValue(tftypes.String, nil)
					proposed["node"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
				}
				configDV, err := tfprotov6.NewDynamicValue(objectType, tftypes.NewValue(objectType, config))
				if err != nil {
					t.Fatal(err)
				}
				proposedDV, err := tfprotov6.NewDynamicValue(objectType, tftypes.NewValue(objectType, proposed))
				if err != nil {
					t.Fatal(err)
				}
				plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, PriorState: read.NewState, ProposedNewState: &proposedDV, Config: &configDV})
				if err != nil {
					t.Fatal(err)
				}
				assertNoDiagnostics(t, plan.Diagnostics)
				if len(plan.RequiresReplace) != 0 {
					t.Fatalf("explicit=%v: unexpected replacement: %v", explicit, plan.RequiresReplace)
				}
			}
			changed := make(map[string]tftypes.Value, len(attrs))
			for k, v := range attrs {
				changed[k] = v
			}
			changed["node"] = tftypes.NewValue(tftypes.String, "another-node")
			changedDV, err := tfprotov6.NewDynamicValue(objectType, tftypes.NewValue(objectType, changed))
			if err != nil {
				t.Fatal(err)
			}
			plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, PriorState: read.NewState, ProposedNewState: &changedDV, Config: &changedDV})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, plan.Diagnostics)
			if len(plan.RequiresReplace) == 0 {
				t.Fatal("changing physical node must require replacement")
			}
		})
	}
}

func assertNoDiagnostics(t *testing.T, diags []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("%s: %s", d.Summary, d.Detail)
		}
	}
}
