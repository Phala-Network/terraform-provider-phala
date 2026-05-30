package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// instanceObjectType mirrors the phala_app_instance schema for protocol-level
// plan tests.
func instanceObjectType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id":                   tftypes.String,
		"app_id":               tftypes.String,
		"name":                 tftypes.String,
		"node_id":              tftypes.Number,
		"docker_compose":       tftypes.String,
		"pre_launch_script":    tftypes.String,
		"env":                  tftypes.Map{ElementType: tftypes.String},
		"encrypted_env":        tftypes.String,
		"compose_hash":         tftypes.String,
		"wait_for_ready":       tftypes.Bool,
		"wait_timeout_seconds": tftypes.Number,
		"vm_uuid":              tftypes.String,
		"instance_id":          tftypes.String,
		"status":               tftypes.String,
		"region":               tftypes.String,
		"instance_type":        tftypes.String,
		"endpoint":             tftypes.String,
		"gateway_base_domain":  tftypes.String,
		"gateway_cname":        tftypes.String,
		"created_at":           tftypes.String,
		"managed":              tftypes.Bool,
	}}
}

// instanceStateValue builds a fully-known prior state for a managed slot with
// the given env value.
func instanceStateValue(envVal string) tftypes.Value {
	ot := instanceObjectType()
	env := tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"SLOT_SECRET": tftypes.NewValue(tftypes.String, envVal),
	})
	return tftypes.NewValue(ot, map[string]tftypes.Value{
		"id":                   tftypes.NewValue(tftypes.String, "app_test:worker-1"),
		"app_id":               tftypes.NewValue(tftypes.String, "app_test"),
		"name":                 tftypes.NewValue(tftypes.String, "worker-1"),
		"node_id":              tftypes.NewValue(tftypes.Number, nil),
		"docker_compose":       tftypes.NewValue(tftypes.String, nil),
		"pre_launch_script":    tftypes.NewValue(tftypes.String, nil),
		"env":                  env,
		"encrypted_env":        tftypes.NewValue(tftypes.String, nil),
		"compose_hash":         tftypes.NewValue(tftypes.String, nil),
		"wait_for_ready":       tftypes.NewValue(tftypes.Bool, true),
		"wait_timeout_seconds": tftypes.NewValue(tftypes.Number, 600),
		"vm_uuid":              tftypes.NewValue(tftypes.String, "vm-aaa"),
		"instance_id":          tftypes.NewValue(tftypes.String, "inst-1"),
		"status":               tftypes.NewValue(tftypes.String, "running"),
		"region":               tftypes.NewValue(tftypes.String, "us-west-1"),
		"instance_type":        tftypes.NewValue(tftypes.String, "tdx.small"),
		"endpoint":             tftypes.NewValue(tftypes.String, "https://example"),
		"gateway_base_domain":  tftypes.NewValue(tftypes.String, "dstack-pha-prod5.phala.network"),
		"gateway_cname":        tftypes.NewValue(tftypes.String, nil),
		"created_at":           tftypes.NewValue(tftypes.String, "2026-05-30T00:00:00Z"),
		"managed":              tftypes.NewValue(tftypes.Bool, true),
	})
}

// instanceConfigValue is the user-set (Optional/Required) subset, with computed
// attributes null — the shape Terraform passes as Config.
func instanceConfigValue(envVal string) tftypes.Value {
	ot := instanceObjectType()
	env := tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"SLOT_SECRET": tftypes.NewValue(tftypes.String, envVal),
	})
	return tftypes.NewValue(ot, map[string]tftypes.Value{
		"id":                   tftypes.NewValue(tftypes.String, nil),
		"app_id":               tftypes.NewValue(tftypes.String, "app_test"),
		"name":                 tftypes.NewValue(tftypes.String, "worker-1"),
		"node_id":              tftypes.NewValue(tftypes.Number, nil),
		"docker_compose":       tftypes.NewValue(tftypes.String, nil),
		"pre_launch_script":    tftypes.NewValue(tftypes.String, nil),
		"env":                  env,
		"encrypted_env":        tftypes.NewValue(tftypes.String, nil),
		"compose_hash":         tftypes.NewValue(tftypes.String, nil),
		"wait_for_ready":       tftypes.NewValue(tftypes.Bool, true),
		"wait_timeout_seconds": tftypes.NewValue(tftypes.Number, 600),
		"vm_uuid":              tftypes.NewValue(tftypes.String, nil),
		"instance_id":          tftypes.NewValue(tftypes.String, nil),
		"status":               tftypes.NewValue(tftypes.String, nil),
		"region":               tftypes.NewValue(tftypes.String, nil),
		"instance_type":        tftypes.NewValue(tftypes.String, nil),
		"endpoint":             tftypes.NewValue(tftypes.String, nil),
		"gateway_base_domain":  tftypes.NewValue(tftypes.String, nil),
		"gateway_cname":        tftypes.NewValue(tftypes.String, nil),
		"created_at":           tftypes.NewValue(tftypes.String, nil),
		"managed":              tftypes.NewValue(tftypes.Bool, nil),
	})
}

