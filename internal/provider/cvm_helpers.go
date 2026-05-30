package provider

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---------------------------------------------------------------------------
// CVMInfo accessor helpers
// ---------------------------------------------------------------------------

func cvmInfoIDString(info *phala.CVMInfo) string {
	return strings.TrimSpace(info.ID)
}

func cvmInfoEnvEncryptionPubkey(info *phala.CVMInfo) string {
	if info.EncryptedEnvPubkey != nil && strings.TrimSpace(*info.EncryptedEnvPubkey) != "" {
		return strings.TrimSpace(*info.EncryptedEnvPubkey)
	}
	if info.KMSInfo != nil && info.KMSInfo.EncryptedEnvPubkey != nil && strings.TrimSpace(*info.KMSInfo.EncryptedEnvPubkey) != "" {
		return strings.TrimSpace(*info.KMSInfo.EncryptedEnvPubkey)
	}
	return ""
}

func cvmInfoOSImageName(info *phala.CVMInfo) string {
	if info.OS != nil && info.OS.Name != nil && strings.TrimSpace(*info.OS.Name) != "" {
		return strings.TrimSpace(*info.OS.Name)
	}
	if info.BaseImage != nil && strings.TrimSpace(*info.BaseImage) != "" {
		return strings.TrimSpace(*info.BaseImage)
	}
	return ""
}

func cvmInfoOSImageHash(info *phala.CVMInfo) string {
	if info.OS == nil || info.OS.OSImageHash == nil {
		return ""
	}
	return strings.TrimSpace(*info.OS.OSImageHash)
}

