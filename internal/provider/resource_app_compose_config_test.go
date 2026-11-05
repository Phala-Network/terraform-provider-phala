package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// composeConfigAPI is a mock Phala Cloud API that records every write.
type composeConfigAPI struct {
	t        *testing.T
	mu       sync.Mutex
	writes   []string
	bodies   map[string]map[string]any
	server   *httptest.Server
	cvmState string
}

func newComposeConfigAPI(t *testing.T) *composeConfigAPI {
	cvm := strings.Replace(envUpdateCVMResponse, `"status":"running"`, `"status":"running","os":{"name":"dstack-0.6.3"},"compose_hash":"abc"`, 1)
	api := &composeConfigAPI{t: t, bodies: map[string]map[string]any{}, cvmState: cvm}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(api.server.Close)
	return api
}

func (a *composeConfigAPI) record(r *http.Request) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			a.t.Errorf("%s: decode body: %v", key, err)
		}
	}
	if _, ok := body["compose_file"]; ok {
		key += " (provision)"
	}
	a.writes = append(a.writes, key)
	a.bodies[key] = body
	return body
}

func (a *composeConfigAPI) take() ([]string, map[string]map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	writes, bodies := a.writes, a.bodies
	a.writes, a.bodies = nil, map[string]map[string]any{}
	return writes, bodies
}

func (a *composeConfigAPI) handle(w http.ResponseWriter, r *http.Request) {
	t := a.t
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.Method == http.MethodGet {
		switch path {
		case "/apps/test":
			writeJSON(t, w, http.StatusOK, `{"app_id":"app_test","name":"demo","cvms":[`+a.cvmState+`]}`)
		case "/apps/test/cvms":
			writeJSON(t, w, http.StatusOK, `[`+a.cvmState+`]`)
		case "/cvms/cvm123":
			writeJSON(t, w, http.StatusOK, a.cvmState)
		case "/cvms/cvm123/docker-compose.yml":
			_, _ = io.WriteString(w, "services:\n  app:\n")
		case "/cvms/cvm123/pre-launch-script":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: GET %s", path)
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}

	body := a.record(r)
	switch {
	case r.Method == http.MethodPost && path == "/cvms":
		if _, provision := body["compose_file"]; provision {
			writeJSON(t, w, http.StatusOK, `{"app_id":"app_test","compose_hash":"abc","app_env_encrypt_pubkey":"`+envUpdateTestPubkey+`","token":"tok_test"}`)
			return
		}
		writeJSON(t, w, http.StatusOK, `{"id":"cvm123","name":"demo","status":"starting"}`)
	case r.Method == http.MethodPatch && path == "/cvms/cvm123":
		writeJSON(t, w, http.StatusAccepted, `{"status":"accepted","correlation_id":"c1"}`)
	default:
		t.Errorf("unexpected request: %s %s", r.Method, path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// appHarness drives phala_app through the provider protocol.
type appHarness struct {
	t        *testing.T
	ctx      context.Context
	server   tfprotov6.ProviderServer
	objType  tftypes.Object
	computed map[string]bool
}

func newAppHarness(t *testing.T, baseURL string) *appHarness {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"api_key": tftypes.String, "api_prefix": tftypes.String, "api_version": tftypes.String, "timeout_seconds": tftypes.Number,
	}}
	provDV, err := tfprotov6.NewDynamicValue(provType, tftypes.NewValue(provType, map[string]tftypes.Value{
		"api_key":         tftypes.NewValue(tftypes.String, "phat_test_key"),
		"api_prefix":      tftypes.NewValue(tftypes.String, baseURL+"/api/v1"),
		"api_version":     tftypes.NewValue(tftypes.String, phala.DefaultAPIVersion),
		"timeout_seconds": tftypes.NewValue(tftypes.Number, 5),
	}))
	if err != nil {
		t.Fatal(err)
	}
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &provDV})
	if err != nil {
		t.Fatal(err)
	}
	assertNoDiagnostics(t, configured.Diagnostics)

	var schemaResp resource.SchemaResponse
	NewAppResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	computed := map[string]bool{}
	for name, attribute := range schemaResp.Schema.Attributes {
		computed[name] = attribute.IsComputed()
	}
	return &appHarness{t: t, ctx: ctx, server: server, objType: envUpdateAppObjectType(), computed: computed}
}

