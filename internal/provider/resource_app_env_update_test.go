package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestAppResourceEnvAttributeNotSensitive asserts that the `env` map attribute
// is NOT marked Sensitive at the schema level. Marking the entire attribute
// Sensitive interacts with element-level marks coming from sensitive
// Terraform variables and causes Terraform Core to suppress in-place env
// diffs (Phala-Network/phala-cloud#246). Sensitivity should be opted into
// per-value at the variable level instead.
func TestAppResourceEnvAttributeNotSensitive(t *testing.T) {
	r := NewAppResource()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	envAttr, ok := resp.Schema.Attributes["env"].(rschema.MapAttribute)
	if !ok {
		t.Fatalf("env attribute not a MapAttribute: %T", resp.Schema.Attributes["env"])
	}
	if envAttr.Sensitive {
		t.Fatal("env MapAttribute must NOT be Sensitive at the schema level " +
			"(see Phala-Network/phala-cloud#246: schema-level Sensitive on a Map " +
			"causes Terraform Core to suppress in-place diffs when sensitive " +
			"variable values flow into elements). Mark sensitive values at the " +
			"variable level instead.")
	}
}

// TestAppResourceEnvUpdateTriggersEnvsPATCH end-to-end exercises the bug at
// Phala-Network/phala-cloud#246: a phala_app resource with an existing env
// value gets a single env-key value change in config; the test asserts that
// the framework's PlanResourceChange surfaces the diff and that
// ApplyResourceChange actually calls PATCH /cvms/{id}/envs.
//
// This guards against regressions in either the Update handler or the schema
// (e.g. re-introducing Sensitive: true on the env attribute, which suppresses
// the diff at Terraform Core level).
func TestAppResourceEnvUpdateTriggersEnvsPATCH(t *testing.T) {
	ctx := context.Background()

	var envPatchCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && path == "/api/v1/cvms/provision":
			writeJSON(t, w, http.StatusOK, `{"app_id":"app_test","compose_hash":"abc","app_env_encrypt_pubkey":"`+envUpdateTestPubkey+`"}`)
		case r.Method == http.MethodPost && path == "/api/v1/cvms":
			writeJSON(t, w, http.StatusOK, envUpdateCVMResponse)
		case r.Method == http.MethodGet && path == "/api/v1/apps/test":
			writeJSON(t, w, http.StatusOK, `{"app_id":"app_test","name":"demo","cvms":[`+envUpdateCVMResponse+`]}`)
		case r.Method == http.MethodGet && path == "/api/v1/apps/test/cvms":
			writeJSON(t, w, http.StatusOK, `[`+envUpdateCVMResponse+`]`)
		case r.Method == http.MethodGet && path == "/api/v1/cvms/cvm123":
			writeJSON(t, w, http.StatusOK, envUpdateCVMResponse)
		case r.Method == http.MethodGet && path == "/api/v1/cvms/cvm123/docker-compose.yml":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "services:\n  app:\n")
		case r.Method == http.MethodGet && path == "/api/v1/cvms/cvm123/pre-launch-script":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && path == "/api/v1/cvms/cvm123/envs":
			envPatchCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Logf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	server := providerserver.NewProtocol6(New("test")())()

	provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"api_key":         tftypes.String,
		"api_prefix":      tftypes.String,
		"api_version":     tftypes.String,
		"timeout_seconds": tftypes.Number,
	}}
	provVal := tftypes.NewValue(provType, map[string]tftypes.Value{
		"api_key":         tftypes.NewValue(tftypes.String, "phat_test_key"),
		"api_prefix":      tftypes.NewValue(tftypes.String, srv.URL+"/api/v1"),
		"api_version":     tftypes.NewValue(tftypes.String, "2026-01-21"),
		"timeout_seconds": tftypes.NewValue(tftypes.Number, 5),
	})
	provDV, err := tfprotov6.NewDynamicValue(provType, provVal)
	if err != nil {
		t.Fatalf("provDV: %v", err)
	}
	if _, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &provDV}); err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}

	objType := envUpdateAppObjectType()

	// Step 1: Create with env={IMAGE: v1}.
	configV1 := envUpdateConfigValue(t, "v1")
	configV1DV, _ := tfprotov6.NewDynamicValue(objType, configV1)
	priorNullDV, _ := tfprotov6.NewDynamicValue(objType, tftypes.NewValue(objType, nil))
	proposedCreateDV, _ := tfprotov6.NewDynamicValue(objType, envUpdateProposedCreate(t, "v1"))

	plan1, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "phala_app",
		PriorState:       &priorNullDV,
		ProposedNewState: &proposedCreateDV,
		Config:           &configV1DV,
	})
	if err != nil {
		t.Fatalf("plan1: %v", err)
	}
	apply1, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "phala_app",
		PriorState:   &priorNullDV,
		PlannedState: plan1.PlannedState,
		Config:       &configV1DV,
	})
	if err != nil {
		t.Fatalf("apply1: %v", err)
	}
	for _, d := range apply1.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("apply1 error: %s — %s", d.Summary, d.Detail)
		}
	}

	createdState, _ := apply1.NewState.Unmarshal(objType)
	var createdAttrs map[string]tftypes.Value
	if err := createdState.As(&createdAttrs); err != nil {
		t.Fatalf("unmarshal created state: %v", err)
	}

	// Step 2: Plan an update with env={IMAGE: v2}.
	configV2 := envUpdateConfigValue(t, "v2")
	configV2DV, _ := tfprotov6.NewDynamicValue(objType, configV2)

	overlay := make(map[string]tftypes.Value, len(createdAttrs))
	for k, v := range createdAttrs {
		overlay[k] = v
	}
	overlay["env"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"IMAGE": tftypes.NewValue(tftypes.String, "v2"),
	})
	proposedUpdateDV, _ := tfprotov6.NewDynamicValue(objType, tftypes.NewValue(objType, overlay))

	plan2, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "phala_app",
		PriorState:       apply1.NewState,
		ProposedNewState: &proposedUpdateDV,
		Config:           &configV2DV,
	})
	if err != nil {
		t.Fatalf("plan2: %v", err)
	}

	plannedV2, _ := plan2.PlannedState.Unmarshal(objType)
	if createdState.Equal(plannedV2) {
		t.Fatal("regression: prior state equals planned state — Terraform Core would report 'No changes.' (Phala-Network/phala-cloud#246)")
	}
	var plannedAttrs map[string]tftypes.Value
	_ = plannedV2.As(&plannedAttrs)
	if createdAttrs["env"].Equal(plannedAttrs["env"]) {
		t.Fatalf("regression: planned env %s equals prior env %s", plannedAttrs["env"], createdAttrs["env"])
	}

	// Step 3: Apply the update; assert /envs PATCH fires.
	apply2, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "phala_app",
		PriorState:   apply1.NewState,
		PlannedState: plan2.PlannedState,
		Config:       &configV2DV,
	})
	if err != nil {
		t.Fatalf("apply2: %v", err)
	}
	for _, d := range apply2.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("apply2 error: %s — %s", d.Summary, d.Detail)
		}
	}

	if got := envPatchCalls.Load(); got == 0 {
		t.Fatal("regression: env PATCH was never called even though config env changed v1 -> v2")
	}
	if got := envPatchCalls.Load(); got != 1 {
		t.Fatalf("regression: expected exactly 1 env PATCH after v1->v2 update, got %d", got)
	}

	// Step 4: re-plan + re-apply with the SAME config (env still v2). No env
	// PATCH should fire — guarding against a regression where dropping the
	// schema-level Sensitive flag spuriously triggers env replace on every
	// apply.
	createdV2State, _ := apply2.NewState.Unmarshal(objType)
	var createdV2Attrs map[string]tftypes.Value
	_ = createdV2State.As(&createdV2Attrs)

	overlay2 := make(map[string]tftypes.Value, len(createdV2Attrs))
	for k, v := range createdV2Attrs {
		overlay2[k] = v
	}
	overlay2["env"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"IMAGE": tftypes.NewValue(tftypes.String, "v2"),
	})
	proposedNoopDV, _ := tfprotov6.NewDynamicValue(objType, tftypes.NewValue(objType, overlay2))

	plan3, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "phala_app",
		PriorState:       apply2.NewState,
		ProposedNewState: &proposedNoopDV,
		Config:           &configV2DV,
	})
	if err != nil {
		t.Fatalf("plan3: %v", err)
	}
	apply3, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "phala_app",
		PriorState:   apply2.NewState,
		PlannedState: plan3.PlannedState,
		Config:       &configV2DV,
	})
	if err != nil {
		t.Fatalf("apply3: %v", err)
	}
	for _, d := range apply3.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("apply3 error: %s — %s", d.Summary, d.Detail)
		}
	}
	if got := envPatchCalls.Load(); got != 1 {
		t.Fatalf("regression: env PATCH fired on a no-op apply (env unchanged); call count went from 1 to %d", got)
	}
}