// cvmInfoImageMatchesUserForm reports whether `userForm` refers to the
// same OS image as this CVM. The cloud returns the OS image as two
// fields (`os.name`, `os.os_image_hash`); the image catalog and the
// `phala images` CLI display the same image as `<name>-<first-N-hex>`.
// Both forms are valid user inputs and must round-trip without producing
// a state diff:
//
//   - bare name      ("dstack-dev-0.5.7")                — matches when
//     userForm == os.name.
//   - combined form  ("dstack-dev-0.5.7-9b6a5239")       — matches when
//     userForm == os.name + "-" + prefix(os.os_image_hash, N) and the
//     prefix portion is a strict hex prefix of os.os_image_hash.
//
// Returns false if the helper cannot prove a match — callers should
// then overwrite state with the bare name.
func cvmInfoImageMatchesUserForm(info *phala.CVMInfo, userForm string) bool {
	name := cvmInfoOSImageName(info)
	if name == "" || userForm == "" {
		return false
	}
	if userForm == name {
		return true
	}
	if !strings.HasPrefix(userForm, name+"-") {
		return false
	}
	suffix := userForm[len(name)+1:]
	if suffix == "" {
		return false
	}
	hash := cvmInfoOSImageHash(info)
	if hash == "" {
		return false
	}
	if !strings.HasPrefix(hash, strings.ToLower(suffix)) {
		return false
	}
	for _, c := range suffix {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func cvmInfoInProgress(info *phala.CVMInfo) bool {
	return info.InProgress || (info.Progress != nil && info.Progress.Target != nil && strings.TrimSpace(*info.Progress.Target) != "")
}

func cvmInfoInstanceType(info *phala.CVMInfo) string {
	if info.Resource.InstanceType != nil && strings.TrimSpace(*info.Resource.InstanceType) != "" {
		return *info.Resource.InstanceType
	}
	return ""
}

func cvmInfoRegion(info *phala.CVMInfo) string {
	if info.NodeInfo != nil && info.NodeInfo.Region != nil && strings.TrimSpace(*info.NodeInfo.Region) != "" {
		return *info.NodeInfo.Region
	}
	return ""
}

// cvmInfoGatewayBaseDomain returns the Phala Cloud gateway base domain
// for this CVM, e.g. "dstack-pha-prod5.phala.network". Empty string
// when the cloud did not include gateway info on the response (some
// legacy / partial responses, or CVMs in early provisioning states).
func cvmInfoGatewayBaseDomain(info *phala.CVMInfo) string {
	if info.Gateway == nil || info.Gateway.BaseDomain == nil {
		return ""
	}
	return strings.TrimSpace(*info.Gateway.BaseDomain)
}

// cvmInfoGatewayCname returns the operator-configured CNAME alias for
// the app, if one is set on the cloud side. Empty when unset.
func cvmInfoGatewayCname(info *phala.CVMInfo) string {
	if info.Gateway == nil || info.Gateway.CNAME == nil {
		return ""
	}
	return strings.TrimSpace(*info.Gateway.CNAME)
}

func cvmInfoEndpoint(info *phala.CVMInfo) string {
	if len(info.Endpoints) > 0 && strings.TrimSpace(info.Endpoints[0].App) != "" {
		return info.Endpoints[0].App
	}
	if len(info.PublicURLs) > 0 && strings.TrimSpace(info.PublicURLs[0].App) != "" {
		return info.PublicURLs[0].App
	}
	return ""
}

func cvmInfoPublicLogsValue(info *phala.CVMInfo) *bool {
	return info.PublicLogs
}

func cvmInfoPublicSysinfoValue(info *phala.CVMInfo) *bool {
	return info.PublicSysinfo
}

func cvmInfoPublicTCBInfoValue(info *phala.CVMInfo) *bool {
	return info.PublicTcbinfo
}

func cvmInfoGatewayEnabledValue(info *phala.CVMInfo) *bool {
	return info.GatewayEnabled
}

func cvmInfoSecureTimeValue(info *phala.CVMInfo) *bool {
	return info.SecureTime
}

func cvmInfoStorageFSValue(info *phala.CVMInfo) string {
	if info.StorageFS != nil && strings.TrimSpace(*info.StorageFS) != "" {
		return *info.StorageFS
	}
	return ""
}

func cvmInfoVMUUID(info *phala.CVMInfo) string {
	if info.VMUUID != nil {
		return strings.TrimSpace(*info.VMUUID)
	}
	return ""
}

func cvmInfoAppID(info *phala.CVMInfo) string {
	if info.AppID != nil {
		return strings.TrimSpace(*info.AppID)
	}
	return ""
}

func cvmInfoInstanceID(info *phala.CVMInfo) string {
	if info.InstanceID != nil {
		return strings.TrimSpace(*info.InstanceID)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

func cvmPath(id string) string {
	return "/cvms/" + url.PathEscape(id)
}

func selectCVMIdentifier(info *phala.CVMInfo, provisionAppID string) string {
	if id := cvmInfoIDString(info); id != "" {
		return id
	}
	if vmUUID := cvmInfoVMUUID(info); vmUUID != "" {
		return vmUUID
	}
	if appID := cvmInfoAppID(info); appID != "" {
		return ensureAppPrefix(appID)
	}
	if strings.TrimSpace(provisionAppID) != "" {
		return ensureAppPrefix(provisionAppID)
	}
	return ""
}

func ensureAppPrefix(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "app_") || strings.HasPrefix(trimmed, "0x") {
		return trimmed
	}
	if len(trimmed) == 40 {
		return "app_" + trimmed
	}
	return trimmed
}

// ---------------------------------------------------------------------------
// Compose-file update helpers
// ---------------------------------------------------------------------------

func provisionAndApplyComposeFileUpdate(
	ctx context.Context,
	client *phala.Client,
	cvmID string,
	provisionReq map[string]any,
) error {
	if strings.TrimSpace(cvmID) == "" {
		return fmt.Errorf("missing cvm id for compose update")
	}
	if strings.TrimSpace(stringFromAny(provisionReq["name"])) == "" {
		return fmt.Errorf("compose update requires non-empty name")
	}

	// Convert map[string]any to the SDK type via JSON round-trip.
	provisionBody, err := json.Marshal(provisionReq)
	if err != nil {
		return fmt.Errorf("marshal compose provision request: %w", err)
	}
	sdkReq := &phala.ProvisionComposeUpdateRequest{}
	if err := json.Unmarshal(provisionBody, sdkReq); err != nil {
		return fmt.Errorf("convert compose provision request: %w", err)
	}
	resp, err := client.ProvisionCVMComposeFileUpdate(ctx, cvmID, sdkReq)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resp.ComposeHash) == "" {
		return fmt.Errorf("compose update provision did not return compose_hash")
	}

	commitReq := &phala.CommitComposeUpdateRequest{
		ComposeHash: resp.ComposeHash,
	}
	if updateEnvVars, ok := provisionReq["update_env_vars"].(bool); ok && updateEnvVars {
		commitReq.UpdateEnvVars = &updateEnvVars
	}
	return client.CommitCVMComposeFileUpdate(ctx, cvmID, commitReq)
}

// ---------------------------------------------------------------------------
// Terraform-value helpers
// ---------------------------------------------------------------------------

func nullableString(v string) types.String {
	if strings.TrimSpace(v) == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}

func nullableBool(v *bool) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*v)
}

func nullableStringPtr(v *string) types.String {
	if v == nil || strings.TrimSpace(*v) == "" {
		return types.StringNull()
	}
	return types.StringValue(strings.TrimSpace(*v))
}

func waitTimeout(v types.Int64) time.Duration {
	if v.IsNull() || v.IsUnknown() || v.ValueInt64() <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(v.ValueInt64()) * time.Second
}

func shouldWait(v types.Bool) bool {
	if v.IsNull() || v.IsUnknown() {
		return true
	}
	return v.ValueBool()
}

