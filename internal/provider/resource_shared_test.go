package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildComposeFile(t *testing.T) {
	t.Parallel()

	t.Run("minimal", func(t *testing.T) {
		cf := buildComposeFile(composeFileFields{
			Name:          "test-app",
			DockerCompose: "version: '3'\nservices:\n  app:\n    image: nginx",
		})
		if cf["name"] != "test-app" {
			t.Errorf("name = %v, want test-app", cf["name"])
		}
		if cf["docker_compose_file"] == nil {
			t.Error("docker_compose_file should be set")
		}
		if _, ok := cf["public_logs"]; ok {
			t.Error("public_logs should not be set when null")
		}
	})

	t.Run("all_fields", func(t *testing.T) {
		cf := buildComposeFile(composeFileFields{
			Name:            "test-app",
			DockerCompose:   "version: '3'",
			PreLaunchScript: types.StringValue("#!/bin/bash\necho hello"),
			PublicLogs:      types.BoolValue(true),
			PublicSysinfo:   types.BoolValue(false),
			PublicTCBInfo:   types.BoolValue(true),
			GatewayEnabled:  types.BoolValue(true),
			SecureTime:      types.BoolValue(false),
			StorageFS:       types.StringValue("zfs"),
			EnvKeys:         []string{"FOO", "BAR"},
		})
		if cf["pre_launch_script"] != "#!/bin/bash\necho hello" {
			t.Errorf("pre_launch_script = %v", cf["pre_launch_script"])
		}
		if cf["public_logs"] != true {
			t.Errorf("public_logs = %v", cf["public_logs"])
		}
		if cf["public_sysinfo"] != false {
			t.Errorf("public_sysinfo = %v", cf["public_sysinfo"])
		}
		if cf["storage_fs"] != "zfs" {
			t.Errorf("storage_fs = %v", cf["storage_fs"])
		}
		keys := cf["allowed_envs"].([]string)
		if len(keys) != 2 || keys[0] != "FOO" || keys[1] != "BAR" {
			t.Errorf("allowed_envs = %v", cf["allowed_envs"])
		}
	})

	t.Run("explicit_empty_env_keys", func(t *testing.T) {
		cf := buildComposeFile(composeFileFields{
			Name:          "test-app",
			DockerCompose: "version: '3'",
			EnvKeys:       []string{},
			HasEnvKeys:    true,
		})
		keys, ok := cf["allowed_envs"].([]string)
		if !ok {
			t.Fatalf("allowed_envs missing or wrong type: %#v", cf["allowed_envs"])
		}
		if len(keys) != 0 {
			t.Fatalf("allowed_envs = %v, want empty slice", keys)
		}
	})
}

func TestBuildComposeFileUpdateRequest(t *testing.T) {
	t.Parallel()

	req := buildComposeFileUpdateRequest(composeFileFields{
		Name:            "test-app",
		DockerCompose:   "version: '3'",
		PreLaunchScript: types.StringValue("#!/bin/sh\necho hi\n"),
		PublicLogs:      types.BoolValue(false),
		PublicSysinfo:   types.BoolValue(true),
		PublicTCBInfo:   types.BoolValue(false),
		GatewayEnabled:  types.BoolValue(true),
		SecureTime:      types.BoolValue(false),
		StorageFS:       types.StringValue("zfs"),
		EnvKeys:         []string{"API_KEY"},
		HasEnvKeys:      true,
	}, true)

	if req["name"] != "test-app" {
		t.Fatalf("name = %v", req["name"])
	}
	if req["docker_compose_file"] != "version: '3'" {
		t.Fatalf("docker_compose_file = %v", req["docker_compose_file"])
	}
	if req["pre_launch_script"] != "#!/bin/sh\necho hi\n" {
		t.Fatalf("pre_launch_script = %v", req["pre_launch_script"])
	}
	if req["public_logs"] != false || req["public_sysinfo"] != true || req["public_tcbinfo"] != false {
		t.Fatalf("unexpected visibility flags: %#v", req)
	}
	if req["gateway_enabled"] != true || req["secure_time"] != false {
		t.Fatalf("unexpected gateway/secure_time flags: %#v", req)
	}
	if req["storage_fs"] != "zfs" {
		t.Fatalf("storage_fs = %v", req["storage_fs"])
	}
	keys, ok := req["allowed_envs"].([]string)
	if !ok || len(keys) != 1 || keys[0] != "API_KEY" {
		t.Fatalf("allowed_envs = %#v", req["allowed_envs"])
	}
	if req["update_env_vars"] != true {
		t.Fatalf("update_env_vars = %v", req["update_env_vars"])
	}
}

func TestCreateNodeAndToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cvms" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 0 {
			if string(body["node"]) != `"physical-node"` {
				t.Errorf("node = %s", body["node"])
			}
			for _, key := range []string{"node_id", "teepod_id"} {
				if _, ok := body[key]; ok {
					t.Errorf("unexpected %s", key)
				}
			}
			_, _ = w.Write([]byte(`{"app_id":"app_test","compose_hash":"hash","token":"token"}`))
		} else {
			if string(body["token"]) != `"token"` {
				t.Errorf("token = %s", body["token"])
			}
			_, _ = w.Write([]byte(`{"id":"cvm_test"}`))
		}
		calls++
	}))
	defer server.Close()
	client, err := phala.NewClient(phala.WithAPIKey("test"), phala.WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildProvisionReq(provisionFields{Node: types.StringValue("physical-node")})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := client.ProvisionCVM(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := commitAndCreate(context.Background(), client, prepared, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestAutomaticPlacementRequestOmitsNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"node", "node_id", "teepod_id"} {
			if _, ok := body[key]; ok {
				t.Errorf("automatic placement sent %s", key)
			}
		}
		_, _ = w.Write([]byte(`{"app_id":"app_test","token":"token"}`))
	}))
	defer server.Close()
	client, err := phala.NewClient(phala.WithAPIKey("test"), phala.WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildProvisionReq(provisionFields{Node: types.StringNull()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ProvisionCVM(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestCommitCreateRequiresToken(t *testing.T) {
	_, err := commitAndCreate(context.Background(), nil, &phala.ProvisionCVMResponse{}, "", false, nil)
	if err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestBuildProvisionReq(t *testing.T) {
	t.Parallel()

	t.Run("optional node", func(t *testing.T) {
		for _, node := range []types.String{types.StringNull(), types.StringUnknown()} {
			req, err := buildProvisionReq(provisionFields{Node: node})
			if err != nil || req.Node != nil {
				t.Errorf("node %v: request = %#v, error = %v", node, req, err)
			}
		}
		if _, err := buildProvisionReq(provisionFields{Node: types.StringValue("  ")}); err == nil {
			t.Error("expected an error for empty node")
		}
	})

	t.Run("minimal", func(t *testing.T) {
		req, err := buildProvisionReq(provisionFields{
			Name:        "test-cvm",
			Size:        "tdx.small",
			ComposeFile: map[string]any{"name": "test"},
			KMS:         "phala",
			Listed:      false,
			Region:      types.StringNull(),
			Node:        types.StringValue("physical-node"),
			Image:       types.StringNull(),
			DiskSize:    types.Int64Null(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if req.Name != "test-cvm" {
			t.Errorf("name = %v", req.Name)
		}
		if req.InstanceType != "tdx.small" {
			t.Errorf("instance_type = %v", req.InstanceType)
		}
		if req.KMSType == nil || *req.KMSType != "PHALA" {
			t.Errorf("kms = %v", req.KMSType)
		}
		if req.Node == nil || *req.Node != "physical-node" {
			t.Errorf("node = %v", req.Node)
		}
	})

	t.Run("with_all_optional_fields", func(t *testing.T) {
		req, err := buildProvisionReq(provisionFields{
			Name:           "test-cvm",
			Size:           "tdx.large",
			ComposeFile:    map[string]any{"name": "test"},
			KMS:            "ethereum",
			Listed:         true,
			Region:         types.StringValue("us-east"),
			Node:           types.StringValue("physical-node"),
			Image:          types.StringValue("dstack-v0.3.5"),
			CustomAppID:    "custom123",
			HasCustomAppID: true,
			Nonce:          7,
			HasNonce:       true,
			DiskSize:       types.Int64Value(50),
		})
		if err != nil {
			t.Fatal(err)
		}
		if req.Node == nil || *req.Node != "physical-node" {
			t.Errorf("node = %v", req.Node)
		}
		if req.KMSType == nil || *req.KMSType != "ETHEREUM" {
			t.Errorf("kms = %v", req.KMSType)
		}
		if req.Image == nil || *req.Image != "dstack-v0.3.5" {
			t.Errorf("image = %v", req.Image)
		}
		if req.CustomAppID == nil || *req.CustomAppID != "custom123" {
			t.Errorf("app_id = %v", req.CustomAppID)
		}
		if req.Nonce == nil || *req.Nonce != 7 {
			t.Errorf("nonce = %v", req.Nonce)
		}
		if req.DiskSize == nil || *req.DiskSize != 50 {
			t.Errorf("disk_size = %v", req.DiskSize)
		}
	})
}

func TestParseEnvConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("null_env", func(t *testing.T) {
		cfg, diags := parseEnvConfig(ctx, types.MapNull(types.StringType), types.StringNull(), types.ListNull(types.StringType), types.StringNull(), types.StringNull(), true)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if cfg.HasAutoEnv {
			t.Error("HasAutoEnv should be false")
		}
	})

	t.Run("auto_env_on_create", func(t *testing.T) {
		envMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"FOO": "bar", "BAZ": "qux"})
		cfg, diags := parseEnvConfig(ctx, envMap, types.StringNull(), types.ListNull(types.StringType), types.StringNull(), types.StringNull(), true)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if !cfg.HasAutoEnv {
			t.Error("HasAutoEnv should be true")
		}
		if len(cfg.EffectiveEnvKeys) != 2 {
			t.Errorf("EffectiveEnvKeys = %v", cfg.EffectiveEnvKeys)
		}
		// Keys should be sorted
		if cfg.EffectiveEnvKeys[0] != "BAZ" || cfg.EffectiveEnvKeys[1] != "FOO" {
			t.Errorf("EffectiveEnvKeys not sorted: %v", cfg.EffectiveEnvKeys)
		}
	})

	t.Run("conflict_auto_and_manual", func(t *testing.T) {
		envMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"FOO": "bar"})
		_, diags := parseEnvConfig(ctx, envMap, types.StringValue("deadbeef"), types.ListNull(types.StringType), types.StringNull(), types.StringNull(), true)
		if !diags.HasError() {
			t.Error("expected conflict error")
		}
	})

	t.Run("phase2_on_create_rejected", func(t *testing.T) {
		_, diags := parseEnvConfig(ctx, types.MapNull(types.StringType), types.StringValue("encrypted"), types.ListNull(types.StringType), types.StringValue("hash"), types.StringValue("tx"), true)
		if !diags.HasError() {
			t.Error("expected create-time phase-2 rejection")
		}
	})
}

func TestComposeSettingsChanged(t *testing.T) {
	t.Parallel()

	a := composeSettingsValues{
		PublicLogs:     types.BoolValue(true),
		PublicSysinfo:  types.BoolValue(false),
		PublicTCBInfo:  types.BoolValue(true),
		GatewayEnabled: types.BoolValue(false),
		SecureTime:     types.BoolValue(false),
	}
	b := a // same values
	if a.changed(b) {
		t.Error("identical settings should not be changed")
	}

	c := a
	c.PublicLogs = types.BoolValue(false)
	if !a.changed(c) {
		t.Error("different PublicLogs should be changed")
	}
}

func TestDiskSizeValidation(t *testing.T) {
	t.Parallel()

	t.Run("grow_allowed", func(t *testing.T) {
		changed, diags := diskSizeValidation(types.Int64Value(50), types.Int64Value(20))
		if diags.HasError() {
			t.Errorf("unexpected error: %v", diags)
		}
		if !changed {
			t.Error("should be changed")
		}
	})

	t.Run("shrink_rejected", func(t *testing.T) {
		_, diags := diskSizeValidation(types.Int64Value(10), types.Int64Value(20))
		if !diags.HasError() {
			t.Error("shrink should be rejected")
		}
	})

	t.Run("null_unchanged", func(t *testing.T) {
		changed, diags := diskSizeValidation(types.Int64Null(), types.Int64Value(20))
		if diags.HasError() {
			t.Errorf("unexpected error: %v", diags)
		}
		if changed {
			t.Error("null should not be changed")
		}
	})
}

func TestPollInterval(t *testing.T) {
	t.Parallel()

	base := 4 * time.Second
	min := base - base/4
	max := base + base/4

	for i := 0; i < 100; i++ {
		d := pollInterval(base)
		if d < min || d > max {
			t.Errorf("pollInterval(%v) = %v, want [%v, %v]", base, d, min, max)
		}
	}
}

func TestInheritOptionalString(t *testing.T) {
	t.Parallel()

	result := inheritOptionalString(types.StringNull(), types.StringValue("fallback"))
	if result.ValueString() != "fallback" {
		t.Errorf("expected fallback, got %v", result)
	}

	result = inheritOptionalString(types.StringValue("override"), types.StringValue("fallback"))
	if result.ValueString() != "override" {
		t.Errorf("expected override, got %v", result)
	}
}

func TestInheritOptionalBool(t *testing.T) {
	t.Parallel()

	result := inheritOptionalBool(types.BoolNull(), types.BoolValue(true))
	if result.ValueBool() != true {
		t.Errorf("expected true, got %v", result)
	}

	result = inheritOptionalBool(types.BoolValue(false), types.BoolValue(true))
	if result.ValueBool() != false {
		t.Errorf("expected false, got %v", result)
	}
}

func TestEnvConfigBuildUpdateReq(t *testing.T) {
	t.Parallel()

	t.Run("auto_env", func(t *testing.T) {
		cfg := &envConfig{
			HasAutoEnv:         true,
			EffectiveEncrypted: "deadbeef",
			EffectiveEnvKeys:   []string{"FOO", "BAR"},
			EnvComposeHash:     "hash123",
			EnvTransactionHash: "tx456",
		}
		req, err := cfg.buildEnvUpdateReq(types.ListNull(types.StringType))
		if err != nil {
			t.Fatal(err)
		}
		if req["encrypted_env"] != "deadbeef" {
			t.Errorf("encrypted_env = %v", req["encrypted_env"])
		}
		if req["compose_hash"] != "hash123" {
			t.Errorf("compose_hash = %v", req["compose_hash"])
		}
		if req["transaction_hash"] != "tx456" {
			t.Errorf("transaction_hash = %v", req["transaction_hash"])
		}
	})

	t.Run("manual_without_encrypted_fails", func(t *testing.T) {
		cfg := &envConfig{
			HasAutoEnv:         false,
			HasManualEncrypted: false,
		}
		_, err := cfg.buildEnvUpdateReq(types.ListNull(types.StringType))
		if err == nil {
			t.Error("expected error for missing encrypted_env")
		}
	})
}