const envUpdateTestPubkey = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"

const envUpdateCVMResponse = `{"vm_uuid":"cvm123","status":"running","name":"demo","instance_type":"tdx.small","kms":"phala","listed":false,"image_name":"ubuntu-24.04","disk_size":20,"region":"US-WEST-1","public_endpoint":"https://example.com","public_logs":false,"public_sysinfo":false,"public_tcbinfo":false,"gateway_enabled":false,"secure_time":false,"storage_fs":"ext4","encrypted_env_pubkey":"` + envUpdateTestPubkey + `"}`

func envUpdateAppObjectType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id":                   tftypes.String,
		"app_id":               tftypes.String,
		"name":                 tftypes.String,
		"region":               tftypes.String,
		"size":                 tftypes.String,
		"image":                tftypes.String,
		"kms":                  tftypes.String,
		"node_id":              tftypes.Number,
		"custom_app_id":        tftypes.String,
		"nonce":                tftypes.Number,
		"public_logs":          tftypes.Bool,
		"public_sysinfo":       tftypes.Bool,
		"public_tcbinfo":       tftypes.Bool,
		"gateway_enabled":      tftypes.Bool,
		"secure_time":          tftypes.Bool,
		"storage_fs":           tftypes.String,
		"disk_size":            tftypes.Number,
		"docker_compose":       tftypes.String,
		"pre_launch_script":    tftypes.String,
		"ssh_authorized_keys":  tftypes.List{ElementType: tftypes.String},
		"env":                  tftypes.Map{ElementType: tftypes.String},
		"encrypted_env":        tftypes.String,
		"env_keys":             tftypes.List{ElementType: tftypes.String},
		"env_compose_hash":     tftypes.String,
		"env_transaction_hash": tftypes.String,
		"listed":               tftypes.Bool,
		"replicas":             tftypes.Number,
		"wait_for_ready":       tftypes.Bool,
		"wait_timeout_seconds": tftypes.Number,
		"status":               tftypes.String,
		"primary_cvm_id":       tftypes.String,
		"cvm_ids":              tftypes.List{ElementType: tftypes.String},
		"instances":            tftypes.List{ElementType: envUpdateInstanceObjectType()},
		"endpoint":             tftypes.String,
	}}
}

