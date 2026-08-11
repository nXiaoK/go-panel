package handler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeSourceableNftInstaller(t *testing.T) string {
	t.Helper()
	raw := mustReadNodeAsset(t, "assets/install_nftables.sh")
	end := strings.LastIndex(string(raw), "\nmain\n")
	if end < 0 {
		t.Fatal("embedded nft installer has no source boundary")
	}
	path := filepath.Join(t.TempDir(), "install-nft-library.sh")
	if err := os.WriteFile(path, raw[:end], 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeNftInstallFake(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestNftInstallerPersistsAndActivatesIPv4Forwarding(t *testing.T) {
	library := writeSourceableNftInstaller(t)
	dir := t.TempDir()
	statePath := filepath.Join(dir, "ip-forward-state")
	configPath := filepath.Join(dir, "99-flux-nftables-forwarding.conf")
	writeNftInstallFake(t, dir, "sudo", "#!/bin/bash\nexec \"$@\"\n")
	writeNftInstallFake(t, dir, "sysctl", `#!/bin/bash
case "$1 $2" in
  "-w net.ipv4.ip_forward=1") printf '1\n' > "$NFT_TEST_SYSCTL_STATE" ;;
  "-n net.ipv4.ip_forward") cat "$NFT_TEST_SYSCTL_STATE" ;;
  *) exit 2 ;;
esac
`)

	cmd := exec.Command("bash", "-c", `
library_path="$1"
config_path="$2"
set --
source "$library_path"
SYSCTL_FILE="$config_path"
install_forwarding_sysctl
`, "bash", library, configPath)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "NFT_TEST_SYSCTL_STATE="+statePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install forwarding sysctl: %v\n%s", err, output)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	if !strings.Contains(config, "net.ipv4.ip_forward = 1") ||
		!strings.Contains(config, "数据包不会进入 forward 链") ||
		!strings.Contains(config, "主机级网络开关") {
		t.Fatalf("forwarding config lacks value or nearby Chinese risk comments:\n%s", config)
	}
	state, err := os.ReadFile(statePath)
	if err != nil || strings.TrimSpace(string(state)) != "1" {
		t.Fatalf("runtime forwarding state=%q err=%v", state, err)
	}
}

func TestNftInstallerReportsOnlyAgentSyncedStableGeneration(t *testing.T) {
	const generationA = "flux_panel_g_0123456789abcdef0123456789abcdef"
	const generationB = "flux_panel_g_fedcba9876543210fedcba9876543210"
	library := writeSourceableNftInstaller(t)
	dir := t.TempDir()
	activePath := filepath.Join(dir, "active-table")
	syncedPath := filepath.Join(dir, "agent-synced")
	writeNftInstallFake(t, dir, "sudo", "#!/bin/bash\nexec \"$@\"\n")
	writeNftInstallFake(t, dir, "systemctl", "#!/bin/bash\nexit 0\n")
	writeNftInstallFake(t, dir, "nft", `#!/bin/bash
if [[ -n "${NFT_TEST_RACE_TABLE:-}" ]]; then
  printf '%s\n' "$NFT_TEST_RACE_TABLE" > "$NFT_TEST_ACTIVE_MARKER"
fi
exit 0
`)

	run := func(t *testing.T, active, synced, race string, wantOK bool) {
		t.Helper()
		if err := os.WriteFile(activePath, []byte(active+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(syncedPath, []byte(synced+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-c", `
library_path="$1"
active_path="$2"
synced_path="$3"
set --
source "$library_path"
ACTIVE_TABLE_MARKER="$active_path"
AGENT_SYNC_MARKER="$synced_path"
verified_active_nft_table
`, "bash", library, activePath, syncedPath)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+":"+os.Getenv("PATH"),
			"NFT_TEST_ACTIVE_MARKER="+activePath,
			"NFT_TEST_RACE_TABLE="+race,
		)
		output, err := cmd.CombinedOutput()
		if wantOK {
			if err != nil || strings.TrimSpace(string(output)) != active {
				t.Fatalf("verified generation output=%q err=%v", output, err)
			}
			return
		}
		if err == nil {
			t.Fatalf("unsafe generation snapshot unexpectedly verified: %q", output)
		}
	}

	t.Run("matching markers", func(t *testing.T) { run(t, generationA, generationA, "", true) })
	t.Run("stale agent marker", func(t *testing.T) { run(t, generationA, generationB, "", false) })
	t.Run("generation changes during nft verification", func(t *testing.T) {
		run(t, generationA, generationA, generationB, false)
	})
}