func listValueAsStrings(ctx context.Context, value types.List, fieldName string) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return nil, diags
	}
	if value.IsUnknown() {
		diags.AddError("Unknown list value", fmt.Sprintf("%s must be known at apply time.", fieldName))
		return nil, diags
	}

	var out []string
	diags.Append(value.ElementsAs(ctx, &out, false)...)
	if diags.HasError() {
		return nil, diags
	}

	clean := make([]string, 0, len(out))
	for _, v := range out {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			clean = append(clean, trimmed)
		}
	}

	return clean, diags
}

func mapValueAsStrings(ctx context.Context, value types.Map, fieldName string) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return nil, diags
	}
	if value.IsUnknown() {
		diags.AddError("Unknown map value", fmt.Sprintf("%s must be known at apply time.", fieldName))
		return nil, diags
	}

	var out map[string]string
	diags.Append(value.ElementsAs(ctx, &out, false)...)
	if diags.HasError() {
		return nil, diags
	}

	clean := make(map[string]string, len(out))
	for k, v := range out {
		key := strings.TrimSpace(k)
		if key == "" {
			diags.AddError("Invalid env key", "env map contains an empty key.")
			continue
		}
		clean[key] = v
	}
	if diags.HasError() {
		return nil, diags
	}

	return clean, diags
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func knownOptionalString(value types.String, fieldName string) (string, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return "", false, diags
	}
	if value.IsUnknown() {
		diags.AddError("Unknown string value", fmt.Sprintf("%s must be known at apply time.", fieldName))
		return "", false, diags
	}
	return value.ValueString(), true, diags
}

func knownOptionalInt64(value types.Int64, fieldName string) (int64, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return 0, false, diags
	}
	if value.IsUnknown() {
		diags.AddError("Unknown integer value", fmt.Sprintf("%s must be known at apply time.", fieldName))
		return 0, false, diags
	}
	return value.ValueInt64(), true, diags
}

// ---------------------------------------------------------------------------
// Provisioning identity helpers
// ---------------------------------------------------------------------------

type provisionIdentity struct {
	KMSType        string
	CustomAppID    string
	HasCustomAppID bool
	Nonce          int64
	HasNonce       bool
}

func resolveProvisionIdentity(
	kmsValue types.String,
	customAppIDValue types.String,
	nonceValue types.Int64,
) (provisionIdentity, diag.Diagnostics) {
	var diags diag.Diagnostics
	var id provisionIdentity

	kmsRaw, hasKMS, kmsDiags := knownOptionalString(kmsValue, "kms")
	diags.Append(kmsDiags...)
	id.KMSType = "phala"
	if hasKMS {
		normalized, err := normalizeKMSType(kmsRaw)
		if err != nil {
			diags.AddError("Invalid kms", err.Error())
		} else {
			id.KMSType = normalized
		}
	}

	var customDiags diag.Diagnostics
	id.CustomAppID, id.HasCustomAppID, customDiags = knownOptionalString(customAppIDValue, "custom_app_id")
	diags.Append(customDiags...)
	id.CustomAppID = strings.TrimSpace(id.CustomAppID)
	if id.HasCustomAppID && id.CustomAppID == "" {
		diags.AddError("Invalid custom_app_id", "custom_app_id cannot be empty.")
	}

	var nonceDiags diag.Diagnostics
	id.Nonce, id.HasNonce, nonceDiags = knownOptionalInt64(nonceValue, "nonce")
	diags.Append(nonceDiags...)
	if id.HasNonce && id.Nonce < 0 {
		diags.AddError("Invalid nonce", "nonce must be greater than or equal to 0.")
	}

	if id.HasNonce && !id.HasCustomAppID {
		diags.AddError("Invalid nonce configuration", "nonce requires custom_app_id to be set.")
	}
	if id.HasCustomAppID && id.KMSType == "phala" && !id.HasNonce {
		diags.AddError("Invalid custom_app_id configuration", "nonce is required when custom_app_id is set with kms = phala.")
	}
	if id.HasNonce && id.KMSType != "phala" {
		diags.AddError("Invalid nonce configuration", "nonce is only supported when kms = phala.")
	}
	if id.KMSType != "phala" {
		diags.AddError(
			"Unsupported kms flow",
			"Only kms = phala is currently supported by this provider. On-chain kms flows (ethereum/base) are planned but not implemented yet.",
		)
	}

	return id, diags
}

func normalizeKMSType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "phala":
		return "phala", nil
	case "ethereum", "eth":
		return "ethereum", nil
	case "base":
		return "base", nil
	default:
		return "", fmt.Errorf(`kms must be one of "phala", "ethereum", "eth", or "base"`)
	}
}

