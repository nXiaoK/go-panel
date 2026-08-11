package handler

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func extractShellFunction(t *testing.T, raw []byte, name string) string {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	start := name + "() {"
	depth := 0
	found := false
	var out strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !found {
			if strings.TrimSpace(line) != start {
				continue
			}
			found = true
		}
		out.WriteString(line)
		out.WriteByte('\n')
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if found && depth == 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !found || depth != 0 {
		t.Fatalf("could not extract shell function %s", name)
	}
	return out.String()
}

func TestSubscriptionGitHubProxyRewritesOnlyApprovedHTTPSOrigins(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "subscription-assets", "vless-server.sh"))
	if err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(t.TempDir(), "github-proxy-functions.sh")
	helper := extractShellFunction(t, raw, "github_proxy_base") +
		extractShellFunction(t, raw, "github_download_url")
	if err := os.WriteFile(helperPath, []byte(helper), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, proxy, upstream string) (string, error) {
		t.Helper()
		cmd := exec.Command("bash", "-c", `source "$1"; github_download_url "$2"`, "bash", helperPath, upstream)
		cmd.Env = append(os.Environ(), "FLUX_GITHUB_PROXY="+proxy, "SUB_PANEL_GITHUB_PROXY=")
		output, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}

	upstream := "https://github.com/XTLS/Xray-core/releases/download/v1/Xray.zip"
	if got, err := run(t, "https://proxy.example.com/github/", upstream); err != nil ||
		got != "https://proxy.example.com/github/"+upstream {
		t.Fatalf("proxied URL=%q err=%v", got, err)
	}
	if got, err := run(t, "", upstream); err != nil || got != upstream {
		t.Fatalf("direct URL=%q err=%v", got, err)
	}
	if got, err := run(t, "http://unsafe.example.com", upstream); err != nil || got != upstream {
		t.Fatalf("unsafe proxy should fall back to direct URL, got=%q err=%v", got, err)
	}
	if got, err := run(t, "https://proxy.example.com", "https://example.com/payload"); err == nil {
		t.Fatalf("unapproved upstream unexpectedly rewritten: %q", got)
	}
}

func TestSubscriptionScriptRoutesGitHubDownloadsThroughHelper(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "subscription-assets", "vless-server.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, want := range []string{
		`SUB_PANEL_GITHUB_PROXY=$(sub_panel_quote "$github_proxy")`,
		`github_download_url "https://github.com/acmesh-official/acme.sh.git"`,
		`github_download_url "https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh"`,
		`github_download_url "https://api.github.com/repos/$repo/releases/latest"`,
		`url=$(github_download_url "$url")`,
		`github_download_url "https://github.com/XTLS/Xray-core/releases/download/`,
		`github_download_url "https://api.github.com/repos/klzgrad/forwardproxy/releases/latest"`,
		`github_download_url "https://api.github.com/repos/ViRb3/wgcf/releases/latest"`,
		`github_download_url "https://github.com/ViRb3/wgcf/releases/download/`,
		`github_download_url "https://raw.githubusercontent.com/Chil30/vless-all-in-one/main/vless-server.sh"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("subscription script does not route GitHub operation through helper: %s", want)
		}
	}
	for _, unsafePublicProxy := range []string{"mirror.ghproxy.com", "gh-proxy.com"} {
		if strings.Contains(script, unsafePublicProxy) {
			t.Fatalf("subscription script retains an unconfigured public download proxy: %s", unsafePublicProxy)
		}
	}
}
