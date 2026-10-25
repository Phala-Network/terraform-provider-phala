package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestInstanceIdentityWaitDoesNotRequireRunning(t *testing.T) {
	var calls, inventoryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Errorf("identity wait mutated Cloud: %s", req.Method)
		}
		if req.URL.Path == "/api/v1/apps/test/cvms" {
			inventoryCalls.Add(1)
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-aaa","name":"worker-1","status":"processing","in_progress":true,"instance_id":null}]`)
			return
		}
		if req.URL.Path != "/api/v1/cvms/vm-aaa/state" {
			t.Errorf("unexpected identity source: %s", req.URL.Path)
		}
		identity := ""
		if calls.Add(1) > 1 {
			identity = "new-instance"
		}
		writeJSON(t, w, http.StatusOK, `{"id":"vm-aaa","instance_id":"`+identity+`"}`)
	}))
	defer server.Close()
	r := &appInstanceResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
	resolved, err := r.waitForInstanceState(context.Background(), "app_test", "worker-1", time.Now().Add(10*time.Second), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || inventoryCalls.Load() != 2 || cvmInfoInstanceID(&resolved) != "new-instance" || resolved.Status != "processing" {
		t.Fatalf("did not wait exactly for host identity: calls=%d instance=%s status=%s", calls.Load(), cvmInfoInstanceID(&resolved), resolved.Status)
	}
}

func TestInstanceWaitOnlyUpdateHonorsReadinessWithoutMutation(t *testing.T) {
	for _, managed := range []bool{true, false} {
		t.Run(map[bool]string{true: "managed", false: "adopted"}[managed], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != http.MethodGet || req.URL.Path != "/api/v1/apps/test/cvms" {
					t.Errorf("readiness-only update mutated Cloud: %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-aaa","name":"worker-1","status":"running","app_id":"app_test","instance_id":"inst-1"}]`)
			}))
			defer server.Close()
			r := &appInstanceResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
			var schemaResp resource.SchemaResponse
			r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
			var fields map[string]tftypes.Value
			if err := instanceStateValue("same").As(&fields); err != nil {
				t.Fatal(err)
			}
			fields["managed"] = tftypes.NewValue(tftypes.Bool, managed)
			fields["wait_for_ready"] = tftypes.NewValue(tftypes.Bool, false)
			fields["status"] = tftypes.NewValue(tftypes.String, "creating")
			prior := tftypes.NewValue(instanceObjectType(), fields)
			fields["wait_for_ready"] = tftypes.NewValue(tftypes.Bool, true)
			plan := tftypes.NewValue(instanceObjectType(), fields)
			req := resource.UpdateRequest{
				State: tfsdk.State{Schema: schemaResp.Schema, Raw: prior},
				Plan:  tfsdk.Plan{Schema: schemaResp.Schema, Raw: plan},
			}
			resp := resource.UpdateResponse{State: req.State}
			r.Update(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if calls != 1 {
				t.Fatalf("got %d readiness requests; want 1", calls)
			}
			var actual appInstanceResourceModel
			if diags := resp.State.Get(context.Background(), &actual); diags.HasError() {
				t.Fatal(diags)
			}
			if actual.Status != types.StringValue("running") || actual.Managed.ValueBool() != managed || !actual.WaitForReady.ValueBool() {
				t.Fatalf("readiness/ownership not preserved: status=%v managed=%v wait=%v", actual.Status, actual.Managed, actual.WaitForReady)
			}
		})
	}
}

func TestIdentityReadUsesHostWhenInventoryHasNotSynchronized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Errorf("identity read mutated Cloud: %s", req.Method)
		}
		switch req.URL.Path {
		case "/api/v1/apps/test/cvms":
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-aaa","name":"worker-1","status":"processing","instance_id":null}]`)
		case "/api/v1/cvms/vm-aaa/state":
			writeJSON(t, w, http.StatusOK, `{"instance_id":"host-instance"}`)
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	r := &appInstanceResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	var fields map[string]tftypes.Value
	if err := instanceStateValue("same").As(&fields); err != nil {
		t.Fatal(err)
	}
	fields["wait_for_identity"] = tftypes.NewValue(tftypes.Bool, true)
	prior := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(instanceObjectType(), fields)}
	resp := resource.ReadResponse{State: prior}
	r.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var actual appInstanceResourceModel
	if diags := resp.State.Get(context.Background(), &actual); diags.HasError() {
		t.Fatal(diags)
	}
	if actual.InstanceID.ValueString() != "host-instance" {
		t.Fatalf("lost authoritative identity: %v", actual.InstanceID)
	}
}