func kmsPayloadValue(kms string) string {
	switch kms {
	case "ethereum":
		return "ETHEREUM"
	case "base":
		return "BASE"
	default:
		return "PHALA"
	}
}

func validateEncryptedEnvConfig(
	hasEncryptedEnv bool,
	hasEnvKeys bool,
	envComposeHash string,
	envTransactionHash string,
) diag.Diagnostics {
	var diags diag.Diagnostics

	if hasEnvKeys && !hasEncryptedEnv {
		diags.AddError(
			"Invalid encrypted env configuration",
			"env_keys requires encrypted_env to be set.",
		)
	}

	hasCompose := strings.TrimSpace(envComposeHash) != ""
	hasTx := strings.TrimSpace(envTransactionHash) != ""
	if hasCompose != hasTx {
		diags.AddError(
			"Invalid phase-2 env update configuration",
			"env_compose_hash and env_transaction_hash must be set together.",
		)
	}
	if (hasCompose || hasTx) && !hasEncryptedEnv {
		diags.AddError(
			"Invalid phase-2 env update configuration",
			"env_compose_hash/env_transaction_hash requires encrypted_env to be set.",
		)
	}

	return diags
}

// ---------------------------------------------------------------------------
// Encryption helpers
// ---------------------------------------------------------------------------

func encryptEnvMap(env map[string]string, publicKeyBase64 string) (string, error) {
	pubkeyBytes, err := decodeEnvPublicKey(publicKeyBase64)
	if err != nil {
		return "", fmt.Errorf("decode env encryption key: %w", err)
	}
	if len(pubkeyBytes) != 32 {
		return "", fmt.Errorf("invalid env encryption key length: expected 32 bytes, got %d", len(pubkeyBytes))
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	envVars := make([]phala.EnvVar, 0, len(keys))
	for _, key := range keys {
		envVars = append(envVars, phala.EnvVar{
			Key:   key,
			Value: env[key],
		})
	}

	return phala.EncryptEnvVars(envVars, hex.EncodeToString(pubkeyBytes))
}

func decodeEnvPublicKey(v string) ([]byte, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil, fmt.Errorf("empty value")
	}
	trimmed = strings.TrimPrefix(trimmed, "0x")
	trimmed = strings.TrimPrefix(trimmed, "0X")

	if out, err := hex.DecodeString(trimmed); err == nil && len(out) == 32 {
		return out, nil
	}

	if out, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return out, nil
	}
	if out, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil {
		return out, nil
	}

	return nil, fmt.Errorf("unable to decode public key (expected 32-byte hex or base64 format)")
}

// ---------------------------------------------------------------------------
// Text / API helpers
// ---------------------------------------------------------------------------

func normalizeTextBody(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var asString string
	if err := json.Unmarshal([]byte(trimmed), &asString); err == nil {
		return asString
	}

	return raw
}

func isNotFound(err error) bool {
	apiErr, ok := err.(*phala.APIError)
	return ok && apiErr.StatusCode == 404
}

func isRetryable(err error) bool {
	apiErr, ok := err.(*phala.APIError)
	if !ok {
		return false
	}
	return apiErr.IsRetryable()
}

// diagnosticFromAPIError extracts structured diagnostic info from an API error.
// Returns (summary, detail) suitable for resp.Diagnostics.AddError().
func diagnosticFromAPIError(fallbackSummary string, err error) (string, string) {
	apiErr, ok := err.(*phala.APIError)
	if !ok {
		return fallbackSummary, err.Error()
	}
	if apiErr.IsStructured() {
		return fmt.Sprintf("[%s] %s", apiErr.ErrorCode, apiErr.Message), apiErr.FormatError()
	}
	return fallbackSummary, apiErr.Error()
}

// composeEnvKeysFromAttrs derives the list of allowed_env keys for an app compose.
// Prefers the env map (returning its sorted keys) and falls back to an explicit
// env_keys list. Returns (keys, hasKeys, diagnostics).
func composeEnvKeysFromAttrs(ctx context.Context, env types.Map, envKeys types.List) ([]string, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	if !env.IsNull() && !env.IsUnknown() {
		envMap, mapDiags := mapValueAsStrings(ctx, env, "env")
		diags.Append(mapDiags...)
		if diags.HasError() {
			return nil, false, diags
		}
		return sortedEnvKeys(envMap), true, diags
	}

	if !envKeys.IsNull() && !envKeys.IsUnknown() {
		keys, listDiags := listValueAsStrings(ctx, envKeys, "env_keys")
		diags.Append(listDiags...)
		if diags.HasError() {
			return nil, false, diags
		}
		sort.Strings(keys)
		return keys, true, diags
	}

	return nil, false, diags
}

// equalStringSlices compares two string slices for order-sensitive equality.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
