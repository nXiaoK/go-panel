package handler

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeInstallerDownloadPolicyRunsBeforeCurl(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		allowInsecure bool
		wantAllowed   bool
		wantProto     string
	}{
		{name: "public HTTP rejected", url: "http://panel.example.com/asset"},
		{name: "HTTPS allowed", url: "https://panel.example.com/asset", wantAllowed: true, wantProto: "=https"},
		{name: "IPv4 loopback HTTP allowed", url: "http://127.0.0.1:6365/asset", wantAllowed: true, wantProto: "=http,https"},
		{name: "IPv6 loopback HTTP allowed", url: "http://[::1]:6365/asset", wantAllowed: true, wantProto: "=http,https"},
		{name: "expanded IPv6 loopback HTTP allowed", url: "http://[0:0:0:0:0:0:0:1]:6365/asset", wantAllowed: true, wantProto: "=http,https"},
		{name: "partially compressed IPv6 loopback HTTP allowed", url: "http://[0:0:0:0:0:0::1]:6365/asset", wantAllowed: true, wantProto: "=http,https"},
		{name: "non-loopback IPv6 HTTP rejected", url: "http://[::2]:6365/asset"},
		{name: "malformed IPv6 compression rejected", url: "http://[:::1]:6365/asset"},
		{name: "overlong IPv6 compression rejected", url: "http://[0:0:0:0:0:0:0::1]:6365/asset"},
		{name: "explicit public HTTP allowed", url: "http://panel.example.com/asset", allowInsecure: true, wantAllowed: true, wantProto: "=http,https"},
	}

	for _, scriptName := range []string{"install.sh", "install_nftables.sh"} {
		scriptName := scriptName
		for _, tc := range tests {
			tc := tc
			t.Run(scriptName+"/"+tc.name, func(t *testing.T) {
				raw, err := fs.ReadFile(nodeInstallAssets, "assets/"+scriptName)
				if err != nil {
					t.Fatalf("read embedded script: %v", err)
				}
				script := string(raw)
				mainCall := strings.LastIndex(script, "\nmain")
				if mainCall < 0 {
					t.Fatal("script has no main call")
				}

				dir := t.TempDir()
				libraryPath := filepath.Join(dir, scriptName)
				if err := os.WriteFile(libraryPath, []byte(script[:mainCall]+"\n"), 0600); err != nil {
					t.Fatalf("write sourceable installer: %v", err)
				}
				curlLog := filepath.Join(dir, "curl.log")
				fakeCurl := filepath.Join(dir, "curl")
				if err := os.WriteFile(fakeCurl, []byte("#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$CURL_LOG\"\n"), 0700); err != nil {
					t.Fatalf("write fake curl: %v", err)
				}

				cmd := exec.Command("bash", "-c", `source "$1"; download_node_file "$2" "$3"`, "bash", libraryPath, tc.url, filepath.Join(dir, "asset"))
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "CURL_LOG="+curlLog)
				if tc.allowInsecure {
					cmd.Env = append(cmd.Env, "ALLOW_INSECURE_NODE_DOWNLOADS=true")
				} else {
					cmd.Env = append(cmd.Env, "ALLOW_INSECURE_NODE_DOWNLOADS=")
				}
				output, runErr := cmd.CombinedOutput()
				if tc.wantAllowed && runErr != nil {
					t.Fatalf("allowed URL failed: %v\n%s", runErr, output)
				}
				if !tc.wantAllowed && runErr == nil {
					t.Fatalf("insecure URL unexpectedly allowed\n%s", output)
				}
				if !tc.wantAllowed && !strings.Contains(string(output), "HTTPS") {
					t.Fatalf("rejection did not report HTTPS policy: %v\n%s", runErr, output)
				}

				log, err := os.ReadFile(curlLog)
				curlCalled := err == nil && len(log) > 0
				if tc.wantAllowed && !curlCalled {
					t.Fatal("allowed URL did not reach curl")
				}
				if tc.wantAllowed {
					wantArgs := "--proto " + tc.wantProto + " --proto-redir =https -fsSL"
					if !strings.Contains(string(log), wantArgs) {
						t.Fatalf("curl args = %q, want protocol policy %q", log, wantArgs)
					}
				}
				if !tc.wantAllowed && curlCalled {
					t.Fatalf("rejected URL reached curl first: %s", log)
				}
			})
		}
	}
}

