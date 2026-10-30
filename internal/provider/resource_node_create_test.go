package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestNodesCatalogPhysicalName(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/teepods/available" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"nodes":[{"teepod_id":18,"node_id":42,"name":"teepod-display-name","node":"physical-node","images":[]}]}`))
	}))
	defer srv.Close()
	d := &nodesDataSource{client: newTestPhalaClient(t, srv.URL+"/api/v1")}
	var schemaResp datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatal(schemaResp.Diagnostics)
	}
	object := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	config := tftypes.NewValue(object, map[string]tftypes.Value{
		"region":              tftypes.NewValue(tftypes.String, nil),
		"support_onchain_kms": tftypes.NewValue(tftypes.Bool, nil),
		"nodes":               tftypes.NewValue(object.AttributeTypes["nodes"], nil),
	})
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: config}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var result nodesDataSourceModel
	if diags := resp.State.Get(ctx, &result); diags.HasError() {
		t.Fatal(diags)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Node.ValueString() != "physical-node" || result.Nodes[0].NodeID.ValueInt64() != 18 || result.Nodes[0].Name.ValueString() != "teepod-display-name" {
		t.Fatalf("unexpected catalog mapping: %#v", result.Nodes)
	}
}

func TestAdoptInstancePlacement(t *testing.T) {
	cases := []struct {
		name, reportedNode, configuredNode, wantError string
		changeDuringWait                              bool
	}{
		{"matching", `"physical-node"`, "physical-node", "", false},
		{"mismatch", `"other-node"`, "physical-node", "Adopted instance is on a different node", false},
		{"missing", `null`, "physical-node", "Cannot verify adopted instance placement", false},
		{"omitted", `null`, "", "", false},
		{"changed during wait", `"physical-node"`, "physical-node", "Adopted instance is on a different node", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mutations := 0
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
					reads++
					reportedNode := tc.reportedNode
					if tc.changeDuringWait && reads > 1 {
						reportedNode = `"other-node"`
					}
					fmt.Fprintf(w, `[{"name":"worker-1","vm_uuid":"vm-aaa","app_id":"app_test","status":"running","node_info":{"id":18,"name":%s}}]`, reportedNode)
					return
				}
				mutations++
				http.Error(w, "unexpected mutation", http.StatusInternalServerError)
			}))
			defer srv.Close()
			server := providerserver.NewProtocol6(New("test")())()
			provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_key": tftypes.String, "api_prefix": tftypes.String, "api_version": tftypes.String, "timeout_seconds": tftypes.Number}}
			prov, _ := tfprotov6.NewDynamicValue(provType, tftypes.NewValue(provType, map[string]tftypes.Value{"api_key": tftypes.NewValue(tftypes.String, "phat_test_key"), "api_prefix": tftypes.NewValue(tftypes.String, srv.URL+"/api/v1"), "api_version": tftypes.NewValue(tftypes.String, nil), "timeout_seconds": tftypes.NewValue(tftypes.Number, 5)}))
			configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &prov})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, configured.Diagnostics)
			ot := instanceObjectType()
			var config map[string]tftypes.Value
			if err := instanceConfigValue("unused").As(&config); err != nil {
				t.Fatal(err)
			}
			config["env"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil)
			config["wait_for_ready"] = tftypes.NewValue(tftypes.Bool, tc.changeDuringWait)
			if tc.configuredNode != "" {
				config["node"] = tftypes.NewValue(tftypes.String, tc.configuredNode)
			}
			configDV, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, config))
			prior, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, nil))
			plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "phala_app_instance", PriorState: &prior, ProposedNewState: &configDV, Config: &configDV})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, plan.Diagnostics)
			applied, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "phala_app_instance", PriorState: &prior, PlannedState: plan.PlannedState, Config: &configDV, PlannedPrivate: plan.PlannedPrivate})
			if err != nil {
				t.Fatal(err)
			}
			if mutations != 0 {
				t.Fatalf("adoption made %d mutation requests", mutations)
			}
			if tc.wantError != "" {
				found := false
				for _, d := range applied.Diagnostics {
					if d.Severity == tfprotov6.DiagnosticSeverityError && d.Summary == tc.wantError {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected %q: %v", tc.wantError, applied.Diagnostics)
				}
				if applied.NewState != nil {
					state, err := applied.NewState.Unmarshal(ot)
					if err != nil {
						t.Fatal(err)
					}
					if !state.IsNull() {
						t.Fatalf("failed adoption wrote state: %v", state)
					}
				}
				return
			}
			assertNoDiagnostics(t, applied.Diagnostics)
			state, err := applied.NewState.Unmarshal(ot)
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]tftypes.Value
			if err := state.As(&values); err != nil {
				t.Fatal(err)
			}
			var managed bool
			if err := values["managed"].As(&managed); err != nil || managed {
				t.Fatalf("managed = %v, %v", managed, err)
			}
			if tc.configuredNode != "" {
				var node string
				if err := values["node"].As(&node); err != nil || node != tc.configuredNode {
					t.Fatalf("node = %q, %v", node, err)
				}
			}
			// A new node on an adopted instance must fail at plan time, before Delete.
			values["node"] = tftypes.NewValue(tftypes.String, "another-node")
			changed, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, values))
			changePlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "phala_app_instance", PriorState: applied.NewState, ProposedNewState: &changed, Config: &changed})
			if err != nil {
				t.Fatal(err)
			}
			blocked := false
			for _, d := range changePlan.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError && d.Summary == "Cannot change adopted instance placement" {
					blocked = true
				}
			}
			if !blocked {
				t.Fatalf("adopted placement change was not blocked: %v", changePlan.Diagnostics)
			}
		})
	}
}