func (h *appHarness) dv(v tftypes.Value) *tfprotov6.DynamicValue {
	h.t.Helper()
	out, err := tfprotov6.NewDynamicValue(h.objType, v)
	if err != nil {
		h.t.Fatal(err)
	}
	return &out
}

// plan plans config against prior (nil on create). Like Terraform Core, the
// proposed state keeps prior values of computed attributes the config omits.
func (h *appHarness) plan(prior *tfprotov6.DynamicValue, config tftypes.Value) *tfprotov6.PlanResourceChangeResponse {
	h.t.Helper()
	proposed := config
	priorDV := h.dv(tftypes.NewValue(h.objType, nil))
	if prior != nil {
		priorDV = prior
		priorValue, err := prior.Unmarshal(h.objType)
		if err != nil {
			h.t.Fatal(err)
		}
		var priorAttrs, configAttrs map[string]tftypes.Value
		if err := priorValue.As(&priorAttrs); err != nil {
			h.t.Fatal(err)
		}
		if err := config.As(&configAttrs); err != nil {
			h.t.Fatal(err)
		}
		proposedAttrs := make(map[string]tftypes.Value, len(configAttrs))
		for name, v := range configAttrs {
			proposedAttrs[name] = v
			if v.IsNull() && h.computed[name] {
				proposedAttrs[name] = priorAttrs[name]
			}
		}
		proposed = tftypes.NewValue(h.objType, proposedAttrs)
	}
	resp, err := h.server.PlanResourceChange(h.ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "phala_app", PriorState: priorDV, ProposedNewState: h.dv(proposed), Config: h.dv(config),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	assertNoDiagnostics(h.t, resp.Diagnostics)
	return resp
}

// apply plans and applies config, returning the new state.
func (h *appHarness) apply(prior *tfprotov6.DynamicValue, config tftypes.Value) *tfprotov6.DynamicValue {
	h.t.Helper()
	planned := h.plan(prior, config)
	priorDV := prior
	if priorDV == nil {
		priorDV = h.dv(tftypes.NewValue(h.objType, nil))
	}
	resp, err := h.server.ApplyResourceChange(h.ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "phala_app", PriorState: priorDV, PlannedState: planned.PlannedState, Config: h.dv(config),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	assertNoDiagnostics(h.t, resp.Diagnostics)
	return resp.NewState
}

func (h *appHarness) attr(state *tfprotov6.DynamicValue, name string) tftypes.Value {
	h.t.Helper()
	value, err := state.Unmarshal(h.objType)
	if err != nil {
		h.t.Fatal(err)
	}
	var attrs map[string]tftypes.Value
	if err := value.As(&attrs); err != nil {
		h.t.Fatal(err)
	}
	return attrs[name]
}

// composeConfigApp returns a phala_app config; overrides replace attributes.
func (h *appHarness) composeConfigApp(overrides map[string]tftypes.Value) tftypes.Value {
	str := func(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	values := map[string]tftypes.Value{
		"name":                 str("cfg-demo"),
		"size":                 str("tdx.small"),
		"storage_fs":           str("ext4"),
		"disk_size":            tftypes.NewValue(tftypes.Number, 20),
		"docker_compose":       str("services:\n  app:\n"),
		"wait_for_ready":       tftypes.NewValue(tftypes.Bool, false),
		"wait_timeout_seconds": tftypes.NewValue(tftypes.Number, 600),
	}
	for name, v := range overrides {
		values[name] = v
	}
	return appObjectValue(values)
}

func (h *appHarness) object(name string, values map[string]tftypes.Value) tftypes.Value {
	typ := h.objType.AttributeTypes[name].(tftypes.Object)
	return nestedObject(typ, values)
}

func nestedObject(typ tftypes.Object, values map[string]tftypes.Value) tftypes.Value {
	all := make(map[string]tftypes.Value, len(typ.AttributeTypes))
	for name, attrType := range typ.AttributeTypes {
		all[name] = tftypes.NewValue(attrType, nil)
	}
	for name, v := range values {
		all[name] = v
	}
	return tftypes.NewValue(typ, all)
}

func stringSet(values ...string) tftypes.Value {
	elems := make([]tftypes.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, tftypes.NewValue(tftypes.String, v))
	}
	return tftypes.NewValue(tftypes.Set{ElementType: tftypes.String}, elems)
}

func stringList(values ...string) tftypes.Value {
	elems := make([]tftypes.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, tftypes.NewValue(tftypes.String, v))
	}
	return tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, elems)
}

