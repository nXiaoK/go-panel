package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func subscriptionRestoreBundle() map[string]any {
	return map[string]any{
		"version": 1, "sourceHost": "node-original123", "server": "192.0.2.5",
		"database": map[string]any{
			"version": "4.0.0",
			"xray": map[string]any{"vless": map[string]any{
				"port": "443", "uuid": "original-uuid", "private_key": "original-private-key",
				"public_key": "original-public-key", "short_id": "abcd", "sni": "example.com",
			}},
			"singbox": map[string]any{},
		},
		"files": map[string]any{},
	}
}

func runSubscriptionRestoreScript(t *testing.T, bundle map[string]any, body string) (string, error, string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required for subscription restore runtime tests")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "subscription-assets", "vless-server.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	var helper strings.Builder
	for _, name := range []string{
		"sub_panel_backup_files", "sub_panel_safe_path", "sub_panel_request", "sub_panel_base_url",
		"sub_panel_validate_backup", "sub_panel_build_backup", "sub_panel_backup", "sub_panel_apply_backup",
		"sub_panel_restore_service_healthy",
		"flux_panel_restore_cli", "sub_panel_report_all", "get_installed_protocols", "db_get_all_protocols",
		"sub_panel_quote", "save_sub_panel_config", "load_sub_panel_config", "sub_panel_source_host", "sub_panel_sanitize_identifier",
	} {
		helper.WriteString(extractShellFunction(t, raw, name))
	}
	// 用进程/服务桩验证恢复顺序，不下载核心、不写系统目录，也不运行真实服务。
	helper.WriteString(`
_err() { printf 'ERROR: %s\n' "$*" >&2; }
_warn() { printf 'WARN: %s\n' "$*" >&2; }
_ok() { printf 'OK: %s\n' "$*"; }
check_root() { :; }
github_proxy_base() { return 1; }
sub_panel_server_ip() { printf '192.0.2.5\n'; }
_ensure_import_dependencies() { printf 'dependencies\n' >> "$ACTION_LOG"; }
create_server_scripts() { printf 'scripts\n' >> "$ACTION_LOG"; }
create_service() { printf 'service %s\n' "$1" >> "$ACTION_LOG"; }
systemctl() {
    printf 'systemctl %s\n' "$*" >> "$ACTION_LOG"
    if [[ "$1" == is-active ]]; then
        if [[ "$FAIL_HEALTH" == 1 ]]; then printf 'activating\n'; else printf 'active\n'; fi
    fi
}
sleep() { :; }
start_services() {
    printf 'start\n' >> "$ACTION_LOG"
    [[ "$FAIL_START" != 1 ]] || return 1
    if [[ "$PARTIAL_START" == 1 ]]; then printf '{"inbounds":[]}' > "$CFG/config.json"
    else printf '{"inbounds":[{"protocol":"vless"}]}' > "$CFG/config.json"; fi
}
create_shortcut() { printf 'shortcut\n' >> "$ACTION_LOG"; }
sub_panel_report_protocol() { printf 'report %s\n' "$1" >> "$ACTION_LOG"; [[ "$FAIL_REPORT" != 1 ]]; }
# Bash 3 的索引数组足以模拟此用例唯一的 vless 服务，避免测试依赖主机 Bash 版本。
PROTO_SVC=(vless-reality)
SUB_PANEL_RESTORING=1
SUB_PANEL_URL=https://panel.example
SUB_PANEL_API_KEY=test-key
DISTRO=debian
`)
	helperPath := filepath.Join(dir, "functions.sh")
	if err := os.WriteFile(helperPath, []byte(helper.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "backup.json")
	if err := os.WriteFile(fixture, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `source "$1"; `+body, "bash", helperPath)
	cmd.Env = append(os.Environ(), "CFG="+cfg, "DB_FILE="+filepath.Join(cfg, "db.json"),
		"SUB_PANEL_FILE="+filepath.Join(cfg, "sub-panel.env"), "SUB_PANEL_NODE_ID_FILE="+filepath.Join(cfg, "sub-panel-node-id"),
		"FIXTURE="+fixture, "ACTION_LOG="+filepath.Join(dir, "actions.log"))
	output, err := cmd.CombinedOutput()
	return string(output), err, dir
}

func TestSubscriptionRestoreScriptPreservesSecretsIdentityAndPermissions(t *testing.T) {
	output, err, dir := runSubscriptionRestoreScript(t, subscriptionRestoreBundle(), `
printf '{"xray":{},"singbox":{}}' > "$DB_FILE"
chmod 644 "$DB_FILE"
sub_panel_apply_backup "$FIXTURE" node-original123
`)
	if err != nil {
		t.Fatalf("restore failed: %v\n%s", err, output)
	}
	cfg := filepath.Join(dir, "config")
	db, _ := os.ReadFile(filepath.Join(cfg, "db.json"))
	if !strings.Contains(string(db), "original-private-key") || !strings.Contains(string(db), "original-uuid") {
		t.Fatalf("credentials were not restored: %s", db)
	}
	id, _ := os.ReadFile(filepath.Join(cfg, "sub-panel-node-id"))
	if string(id) != "original123\n" {
		t.Fatalf("identity changed: %q", id)
	}
	for _, name := range []string{"db.json", "sub-panel-node-id", "sub-panel.env"} {
		info, err := os.Stat(filepath.Join(cfg, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private file %s permissions: info=%v err=%v", name, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg, ".restore-pending")); !os.IsNotExist(err) {
		t.Fatal("successful recovery left pending marker")
	}
}

func TestSubscriptionRestoreScriptRejectsUnsafeOrIncompleteSnapshots(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"path traversal", func(b map[string]any) { b["files"] = map[string]any{"../outside": "secret"} }},
		{"shell environment", func(b map[string]any) { b["files"] = map[string]any{"sub-panel.env": "touch /tmp/unsafe"} }},
		{"missing private key", func(b map[string]any) {
			delete(b["database"].(map[string]any)["xray"].(map[string]any)["vless"].(map[string]any), "private_key")
		}},
		{"invalid port", func(b map[string]any) {
			b["database"].(map[string]any)["xray"].(map[string]any)["vless"].(map[string]any)["port"] = "99999"
		}},
		{"OpenRC injection", func(b map[string]any) {
			b["database"].(map[string]any)["singbox"] = map[string]any{"anytls": map[string]any{"port": "4433", "password": "$(touch /tmp/unsafe)"}}
		}},
		{"unknown version", func(b map[string]any) { b["version"] = 2 }},
		{"wrong identity", func(b map[string]any) { b["sourceHost"] = "node-someone-else" }},
		{"unrestored WARP dependency", func(b map[string]any) {
			b["database"].(map[string]any)["routing_rules"] = []any{map[string]any{"outbound": "warp"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := subscriptionRestoreBundle()
			tc.mutate(bundle)
			output, err, dir := runSubscriptionRestoreScript(t, bundle, `sub_panel_apply_backup "$FIXTURE" node-original123`)
			if err == nil {
				t.Fatalf("invalid snapshot accepted: %s", output)
			}
			if _, err := os.Stat(filepath.Join(dir, "actions.log")); !os.IsNotExist(err) {
				t.Fatal("validation failure reached dependency/service mutations")
			}
		})
	}
}

func TestSubscriptionRestoreScriptRejectsExistingNodesAndSymlinks(t *testing.T) {
	for _, body := range []string{
		`jq '.database' "$FIXTURE" > "$DB_FILE"`,
		`printf '{"xray":{},"singbox":"damaged"}' > "$DB_FILE"`,
		`printf '{"inbounds":[]}' > "$CFG/config.json"`,
		`printf '[snell-server]\n' > "$CFG/snell.conf"`,
		`ln -s "$FIXTURE" "$CFG/role"`,
		`ln -s "$FIXTURE" "$CFG/config.json"`,
		`ln -s "$FIXTURE" "$CFG/sub-panel-node-id"`,
	} {
		output, err, dir := runSubscriptionRestoreScript(t, subscriptionRestoreBundle(), body+`; sub_panel_apply_backup "$FIXTURE" node-original123`)
		if err == nil {
			t.Fatalf("unsafe existing target accepted: %s", output)
		}
		if _, err := os.Stat(filepath.Join(dir, "actions.log")); !os.IsNotExist(err) {
			t.Fatal("unsafe target reached service mutations")
		}
	}
}

func TestSubscriptionRestoreScriptFailureKeepsPendingAndAllowsSameIdentityRetry(t *testing.T) {
	for _, failure := range []string{"FAIL_START=1", "PARTIAL_START=1", "FAIL_HEALTH=1"} {
		output, err, _ := runSubscriptionRestoreScript(t, subscriptionRestoreBundle(), failure+`
if sub_panel_apply_backup "$FIXTURE" node-original123; then exit 10; fi
test "$(cat "$CFG/.restore-pending")" = node-original123 || exit 11
SUB_PANEL_RESTORING=0
sub_panel_backup || exit 12
FAIL_START=0 PARTIAL_START=0 FAIL_HEALTH=0
sub_panel_apply_backup "$FIXTURE" node-original123 || exit 13
test ! -e "$CFG/.restore-pending"
`)
		if err != nil {
			t.Fatalf("failure/retry flow %s failed: %v\n%s", failure, err, output)
		}
	}
}

func TestSubscriptionRestoreScriptChecksBusinessErrorsAndReportFailures(t *testing.T) {
	output, err, _ := runSubscriptionRestoreScript(t, subscriptionRestoreBundle(), `
curl() {
    local output=""
    while [[ $# -gt 0 ]]; do if [[ "$1" == -o ]]; then output="$2"; shift; fi; shift; done
    printf '{"code":401,"msg":"bad key"}' > "$output"
}
if sub_panel_request restore "$FIXTURE" "$CFG/response.json"; then exit 20; fi
jq '.database' "$FIXTURE" > "$DB_FILE"
FAIL_REPORT=1
sub_panel_backup() { printf 'UNEXPECTED BACKUP\n'; }
if sub_panel_report_all; then exit 21; fi
`)
	if err != nil || strings.Contains(output, "UNEXPECTED BACKUP") {
		t.Fatalf("business/report failure mishandled: %v\n%s", err, output)
	}
}

func TestSubscriptionRestoreScriptBackupExcludesLocalSecretsOutsideAllowlist(t *testing.T) {
	output, err, dir := runSubscriptionRestoreScript(t, subscriptionRestoreBundle(), `
jq '.database' "$FIXTURE" > "$DB_FILE"
printf 'original123\n' > "$SUB_PANEL_NODE_ID_FILE"
printf 'local-api-key-must-not-be-in-backup\n' > "$SUB_PANEL_FILE"
printf 'untrusted executable\n' > "$CFG/watchdog.sh"
sub_panel_build_backup "$CFG/snapshot.json"
`)
	if err != nil {
		t.Fatalf("build backup: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config", "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "local-api-key") || strings.Contains(string(data), "untrusted executable") ||
		!strings.Contains(string(data), "original-private-key") || !strings.Contains(string(data), "node-original123") {
		t.Fatalf("backup scope/identity wrong: %s", data)
	}
}

func TestSubscriptionRestoreScriptKeepsLegacyNumericCredentials(t *testing.T) {
	bundle := subscriptionRestoreBundle()
	bundle["database"].(map[string]any)["xray"].(map[string]any)["vless"].(map[string]any)["short_id"] = 12345678
	output, err, _ := runSubscriptionRestoreScript(t, bundle, `sub_panel_validate_backup "$FIXTURE" node-original123`)
	if err != nil {
		t.Fatalf("numeric short_id was rejected: %v\n%s", err, output)
	}
}

func TestSubscriptionRestoreScriptCLICompletesOnlyAfterRestoreReportAndBackup(t *testing.T) {
	for _, failStart := range []bool{false, true} {
		body := `
sub_panel_request() {
    printf 'request %s\n' "$1" >> "$ACTION_LOG"
    case "$1" in
        restore) jq -n --slurpfile snapshot "$FIXTURE" '{code:0,data:$snapshot[0]}' > "$3" ;;
        backup) cp "$2" "$CFG/remote-backup.json"; printf '{"code":0}' > "$3" ;;
        *) return 90 ;;
    esac
}
`
		if failStart {
			body += "FAIL_START=1\n"
		}
		body += `flux_panel_restore_cli https://panel.example test-key node-original123`
		output, err, dir := runSubscriptionRestoreScript(t, subscriptionRestoreBundle(), body)
		actions, _ := os.ReadFile(filepath.Join(dir, "actions.log"))
		if failStart {
			if err == nil || strings.Contains(string(actions), "report ") || strings.Contains(string(actions), "request backup") ||
				strings.Contains(output, "节点已恢复") {
				t.Fatalf("failed restore reported success or uploaded: %v\n%s\n%s", err, output, actions)
			}
			continue
		}
		if err != nil || !strings.Contains(output, "节点已恢复") {
			t.Fatalf("CLI failed: %v\n%s\n%s", err, output, actions)
		}
		flow := string(actions)
		if !(strings.Index(flow, "request restore") < strings.Index(flow, "start\n") &&
			strings.Index(flow, "start\n") < strings.Index(flow, "report vless") &&
			strings.Index(flow, "report vless") < strings.Index(flow, "request backup")) {
			t.Fatalf("unexpected recovery ordering: %s", flow)
		}
		data, err := os.ReadFile(filepath.Join(dir, "config", "remote-backup.json"))
		if err != nil || !strings.Contains(string(data), "original-private-key") || !strings.Contains(string(data), "node-original123") {
			t.Fatalf("recovered snapshot changed credentials/identity: err=%v data=%s", err, data)
		}
	}
}