func TestInstanceCreatePlacementRequest(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "physical node"}[explicit], func(t *testing.T) {
			ctx := context.Background()
			var body map[string]json.RawMessage
			created := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms":
					if !created {
						_, _ = w.Write([]byte(`[]`))
					} else {
						_, _ = w.Write([]byte(`[{"name":"worker-1","vm_uuid":"vm-aaa","app_id":"app_test","status":"running"}]`))
					}
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/test/instances":
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					created = true
					_, _ = w.Write([]byte(`{"name":"worker-1","vm_uuid":"vm-aaa","app_id":"app_test","status":"running"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			server := providerserver.NewProtocol6(New("test")())()
			provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_key": tftypes.String, "api_prefix": tftypes.String, "api_version": tftypes.String, "timeout_seconds": tftypes.Number}}
			prov, _ := tfprotov6.NewDynamicValue(provType, tftypes.NewValue(provType, map[string]tftypes.Value{"api_key": tftypes.NewValue(tftypes.String, "phat_test_key"), "api_prefix": tftypes.NewValue(tftypes.String, srv.URL+"/api/v1"), "api_version": tftypes.NewValue(tftypes.String, nil), "timeout_seconds": tftypes.NewValue(tftypes.Number, 5)}))
			configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &prov})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, configured.Diagnostics)
			ot := instanceObjectType()
			var config map[string]tftypes.Value
			if err := instanceConfigValue("unused").As(&config); err != nil {
				t.Fatal(err)
			}
			config["env"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil)
			config["wait_for_ready"] = tftypes.NewValue(tftypes.Bool, false)
			if explicit {
				config["node"] = tftypes.NewValue(tftypes.String, "physical-node")
			}
			configDV, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, config))
			prior, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, nil))
			proposed := make(map[string]tftypes.Value, len(config))
			for key, value := range config {
				proposed[key] = value
			}
			if !explicit {
				proposed["node"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
			}
			proposed["legacy_node_id"] = tftypes.NewValue(tftypes.Number, tftypes.UnknownValue)
			proposedDV, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, proposed))
			plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "phala_app_instance", PriorState: &prior, ProposedNewState: &proposedDV, Config: &configDV})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, plan.Diagnostics)
			plannedValue, err := plan.PlannedState.Unmarshal(ot)
			if err != nil {
				t.Fatal(err)
			}
			var plannedAttrs map[string]tftypes.Value
			if err := plannedValue.As(&plannedAttrs); err != nil {
				t.Fatal(err)
			}
			if !explicit && plannedAttrs["node"].IsKnown() {
				t.Fatalf("expected unknown node: %v", plannedAttrs["node"])
			}
			if plannedAttrs["legacy_node_id"].IsKnown() {
				t.Fatalf("expected unknown legacy ID: %v", plannedAttrs["legacy_node_id"])
			}
			applied, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "phala_app_instance", PriorState: &prior, PlannedState: plan.PlannedState, Config: &configDV, PlannedPrivate: plan.PlannedPrivate})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, applied.Diagnostics)
			createdValue, err := applied.NewState.Unmarshal(ot)
			if err != nil {
				t.Fatal(err)
			}
			var createdAttrs map[string]tftypes.Value
			if err := createdValue.As(&createdAttrs); err != nil {
				t.Fatal(err)
			}
			if !explicit && !createdAttrs["node"].IsNull() {
				t.Fatalf("create node is not null: %v", createdAttrs["node"])
			}
			if !createdAttrs["legacy_node_id"].IsNull() {
				t.Fatalf("create legacy ID is not null: %v", createdAttrs["legacy_node_id"])
			}
			refreshed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "phala_app_instance", CurrentState: applied.NewState})
			if err != nil {
				t.Fatal(err)
			}
			assertNoDiagnostics(t, refreshed.Diagnostics)
			readValue, err := refreshed.NewState.Unmarshal(ot)
			if err != nil {
				t.Fatal(err)
			}
			var readAttrs map[string]tftypes.Value
			if err := readValue.As(&readAttrs); err != nil {
				t.Fatal(err)
			}
			if !explicit && !readAttrs["node"].IsNull() {
				t.Fatalf("read node is not null: %v", readAttrs["node"])
			}
			if !readAttrs["legacy_node_id"].IsNull() {
				t.Fatalf("read legacy ID is not null: %v", readAttrs["legacy_node_id"])
			}
			if !created {
				t.Fatal("instance POST not received")
			}
			for _, key := range []string{"node_id", "teepod_id"} {
				if _, ok := body[key]; ok {
					t.Fatalf("unexpected selector %s", key)
				}
			}
			if explicit && string(body["node"]) != `"physical-node"` {
				t.Fatalf("node: %s", body["node"])
			}
			if !explicit {
				if _, ok := body["node"]; ok {
					t.Fatalf("automatic placement sent node: %s", body["node"])
				}
			}
		})
	}
}