func TestNftInstallerGeneratedRuleURLPreservesPanelScheme(t *testing.T) {
	// Rule download moved out of the installer into the staged apply script
	// (apply_nft_rules.sh) by the crash-safe refresh work; the scheme and
	// redirect-protocol contract now lives there.
	installer, err := fs.ReadFile(nodeInstallAssets, "assets/install_nftables.sh")
	if err != nil {
		t.Fatalf("read embedded nft installer: %v", err)
	}
	if strings.Contains(string(installer), `RULE_URL="http://${PANEL_HOST}:${PANEL_PORT}`) {
		t.Fatal("installer still hard-codes HTTP rule URL")
	}

	raw, err := fs.ReadFile(nodeInstallAssets, "assets/apply_nft_rules.sh")
	if err != nil {
		t.Fatalf("read embedded apply script: %v", err)
	}
	script := string(raw)
	if !strings.Contains(script, `PANEL_BASE_URL="${SERVER_ADDR%/}"`) ||
		!strings.Contains(script, `RULE_URL="${PANEL_BASE_URL}/api/v1/node/nft-config?secret=${SECRET}"`) {
		t.Fatal("apply script does not preserve the configured panel scheme")
	}
	if !strings.Contains(script, `curl --proto "$CURL_INITIAL_PROTOCOLS" --proto-redir '=https' -fsSL "$RULE_URL"`) {
		t.Fatal("apply script rule downloader lacks redirect protocol restrictions")
	}
}

func TestNftInstallerInstallsStaticCrashSafeApplyScript(t *testing.T) {
	asset, err := fs.ReadFile(nodeInstallAssets, "assets/apply_nft_rules.sh")
	if err != nil {
		t.Fatal(err)
	}
	apply := string(asset)
	if !strings.Contains(apply, `nft_flow_reporter \`) || !strings.Contains(apply, `--refresh "$TMP_RULES" "$SERVER_ADDR" "$SECRET" "$NFT_TABLE_NAME"`) {
		t.Fatal("apply asset does not delegate refresh to reporter")
	}
	if !strings.Contains(apply, `FORWARD_SYSCTL_FILE="/etc/sysctl.d/99-flux-nftables-forwarding.conf"`) ||
		!strings.Contains(apply, `net.ipv4.ip_forward = 1`) ||
		!strings.Contains(apply, `> /proc/sys/net/ipv4/ip_forward`) ||
		!strings.Contains(apply, `远程组件升级会直接替换本脚本而不重跑安装器`) {
		t.Fatal("apply asset does not converge persistent and runtime IPv4 forwarding after remote upgrades")
	}
	for _, forbidden := range []string{"delete table", "add table", "nft -f -", "|| true"} {
		if strings.Contains(apply, forbidden) {
			t.Fatalf("apply asset contains unsafe legacy operation %q", forbidden)
		}
	}
	root, err := os.ReadFile(filepath.Join("..", "..", "install_nftables.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"embedded": mustReadNodeAsset(t, "assets/install_nftables.sh"), "root": root} {
		script := string(raw)
		if !strings.Contains(script, `stage_support_script "apply_nft_rules.sh" "$SCRIPT_FILE" 0755`) {
			t.Fatalf("%s installer does not stage the static apply script", name)
		}
		if strings.Contains(script, `$NFT_BIN delete table inet`) || strings.Contains(script, `cat > "$tmp_file" <<'EOF'`) {
			t.Fatalf("%s installer still embeds destructive apply script", name)
		}
		reporter := strings.LastIndex(script, `install_binary "nft_flow_reporter" "$FLOW_REPORTER_FILE"`)
		applyInstall := strings.LastIndex(script, `write_rule_script`)
		agent := strings.LastIndex(script, `install_binary "nft_agent" "$AGENT_FILE"`)
		if reporter < 0 || applyInstall < reporter || agent < applyInstall {
			t.Fatalf("%s installer order does not install reporter -> apply script -> agent", name)
		}
		if !strings.Contains(script, `ACTIVE_TABLE_MARKER="$STATE_DIR/active-table"`) ||
			!strings.Contains(script, `AGENT_SYNC_MARKER="/run/flux-nftables/agent-synced"`) ||
			!strings.Contains(script, `active_table="$(wait_for_verified_active_nft_table)"`) ||
			!strings.Contains(script, `[[ "$synced_table" == "$table_name" ]]`) {
			t.Fatalf("%s installer does not wait for the current agent-synced active generation", name)
		}
		if !strings.Contains(script, `SYSCTL_FILE="/etc/sysctl.d/99-flux-nftables-forwarding.conf"`) ||
			!strings.Contains(script, `net.ipv4.ip_forward = 1`) ||
			!strings.Contains(script, `sysctl -w net.ipv4.ip_forward=1`) ||
			!strings.Contains(script, `install_forwarding_sysctl`) {
			t.Fatalf("%s installer does not persist and activate IPv4 forwarding", name)
		}
		if !strings.Contains(script, `已从面板同步此节点当前启用的转发规则`) ||
			!strings.Contains(script, `当前活动规则表: inet $active_table`) {
			t.Fatalf("%s installer does not report the restored active generation", name)
		}
	}
}

func mustReadNodeAsset(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(nodeInstallAssets, name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