func envUpdateInstanceObjectType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id":            tftypes.String,
		"app_id":        tftypes.String,
		"name":          tftypes.String,
		"vm_uuid":       tftypes.String,
		"instance_id":   tftypes.String,
		"status":        tftypes.String,
		"region":        tftypes.String,
		"instance_type": tftypes.String,
		"endpoint":      tftypes.String,
		"created_at":    tftypes.String,
	}}
}

func envUpdateConfigValue(t *testing.T, imageVal string) tftypes.Value {
	t.Helper()
	objType := envUpdateAppObjectType()
	envTfVal := tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"IMAGE": tftypes.NewValue(tftypes.String, imageVal),
	})
	return tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":                   tftypes.NewValue(tftypes.String, nil),
		"app_id":               tftypes.NewValue(tftypes.String, nil),
		"name":                 tftypes.NewValue(tftypes.String, "env-drift-demo"),
		"region":               tftypes.NewValue(tftypes.String, "US-WEST-1"),
		"size":                 tftypes.NewValue(tftypes.String, "tdx.small"),
		"image":                tftypes.NewValue(tftypes.String, nil),
		"kms":                  tftypes.NewValue(tftypes.String, nil),
		"node_id":              tftypes.NewValue(tftypes.Number, nil),
		"custom_app_id":        tftypes.NewValue(tftypes.String, nil),
		"nonce":                tftypes.NewValue(tftypes.Number, nil),
		"public_logs":          tftypes.NewValue(tftypes.Bool, nil),
		"public_sysinfo":       tftypes.NewValue(tftypes.Bool, nil),
		"public_tcbinfo":       tftypes.NewValue(tftypes.Bool, nil),
		"gateway_enabled":      tftypes.NewValue(tftypes.Bool, nil),
		"secure_time":          tftypes.NewValue(tftypes.Bool, nil),
		"storage_fs":           tftypes.NewValue(tftypes.String, "ext4"),
		"disk_size":            tftypes.NewValue(tftypes.Number, 20),
		"docker_compose":       tftypes.NewValue(tftypes.String, "services:\n  app:\n"),
		"pre_launch_script":    tftypes.NewValue(tftypes.String, nil),
		"ssh_authorized_keys":  tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
		"env":                  envTfVal,
		"encrypted_env":        tftypes.NewValue(tftypes.String, nil),
		"env_keys":             tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
		"env_compose_hash":     tftypes.NewValue(tftypes.String, nil),
		"env_transaction_hash": tftypes.NewValue(tftypes.String, nil),
		"listed":               tftypes.NewValue(tftypes.Bool, nil),
		"replicas":             tftypes.NewValue(tftypes.Number, 1),
		"wait_for_ready":       tftypes.NewValue(tftypes.Bool, false),
		"wait_timeout_seconds": tftypes.NewValue(tftypes.Number, 600),
		"status":               tftypes.NewValue(tftypes.String, nil),
		"primary_cvm_id":       tftypes.NewValue(tftypes.String, nil),
		"cvm_ids":              tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
		"instances":            tftypes.NewValue(tftypes.List{ElementType: envUpdateInstanceObjectType()}, nil),
		"endpoint":             tftypes.NewValue(tftypes.String, nil),
	})
}