func sortInitScripts(body map[string]any) {
	if scripts, ok := body["init_scripts"].([]any); ok {
		sort.Slice(scripts, func(i, j int) bool { return scripts[i].(string) < scripts[j].(string) })
	}
}

// TestAppResourceComposeSettingsLifecycle creates an app with compose
// settings and init scripts, then updates them in place, and asserts the
// exact request bodies: create sends them in the provision request, an
// unchanged config plans no change, and an update sends only the changed keys
// as one compose merge patch.
func TestAppResourceComposeSettingsLifecycle(t *testing.T) {
	api := newComposeConfigAPI(t)
	h := newAppHarness(t, api.server.URL)
	portPolicyType := h.objType.AttributeTypes["port_policy"].(tftypes.Object)
	portType := portPolicyType.AttributeTypes["ports"].(tftypes.List).ElementType.(tftypes.Object)
	portPolicy := h.object("port_policy", map[string]tftypes.Value{
		"restrict_mode": tftypes.NewValue(tftypes.Bool, true),
		"ports": tftypes.NewValue(portPolicyType.AttributeTypes["ports"], []tftypes.Value{
			nestedObject(portType, map[string]tftypes.Value{
				"port": tftypes.NewValue(tftypes.Number, 8080),
				"pp":   tftypes.NewValue(tftypes.Bool, true),
			}),
		}),
	})
	createConfig := h.composeConfigApp(map[string]tftypes.Value{
		"port_policy": portPolicy,
		"swap_size":   tftypes.NewValue(tftypes.String, "2G"),
		"runner":      tftypes.NewValue(tftypes.String, "docker-compose"),
		"requirements": h.object("requirements", map[string]tftypes.Value{
			"health_check": tftypes.NewValue(tftypes.Bool, true),
			"platforms":    stringList(),
		}),
		"init_scripts": stringSet("docker_cleanup"),
	})

	created := h.apply(nil, createConfig)
	writes, bodies := api.take()
	if !reflect.DeepEqual(writes, []string{"POST /cvms (provision)", "POST /cvms"}) {
		t.Fatalf("create writes = %v", writes)
	}
	provision := bodies["POST /cvms (provision)"]
	compose := provision["compose_file"].(map[string]any)
	assertJSONEqual(t, "port_policy", compose["port_policy"], `{"ports":[{"port":8080,"pp":true}],"restrict_mode":true}`)
	assertJSONEqual(t, "requirements", compose["requirements"], `{"health_check":true,"platforms":[]}`)
	if compose["swap_size"] != "2G" || compose["runner"] != "docker-compose" {
		t.Fatalf("compose_file = %v", compose)
	}
	assertJSONEqual(t, "init_scripts", provision["init_scripts"], `["docker_cleanup"]`)

	noop := h.plan(created, createConfig)
	plannedNoop, _ := noop.PlannedState.Unmarshal(h.objType)
	createdValue, _ := created.Unmarshal(h.objType)
	if !plannedNoop.Equal(createdValue) {
		t.Fatalf("unchanged config plans a change:\n%s\n%s", plannedNoop, createdValue)
	}

	// Drop swap_size, change requirements and add an init script.
	updateConfig := h.composeConfigApp(map[string]tftypes.Value{
		"port_policy": portPolicy,
		"runner":      tftypes.NewValue(tftypes.String, "docker-compose"),
		"requirements": h.object("requirements", map[string]tftypes.Value{
			"health_check":        tftypes.NewValue(tftypes.Bool, false),
			"platforms":           stringList(),
			"ephemeral_data_disk": tftypes.NewValue(tftypes.Bool, true),
		}),
		"init_scripts":      stringSet("docker_cleanup", "env_merge"),
		"confirm_data_wipe": tftypes.NewValue(tftypes.Bool, true),
	})
	updatePlan := h.plan(created, updateConfig)
	if len(updatePlan.RequiresReplace) != 0 {
		t.Fatalf("in-place settings planned replacement: %v", updatePlan.RequiresReplace)
	}
	planned, _ := updatePlan.PlannedState.Unmarshal(h.objType)
	var plannedAttrs map[string]tftypes.Value
	_ = planned.As(&plannedAttrs)
	if plannedAttrs["compose_hash"].IsKnown() {
		t.Fatal("compose_hash must be unknown when a compose setting changes")
	}
	h.apply(created, updateConfig)
	writes, bodies = api.take()
	if !reflect.DeepEqual(writes, []string{"PATCH /cvms/cvm123"}) {
		t.Fatalf("update writes = %v", writes)
	}
	patch := bodies["PATCH /cvms/cvm123"]
	sortInitScripts(patch)
	assertJSONEqual(t, "PATCH body", patch, `{
		"compose": {
			"swap_size": null,
			"requirements": {"ephemeral_data_disk": true, "health_check": false}
		},
		"init_scripts": ["docker_cleanup", "env_merge"],
		"confirm_data_wipe": true
	}`)
}

