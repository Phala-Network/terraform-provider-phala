package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Exercise Terraform Core, not just the resource methods: the environment
// map is sensitive and contains identifiers unknown until the same apply
// allocates every slot. A three-request barrier proves the updates overlap.
func TestCVMEnvSingleTerraformApplyAndSensitiveUpdate(t *testing.T) {
	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform CLI is required for the Core integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(pluginDir, "terraform-provider-phala"), ".")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, output)
	}
	var mu sync.Mutex
	instances := map[string]map[string]any{}
	patches := 0
	allocationCount := 0
	round := make(chan struct{})
	info := func(name string) map[string]any {
		return map[string]any{
			"id": "cvm-" + name, "vm_uuid": "vm-" + name, "name": name,
			"instance_id": "instance-" + name, "app_id": "app_test", "status": "processing",
			"encrypted_env_pubkey": envUpdateTestPubkey, "compose_hash": "abc",
			"os":       map[string]any{"name": "test-os"},
			"resource": map[string]any{"instance_type": "tdx.small"},
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		write := func(body any) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(body); err != nil {
				t.Error(err)
			}
		}
		list := func() []map[string]any {
			var names []string
			for name := range instances {
				names = append(names, name)
			}
			sort.Strings(names)
			out := make([]map[string]any, 0, len(names))
			for _, name := range names {
				out = append(out, instances[name])
			}
			return out
		}
		p := strings.TrimPrefix(req.URL.Path, "/api/v1")
		switch {
		case req.Method == "POST" && p == "/cvms":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if raw := body["compose_file"]; raw != nil {
				var compose struct {
					AllowedEnvs []string `json:"allowed_envs"`
				}
				if err := json.Unmarshal(raw, &compose); err != nil || len(compose.AllowedEnvs) != 2 {
					t.Errorf("missing measured allowlist: %s (%v)", raw, err)
				}
				write(map[string]any{"app_id": "app_test", "compose_hash": "abc"})
			} else {
				if body["encrypted_env"] != nil {
					t.Error("allocation must not own runtime values")
				}
				instances["coord-0"] = info("coord-0")
				allocationCount++
				write(instances["coord-0"])
			}
		case req.Method == "GET" && p == "/apps/test":
			write(map[string]any{"app_id": "app_test", "name": "coord-0", "cvms": list()})
		case req.Method == "GET" && p == "/apps/test/cvms":
			write(list())
		case req.Method == "POST" && p == "/apps/test/instances":
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			instances[body.Name] = info(body.Name)
			allocationCount++
			write(instances[body.Name])
		case req.Method == "GET" && strings.HasSuffix(p, "/compose_file"):
			write(map[string]any{"allowed_envs": []string{"BROKERS", "SECRET"}})
		case req.Method == "GET" && strings.HasPrefix(p, "/cvms/"):
			name := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(p, "/cvms/"), "vm-"), "cvm-")
			if current := instances[name]; current != nil {
				write(current)
			} else {
				t.Errorf("unknown target: %s", p)
				w.WriteHeader(404)
			}
		case req.Method == "PATCH" && strings.HasSuffix(p, "/envs"):
			var body map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body) != 1 || body["encrypted_env"] == nil {
				t.Errorf("not a value-only update: %s %v", p, body)
			}
			patches++
			thisRound := round
			if patches%3 == 0 {
				for _, current := range instances {
					current["status"] = "running"
				}
				close(round)
				round = make(chan struct{})
			}
			// Do not hold the inventory lock while waiting for all three
			// independent requests to be in flight.
			mu.Unlock()
			select {
			case <-thisRound:
			case <-req.Context().Done():
			}
			mu.Lock()
			write(map[string]any{"status": "in_progress"})
		default:
			t.Errorf("unexpected API request: %s %s", req.Method, p)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	config := fmt.Sprintf(`terraform {
  required_providers {
    phala = { source = "phala-network/phala" }
  }
}
provider "phala" {
  api_key = "integration-test"
  api_prefix = %q
}
variable "secret" {
  type = string
  sensitive = true
}
resource "phala_app" "cluster" {
  name = "coord-0"
  members = ["coord-0", "coord-1", "coord-2"]
  size = "tdx.small"
  docker_compose = "services:\n  app:\n    image: nginx\n"
  env_keys = ["BROKERS", "SECRET"]
  wait_for_ready = false
}
resource "phala_app_instance" "peer" {
  for_each = toset(["coord-0", "coord-1", "coord-2"])
  name = each.key
  app_id = phala_app.cluster.app_id
  wait_for_ready = false
  wait_for_identity = true
}
resource "phala_cvm_env" "peer" {
  for_each = toset(["coord-0", "coord-1", "coord-2"])
  cvm_id = phala_app_instance.peer[each.key].vm_uuid
  env = {
    BROKERS = jsonencode([for peer in phala_app_instance.peer : peer.instance_id])
    SECRET = var.secret
  }
}
`, server.URL+"/api/v1")
	cliConfig := fmt.Sprintf("provider_installation {\n  dev_overrides {\n    \"phala-network/phala\" = %q\n  }\n  direct {}\n}\n", pluginDir)
	for name, body := range map[string]string{"main.tf": config, "terraformrc": cliConfig} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, terraform, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+filepath.Join(dir, "terraformrc"), "TF_DATA_DIR="+filepath.Join(dir, ".terraform"), "TF_CLI_ARGS=", "TF_CLI_ARGS_apply=", "TF_CLI_ARGS_plan=", "PHALA_CLOUD_REQUESTS_PER_MINUTE=60000")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("terraform %v: %v\n%s", args, err, output)
		}
	}
	run("apply", "-auto-approve", "-input=false", "-no-color", "-var=secret=first")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color", "-var=secret=first")
	run("apply", "-auto-approve", "-input=false", "-no-color", "-var=secret=rotated")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color", "-var=secret=rotated")
	mu.Lock()
	defer mu.Unlock()
	if patches != 6 || allocationCount != 3 {
		t.Fatalf("got patches=%d allocations=%d; want six concurrent env updates across two applies, three initial CVMs", patches, allocationCount)
	}
}