// TestAppInstanceEnvUpdatePlansInPlace locks the fix that made
// phala_app_instance.env update in place: changing env must NOT appear in
// the plan's RequiresReplace set (previously env carried RequiresReplace,
// which destroyed and recreated the slot CVM on any env edit).
func TestAppInstanceEnvUpdatePlansInPlace(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()

	provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"api_key":         tftypes.String,
		"api_prefix":      tftypes.String,
		"api_version":     tftypes.String,
		"timeout_seconds": tftypes.Number,
	}}
	provVal := tftypes.NewValue(provType, map[string]tftypes.Value{
		"api_key":         tftypes.NewValue(tftypes.String, "phat_test_key"),
		"api_prefix":      tftypes.NewValue(tftypes.String, "https://example.invalid/api/v1"),
		"api_version":     tftypes.NewValue(tftypes.String, nil),
		"timeout_seconds": tftypes.NewValue(tftypes.Number, 5),
	})
	provDV, _ := tfprotov6.NewDynamicValue(provType, provVal)
	if _, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &provDV}); err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}

	ot := instanceObjectType()
	prior := instanceStateValue("v1")
	priorDV, _ := tfprotov6.NewDynamicValue(ot, prior)

	// Proposed new state = prior with env bumped to v2 (computed attrs carried
	// over known, as UseStateForUnknown would yield).
	var priorAttrs map[string]tftypes.Value
	_ = prior.As(&priorAttrs)
	priorAttrs["env"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"SLOT_SECRET": tftypes.NewValue(tftypes.String, "v2"),
	})
	proposed := tftypes.NewValue(ot, priorAttrs)
	proposedDV, _ := tfprotov6.NewDynamicValue(ot, proposed)

	configDV, _ := tfprotov6.NewDynamicValue(ot, instanceConfigValue("v2"))

	plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "phala_app_instance",
		PriorState:       &priorDV,
		ProposedNewState: &proposedDV,
		Config:           &configDV,
	})
	if err != nil {
		t.Fatalf("PlanResourceChange: %v", err)
	}
	for _, d := range plan.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("plan diagnostic error: %s — %s", d.Summary, d.Detail)
		}
	}
	if len(plan.RequiresReplace) != 0 {
		var paths []string
		for _, p := range plan.RequiresReplace {
			paths = append(paths, p.String())
		}
		t.Fatalf("env change must plan as in-place update, but RequiresReplace = %v", paths)
	}
}

// TestAppInstanceForceNewMatrix asserts, behaviorally via PlanResourceChange,
// exactly which phala_app_instance attributes force replacement. This is the
// ground-truth check behind the schema's RequiresReplace modifiers — and a
// guard against an env-style modifier being added or dropped by accident.
func TestAppInstanceForceNewMatrix(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()

	provType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"api_key": tftypes.String, "api_prefix": tftypes.String,
		"api_version": tftypes.String, "timeout_seconds": tftypes.Number,
	}}
	provVal := tftypes.NewValue(provType, map[string]tftypes.Value{
		"api_key":         tftypes.NewValue(tftypes.String, "phat_test_key"),
		"api_prefix":      tftypes.NewValue(tftypes.String, "https://example.invalid/api/v1"),
		"api_version":     tftypes.NewValue(tftypes.String, nil),
		"timeout_seconds": tftypes.NewValue(tftypes.Number, 5),
	})
	provDV, _ := tfprotov6.NewDynamicValue(provType, provVal)
	if _, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &provDV}); err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	ot := instanceObjectType()

	// Each case starts from the same managed prior state, changes exactly one
	// attribute, and asserts whether that attribute lands in RequiresReplace.
	cases := []struct {
		attr     string
		newVal   tftypes.Value
		forceNew bool
	}{
		{"node_id", tftypes.NewValue(tftypes.Number, 18), true},
		{"docker_compose", tftypes.NewValue(tftypes.String, "services:\n  x:\n"), true},
		{"pre_launch_script", tftypes.NewValue(tftypes.String, "#!/bin/sh\necho hi\n"), true},
		{"compose_hash", tftypes.NewValue(tftypes.String, "abc123"), true},
		{"env", tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
			"SLOT_SECRET": tftypes.NewValue(tftypes.String, "v2"),
		}), false},
	}

	for _, tc := range cases {
		t.Run(tc.attr, func(t *testing.T) {
			var prior map[string]tftypes.Value
			_ = instanceStateValue("v1").As(&prior)
			var cfg map[string]tftypes.Value
			_ = instanceConfigValue("v1").As(&cfg)

			prior[tc.attr] = tc.newVal // proposed new state = prior with attr changed
			cfg[tc.attr] = tc.newVal   // config carries the same user-set change

			priorOrig := instanceStateValue("v1")
			priorOrigDV, _ := tfprotov6.NewDynamicValue(ot, priorOrig)
			proposedDV, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, prior))
			cfgDV, _ := tfprotov6.NewDynamicValue(ot, tftypes.NewValue(ot, cfg))

			plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "phala_app_instance",
				PriorState:       &priorOrigDV,
				ProposedNewState: &proposedDV,
				Config:           &cfgDV,
			})
			if err != nil {
				t.Fatalf("PlanResourceChange: %v", err)
			}
			for _, d := range plan.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError {
					t.Fatalf("plan error: %s — %s", d.Summary, d.Detail)
				}
			}
			hit := false
			for _, p := range plan.RequiresReplace {
				if p.String() == "AttributeName(\""+tc.attr+"\")" {
					hit = true
				}
			}
			if hit != tc.forceNew {
				var got []string
				for _, p := range plan.RequiresReplace {
					got = append(got, p.String())
				}
				t.Fatalf("%s: forceNew=%v want %v (RequiresReplace=%v)", tc.attr, hit, tc.forceNew, got)
			}
		})
	}
}
