package handler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	nftUninstallGenerationA = "flux_panel_g_0123456789abcdef0123456789abcdef"
	nftUninstallGenerationB = "flux_panel_g_ffffffffffffffffffffffffffffffff"
)

type nftUninstallScript struct {
	name             string
	raw              []byte
	sourceEndMarker  string
	stateRemovalCall string
}

func TestNftUninstallScriptsRemoveKernelStateBeforePersistentState(t *testing.T) {
	for _, script := range loadNftUninstallScripts(t) {
		t.Run(script.name, func(t *testing.T) {
			contents := string(script.raw)
			if !strings.Contains(contents, `STATE_DIR="/var/lib/flux-nftables"`) {
				t.Fatal("uninstaller does not declare the durable nftables state directory")
			}

			guard := strings.LastIndex(contents, "if ! delete_managed_nft_tables; then")
			if guard < 0 {
				t.Fatal("uninstaller does not guard managed nft table deletion")
			}
			removeState := strings.LastIndex(contents, script.stateRemovalCall)
			if removeState < 0 {
				t.Fatalf("uninstaller does not remove persistent state with %q", script.stateRemovalCall)
			}
			if guard > removeState {
				t.Fatal("persistent state is removed before managed nft table deletion is confirmed")
			}
		})
	}
}

func TestNftUninstallScriptsRemoveOnlyPersistentForwardingSetting(t *testing.T) {
	for _, script := range loadNftUninstallScripts(t) {
		t.Run(script.name, func(t *testing.T) {
			contents := string(script.raw)
			if !strings.Contains(contents, `SYSCTL_FILE="/etc/sysctl.d/99-flux-nftables-forwarding.conf"`) {
				t.Fatal("uninstaller does not identify the Flux Panel forwarding sysctl file")
			}
			if !strings.Contains(contents, `"$SYSCTL_FILE"`) {
				t.Fatal("uninstaller does not remove or verify the forwarding sysctl file")
			}
			for _, forbidden := range []string{
				"sysctl -w net.ipv4.ip_forward=0",
				"net.ipv4.ip_forward = 0",
			} {
				if strings.Contains(contents, forbidden) {
					t.Fatalf("uninstaller could disrupt another forwarding service with %q", forbidden)
				}
			}
		})
	}
}

func TestNftUninstallHelpersDeleteOnlyStrictlyManagedTables(t *testing.T) {
	const inventory = `table inet filter
table inet flux_panel
table inet flux_panel_backup
table inet flux_panel_g_short
table inet flux_panel_g_0123456789abcdef0123456789abcdeF
table inet flux_panel_g_0123456789abcdef0123456789abcdef0
table inet flux_panel_g_0123456789abcdef0123456789abcdef_extra
table inet ` + nftUninstallGenerationA + `
table inet ` + nftUninstallGenerationB + `
table ip flux_panel
table ip6 ` + nftUninstallGenerationA + `
`

	wantDeleted := []string{
		"inet flux_panel",
		"inet " + nftUninstallGenerationA,
		"inet " + nftUninstallGenerationB,
	}
	wantRemaining := []string{
		"table inet filter",
		"table inet flux_panel_backup",
		"table inet flux_panel_g_short",
		"table inet flux_panel_g_0123456789abcdef0123456789abcdeF",
		"table inet flux_panel_g_0123456789abcdef0123456789abcdef0",
		"table inet flux_panel_g_0123456789abcdef0123456789abcdef_extra",
		"table ip flux_panel",
		"table ip6 " + nftUninstallGenerationA,
	}

	for _, script := range loadNftUninstallScripts(t) {
		t.Run(script.name, func(t *testing.T) {
			result := runNftDeleteHelper(t, script, inventory, "")
			if result.err != nil {
				t.Fatalf("delete_managed_nft_tables failed: %v\n%s", result.err, result.output)
			}
			if got := sortedNonemptyLines(result.deleteLog); !equalStrings(got, sortedCopy(wantDeleted)) {
				t.Fatalf("deleted tables = %q, want %q", got, sortedCopy(wantDeleted))
			}

			for _, remaining := range wantRemaining {
				if !hasExactLine(result.inventory, remaining) {
					t.Errorf("unrelated table was removed: %s", remaining)
				}
			}
			for _, deleted := range wantDeleted {
				if hasExactLine(result.inventory, "table "+deleted) {
					t.Errorf("managed table remains after successful cleanup: %s", deleted)
				}
			}
		})
	}
}

