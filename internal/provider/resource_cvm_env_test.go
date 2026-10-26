package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func cvmEnvTestPlan(t *testing.T, r *cvmEnvResource, model cvmEnvResourceModel) tfsdk.Plan {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(context.Background(), model); d.HasError() {
		t.Fatal(d)
	}
	return plan
}

func cvmEnvTestModel(t *testing.T) cvmEnvResourceModel {
	t.Helper()
	env, d := types.MapValueFrom(context.Background(), types.StringType, map[string]string{"BROKERS": "https://peer.example"})
	if d.HasError() {
		t.Fatal(d)
	}
	return cvmEnvResourceModel{
		ID: types.StringNull(), CVMID: types.StringValue("vm-aaa"), Env: env,
		WaitForReady: types.BoolValue(true), WaitTimeoutSecond: types.Int64Value(5), Status: types.StringNull(),
	}
}

func TestCVMEnvLifecycleDoesNotChangeMeasuredComposeOrDeleteCVM(t *testing.T) {
	ctx := context.Background()
	var patches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "GET" && req.URL.Path == "/api/v1/cvms/vm-aaa":
			writeJSON(t, w, 200, `{"vm_uuid":"vm-aaa","status":"running","encrypted_env_pubkey":"`+envUpdateTestPubkey+`"}`)
		case req.Method == "GET" && req.URL.Path == "/api/v1/cvms/vm-aaa/compose_file":
			writeJSON(t, w, 200, `{"allowed_envs":["BROKERS"],"manifest_version":3,"key_provider_info":{"id":"kms-identity"},"future_field":"must-not-rewrite"}`)
		case req.Method == "PATCH" && req.URL.Path == "/api/v1/cvms/vm-aaa/envs":
			patches.Add(1)
			var body map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 1 || body["encrypted_env"] == nil || strings.Contains(string(body["encrypted_env"]), "peer.example") {
				t.Errorf("expected encrypted values only, got %v", body)
			}
			writeJSON(t, w, 202, `{"status":"in_progress"}`)
		default:
			t.Errorf("unexpected mutation/request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	r := &cvmEnvResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
	model := cvmEnvTestModel(t)
	plan := cvmEnvTestPlan(t, r, model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var actual cvmEnvResourceModel
	if d := created.State.Get(ctx, &actual); d.HasError() {
		t.Fatal(d)
	}
	if actual.ID.ValueString() != "vm-aaa" || actual.Status.ValueString() != "running" || !actual.Env.Equal(model.Env) {
		t.Fatalf("invalid state: %+v", actual)
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	// Readiness-only changes must not send a second payload or restart.
	actual.WaitTimeoutSecond = types.Int64Value(10)
	updatePlan := cvmEnvTestPlan(t, r, actual)
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: read.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	// Changing an environment value must PATCH once, in place.
	actual.Env, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{"BROKERS": "https://new-peer.example"})
	updatedPlan := cvmEnvTestPlan(t, r, actual)
	changed := resource.UpdateResponse{State: updated.State}
	r.Update(ctx, resource.UpdateRequest{Plan: updatedPlan, State: updated.State}, &changed)
	if changed.Diagnostics.HasError() {
		t.Fatal(changed.Diagnostics)
	}
	r.Delete(ctx, resource.DeleteRequest{State: changed.State}, &resource.DeleteResponse{})
	if patches.Load() != 2 {
		t.Fatalf("got %d mutations; want create + value update only", patches.Load())
	}
}

func TestCVMEnvRejectsInvalidConfigurationBeforeMutation(t *testing.T) {
	for _, test := range []string{"unknown-env", "null-env", "unknown-value", "null-value", "unknown-id", "empty-id", "invalid-timeout", "whitespace-key", "forbidden-key", "missing-key"} {
		t.Run(test, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "GET" {
					t.Errorf("invalid configuration mutated Cloud: %s", req.Method)
				}
				switch req.URL.Path {
				case "/api/v1/cvms/vm-aaa":
					key := envUpdateTestPubkey
					if test == "missing-key" {
						key = ""
					}
					writeJSON(t, w, 200, `{"status":"running","encrypted_env_pubkey":"`+key+`"}`)
				case "/api/v1/cvms/vm-aaa/compose_file":
					writeJSON(t, w, 200, `{"allowed_envs":["BROKERS"]}`)
				default:
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			r := &cvmEnvResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
			model := cvmEnvTestModel(t)
			switch test {
			case "unknown-env":
				model.Env = types.MapUnknown(types.StringType)
			case "null-env":
				model.Env = types.MapNull(types.StringType)
			case "unknown-value":
				model.Env, _ = types.MapValue(types.StringType, map[string]attr.Value{"BROKERS": types.StringUnknown()})
			case "null-value":
				model.Env, _ = types.MapValue(types.StringType, map[string]attr.Value{"BROKERS": types.StringNull()})
			case "unknown-id":
				model.CVMID = types.StringUnknown()
			case "empty-id":
				model.CVMID = types.StringValue("")
			case "invalid-timeout":
				model.WaitTimeoutSecond = types.Int64Value(0)
			case "whitespace-key":
				model.Env, _ = types.MapValueFrom(context.Background(), types.StringType, map[string]string{" BROKERS": "value"})
			case "forbidden-key":
				model.Env, _ = types.MapValueFrom(context.Background(), types.StringType, map[string]string{"NOT_ALLOWED": "value"})
			}
			diags, accepted := r.apply(context.Background(), &model, true)
			if !diags.HasError() || accepted {
				t.Fatalf("configuration accepted: %v", diags)
			}
		})
	}
}

func TestCVMEnvCancellationWhileBusyDoesNotMutate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Errorf("mutated busy target: %s", req.Method)
		}
		writeJSON(t, w, 200, `{"status":"running","in_progress":true}`)
	}))
	defer server.Close()
	r := &cvmEnvResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
	model := cvmEnvTestModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	diags, accepted := r.apply(ctx, &model, true)
	if !diags.HasError() || accepted {
		t.Fatalf("cancelled update accepted: %v", diags)
	}
}