// TestAppResourceComposeConfigForceNew asserts that exactly the settings the
// backend cannot update (runner, bash_script, requirements.gpu_policy) plan a
// replacement, and that the other compose settings update in place.
func TestAppResourceComposeConfigForceNew(t *testing.T) {
	api := newComposeConfigAPI(t)
	h := newAppHarness(t, api.server.URL)
	created := h.apply(nil, h.composeConfigApp(nil))
	api.take()

	gpuPolicyType := h.objType.AttributeTypes["requirements"].(tftypes.Object).AttributeTypes["gpu_policy"].(tftypes.Object)
	for _, tc := range []struct {
		attr     string
		value    tftypes.Value
		forceNew bool
	}{
		{"runner", tftypes.NewValue(tftypes.String, "nerdctl-compose"), true},
		{"bash_script", tftypes.NewValue(tftypes.String, "echo hi"), true},
		{"requirements", h.object("requirements", map[string]tftypes.Value{
			"gpu_policy": nestedObject(gpuPolicyType, map[string]tftypes.Value{"attest_gpu": tftypes.NewValue(tftypes.Bool, true)}),
		}), true},
		{"requirements", h.object("requirements", map[string]tftypes.Value{"tdx_measure_acpi_tables": tftypes.NewValue(tftypes.Bool, true)}), false},
		{"port_policy", h.object("port_policy", map[string]tftypes.Value{"restrict_mode": tftypes.NewValue(tftypes.Bool, true)}), false},
		{"swap_size", tftypes.NewValue(tftypes.String, "4G"), false},
		{"storage_discard", tftypes.NewValue(tftypes.Bool, true), false},
		{"snapshotter", tftypes.NewValue(tftypes.String, "stargz"), false},
		{"event_log_version", tftypes.NewValue(tftypes.Number, 2), false},
		{"init_scripts", stringSet("docker_cleanup"), false},
	} {
		planned := h.plan(created, h.composeConfigApp(map[string]tftypes.Value{tc.attr: tc.value}))
		got := len(planned.RequiresReplace) > 0
		if got != tc.forceNew {
			t.Errorf("%s = %s: replacement %v, want %v (%v)", tc.attr, tc.value, got, tc.forceNew, planned.RequiresReplace)
		}
	}
}