func TestNftUninstallHelpersFailWhenManagedTableDeletionFails(t *testing.T) {
	const inventory = "table inet flux_panel\ntable inet " + nftUninstallGenerationA + "\n"

	for _, script := range loadNftUninstallScripts(t) {
		t.Run(script.name, func(t *testing.T) {
			result := runNftDeleteHelper(t, script, inventory, nftUninstallGenerationA)
			if result.err == nil {
				t.Fatalf("delete_managed_nft_tables succeeded despite retained table; output:\n%s", result.output)
			}
			if !hasExactLine(result.inventory, "table inet "+nftUninstallGenerationA) {
				t.Fatal("fake failing deletion did not retain its target table")
			}
		})
	}
}

type nftDeleteResult struct {
	output    string
	deleteLog string
	inventory string
	err       error
}

func runNftDeleteHelper(t *testing.T, script nftUninstallScript, inventory, failDelete string) nftDeleteResult {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "uninstall-library.sh")
	source := sourceableNftUninstallScript(t, script)
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}

	inventoryPath := filepath.Join(dir, "nft-state")
	deleteLogPath := filepath.Join(dir, "nft-delete.log")
	if err := os.WriteFile(inventoryPath, []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFakeNftCommands(t, dir)

	cmd := exec.Command("bash", "-c", `
script_path=$1
set --
source "$script_path"
trap - EXIT
DELETE_SELF=0
SUDO_CMD=sudo
delete_managed_nft_tables
`, "bash", sourcePath)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		"NFT_TEST_STATE="+inventoryPath,
		"NFT_TEST_DELETE_LOG="+deleteLogPath,
		"NFT_TEST_FAIL_DELETE="+failDelete,
	)
	output, runErr := cmd.CombinedOutput()
	deleteLog, err := os.ReadFile(deleteLogPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	remaining, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	return nftDeleteResult{
		output: string(output), deleteLog: string(deleteLog), inventory: string(remaining), err: runErr,
	}
}

func loadNftUninstallScripts(t *testing.T) []nftUninstallScript {
	t.Helper()
	rootInstaller, err := os.ReadFile(filepath.Join("..", "..", "install_nftables.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return []nftUninstallScript{
		{
			name: "standalone", raw: mustReadNodeAsset(t, "assets/uninstall_nftables.sh"),
			sourceEndMarker: "\nif ! confirm_uninstall; then", stateRemovalCall: `remove_path "$STATE_DIR"`,
		},
		{
			name: "embedded-installer", raw: mustReadNodeAsset(t, "assets/install_nftables.sh"),
			sourceEndMarker: "\nmain\n", stateRemovalCall: `remove_uninstall_path "$STATE_DIR"`,
		},
		{
			name: "root-installer", raw: rootInstaller,
			sourceEndMarker: "\nmain\n", stateRemovalCall: `remove_uninstall_path "$STATE_DIR"`,
		},
	}
}

func sourceableNftUninstallScript(t *testing.T, script nftUninstallScript) []byte {
	t.Helper()
	end := strings.LastIndex(string(script.raw), script.sourceEndMarker)
	if end < 0 {
		t.Fatalf("%s script lacks source boundary %q", script.name, script.sourceEndMarker)
	}
	return append([]byte(nil), script.raw[:end]...)
}

func writeFakeNftCommands(t *testing.T, dir string) {
	t.Helper()
	const nft = `#!/bin/bash
if [[ "$#" -eq 2 && "$1" == "list" && "$2" == "tables" ]]; then
  cat "$NFT_TEST_STATE"
  exit 0
fi
if [[ "$#" -eq 4 && "$1" == "delete" && "$2" == "table" ]]; then
  printf '%s %s\n' "$3" "$4" >> "$NFT_TEST_DELETE_LOG"
  if [[ "$3" == "inet" && "$4" == "${NFT_TEST_FAIL_DELETE:-}" ]]; then
    exit 1
  fi
  state_tmp="${NFT_TEST_STATE}.tmp"
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" != "table $3 $4" ]]; then
      printf '%s\n' "$line" >> "$state_tmp"
    fi
  done < "$NFT_TEST_STATE"
  mv "$state_tmp" "$NFT_TEST_STATE"
  exit 0
fi
echo "unexpected fake nft invocation: $*" >&2
exit 2
`
	const sudo = `#!/bin/bash
exec "$@"
`
	for name, contents := range map[string]string{"nft": nft, "sudo": sudo} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func sortedNonemptyLines(raw string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return lines
}

func sortedCopy(values []string) []string {
	copyOfValues := append([]string(nil), values...)
	sort.Strings(copyOfValues)
	return copyOfValues
}

func equalStrings(a, b []string) bool {
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

func hasExactLine(raw, want string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