func TestAppAllowsMeasuredKeysWithoutOwningValues(t *testing.T) {
	ctx := context.Background()
	keys, _ := types.ListValueFrom(ctx, types.StringType, []string{"BROKERS"})
	for _, create := range []bool{true, false} {
		cfg, diags := parseEnvConfig(ctx, types.MapNull(types.StringType), types.StringNull(), keys, types.StringNull(), types.StringNull(), create)
		if diags.HasError() || cfg.HasAutoEnv || cfg.HasEffectiveEncrypted || len(cfg.EffectiveEnvKeys) != 1 {
			t.Fatalf("allowlist-only configuration failed: cfg=%+v diags=%v", cfg, diags)
		}
	}
}

func TestInstanceRelinquishesInlineEnvWithoutMutation(t *testing.T) {
	ctx := context.Background()
	r := &appInstanceResource{}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema, Raw: instanceStateValue("existing-value")}
	var model appInstanceResourceModel
	if d := state.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	model.Env = types.MapNull(types.StringType)
	model.WaitForReady = types.BoolValue(false)
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.UpdateResponse{State: state}
	// A nil client is intentional: this ownership-only change cannot call
	// Cloud, let alone erase the old value or replace the CVM.
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var actual appInstanceResourceModel
	if d := resp.State.Get(ctx, &actual); d.HasError() {
		t.Fatal(d)
	}
	if !actual.Env.IsNull() || actual.VMUUID != model.VMUUID || actual.Managed != model.Managed {
		t.Fatal("handover changed the CVM binding or retained inline ownership")
	}
}

func TestCVMEnvFailedUpdateDoesNotPersistUnappliedValues(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "PATCH":
			writeJSON(t, w, 403, `{"detail":"permission denied"}`)
		case strings.HasSuffix(req.URL.Path, "/compose_file"):
			writeJSON(t, w, 200, `{"allowed_envs":["BROKERS"]}`)
		default:
			writeJSON(t, w, 200, `{"status":"running","encrypted_env_pubkey":"`+envUpdateTestPubkey+`"}`)
		}
	}))
	defer server.Close()
	r := &cvmEnvResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
	model := cvmEnvTestModel(t)
	model.ID = model.CVMID
	model.Status = types.StringValue("running")
	priorPlan := cvmEnvTestPlan(t, r, model)
	state := tfsdk.State{Schema: priorPlan.Schema, Raw: priorPlan.Raw}
	model.Env, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{"BROKERS": "unapplied-value"})
	plan := cvmEnvTestPlan(t, r, model)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
		t.Fatal("failed update lost the persisted state or recorded unapplied values")
	}
}

func TestCVMEnvAcceptedUpdateRetainsBindingOnReadinessFailure(t *testing.T) {
	ctx := context.Background()
	var patched atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "PATCH":
			patched.Store(true)
			writeJSON(t, w, 202, `{"status":"in_progress"}`)
		case strings.HasSuffix(req.URL.Path, "/compose_file"):
			writeJSON(t, w, 200, `{"allowed_envs":["BROKERS"]}`)
		default:
			status := "running"
			if patched.Load() {
				status = "stopped"
			}
			writeJSON(t, w, 200, `{"status":"`+status+`","encrypted_env_pubkey":"`+envUpdateTestPubkey+`"}`)
		}
	}))
	defer server.Close()
	r := &cvmEnvResource{client: newTestPhalaClient(t, server.URL+"/api/v1")}
	model := cvmEnvTestModel(t)
	plan := cvmEnvTestPlan(t, r, model)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("stopped CVM was reported ready")
	}
	var actual cvmEnvResourceModel
	if d := resp.State.Get(ctx, &actual); d.HasError() {
		t.Fatal(d)
	}
	if actual.ID != model.CVMID || !actual.Env.Equal(model.Env) {
		t.Fatal("accepted update was not retained after readiness failure")
	}
}
