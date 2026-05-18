package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func membersList(t *testing.T, vals ...string) types.List {
	t.Helper()
	v, diags := types.ListValueFrom(context.Background(), types.StringType, vals)
	if diags.HasError() {
		t.Fatalf("build members list: %v", diags)
	}
	return v
}

func TestValidateMembersAndName_NoMembers_NoOp(t *testing.T) {
	plan := appResourceModel{
		Name:    types.StringValue("solo-app"),
		Members: types.ListNull(types.StringType),
	}
	diags := validateMembersAndName(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("unexpected error when members unset: %v", diags)
	}
}

func TestValidateMembersAndName_HappyPath_BootstrapMatchesMembers(t *testing.T) {
	plan := appResourceModel{
		Name:    types.StringValue("consul-0"),
		Members: membersList(t, "consul-0", "consul-1", "consul-2"),
	}
	diags := validateMembersAndName(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("expected no error for valid MIG config, got: %v", diags)
	}
}

func TestValidateMembersAndName_NameNotInMembers_HardError(t *testing.T) {
	plan := appResourceModel{
		Name:    types.StringValue("typo-0"),
		Members: membersList(t, "consul-0", "consul-1", "consul-2"),
	}
	diags := validateMembersAndName(context.Background(), plan)
	if !diags.HasError() {
		t.Fatal("expected hard error when name is not in members")
	}
	// Should call out the misalignment in the message.
	combined := diagsToString(diags)
	if !strings.Contains(combined, "typo-0") || !strings.Contains(combined, "consul-0") {
		t.Fatalf("expected error to name the bad value and the members list, got:\n%s", combined)
	}
}

func TestValidateMembersAndName_EmptyMembersList_HardError(t *testing.T) {
	// Construct an explicitly empty (non-null) list. types.ListValueFrom
	// with an empty Go slice can in some framework versions produce a
	// null list; the zero-elements types.ListValue is unambiguous.
	emptyList, diags := types.ListValue(types.StringType, nil)
	if diags.HasError() {
		t.Fatalf("build empty list: %v", diags)
	}
	plan := appResourceModel{
		Name:    types.StringValue("consul-0"),
		Members: emptyList,
	}
	out := validateMembersAndName(context.Background(), plan)
	if !out.HasError() {
		t.Fatal("expected hard error when members is an explicitly empty list")
	}
}

func TestValidateMembersAndName_SingleMember_OK(t *testing.T) {
	plan := appResourceModel{
		Name:    types.StringValue("only-one"),
		Members: membersList(t, "only-one"),
	}
	diags := validateMembersAndName(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("single-member MIG (which is effectively the same as a singleton) should be valid: %v", diags)
	}
}

func diagsToString(diags diag.Diagnostics) string {
	var parts []string
	for _, d := range diags.Errors() {
		parts = append(parts, d.Summary()+": "+d.Detail())
	}
	return strings.Join(parts, "\n")
}