func envUpdateProposedCreate(t *testing.T, imageVal string) tftypes.Value {
	t.Helper()
	objType := envUpdateAppObjectType()
	envTfVal := tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"IMAGE": tftypes.NewValue(tftypes.String, imageVal),
	})
	return tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":                   tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"app_id":               tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":                 tftypes.NewValue(tftypes.String, "env-drift-demo"),
		"region":               tftypes.NewValue(tftypes.String, "US-WEST-1"),
		"size":                 tftypes.NewValue(tftypes.String, "tdx.small"),
		"image":                tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"kms":                  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"node_id":              tftypes.NewValue(tftypes.Number, nil),
		"custom_app_id":        tftypes.NewValue(tftypes.String, nil),
		"nonce":                tftypes.NewValue(tftypes.Number, nil),
		"public_logs":          tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"public_sysinfo":       tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"public_tcbinfo":       tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"gateway_enabled":      tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"secure_time":          tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"storage_fs":           tftypes.NewValue(tftypes.String, "ext4"),
		"disk_size":            tftypes.NewValue(tftypes.Number, 20),
		"docker_compose":       tftypes.NewValue(tftypes.String, "services:\n  app:\n"),
		"pre_launch_script":    tftypes.NewValue(tftypes.String, nil),
		"ssh_authorized_keys":  tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
		"env":                  envTfVal,
		"encrypted_env":        tftypes.NewValue(tftypes.String, nil),
		"env_keys":             tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
		"env_compose_hash":     tftypes.NewValue(tftypes.String, nil),
		"env_transaction_hash": tftypes.NewValue(tftypes.String, nil),
		"listed":               tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue),
		"replicas":             tftypes.NewValue(tftypes.Number, 1),
		"wait_for_ready":       tftypes.NewValue(tftypes.Bool, false),
		"wait_timeout_seconds": tftypes.NewValue(tftypes.Number, 600),
		"status":               tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"primary_cvm_id":       tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"cvm_ids":              tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, tftypes.UnknownValue),
		"instances":            tftypes.NewValue(tftypes.List{ElementType: envUpdateInstanceObjectType()}, tftypes.UnknownValue),
		"endpoint":             tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
}
