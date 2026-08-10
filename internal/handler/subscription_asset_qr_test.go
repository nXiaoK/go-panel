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

func vlessServerQRHelper(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "subscription-assets", "vless-server.sh"))
	if err != nil {
		t.Fatalf("read vless server script: %v", err)
	}
	legacyQRHost := strings.Join([]string{"api", "qrserver", "com"}, ".")
	if bytes.Contains(raw, []byte(legacyQRHost)) {
		t.Fatal("vless server script still contains the third-party QR endpoint")
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	var helper strings.Builder
	depth := 0
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !found {
			if !strings.HasPrefix(line, "gen_qr() {") {
				continue
			}
			found = true
		}
		helper.WriteString(line)
		helper.WriteByte('\n')
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if found && depth == 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan vless server script: %v", err)
	}
	if !found || depth != 0 {
		t.Fatal("could not extract gen_qr helper")
	}
	return helper.String()
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runVlessServerQRHelper(t *testing.T, withQREncode bool) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	curlLog := filepath.Join(dir, "curl.log")
	qrArgsLog := filepath.Join(dir, "qr-args.log")
	qrInputLog := filepath.Join(dir, "qr-input.log")
	writeExecutable(t, filepath.Join(binDir, "curl"), `#!/bin/sh
printf '%s\n' "$*" >> "$CURL_LOG"
exit 99
`)
	if withQREncode {
		writeExecutable(t, filepath.Join(binDir, "qrencode"), `#!/bin/sh
printf '%s\n' "$*" > "$QR_ARGS_LOG"
IFS= read -r input || true
printf '%s' "$input" > "$QR_INPUT_LOG"
printf 'LOCAL-ANSI-QR\n'
`)
	}

	harness := filepath.Join(dir, "harness.sh")
	body := "#!/bin/bash\nset -e\n" + vlessServerQRHelper(t) + "\ngen_qr \"$QR_SECRET\"\n"
	writeExecutable(t, harness, body)
	secret := "vless://uuid:password@example.test:443?psk=secret"
	cmd := exec.Command("/bin/bash", harness)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir,
		"QR_SECRET="+secret,
		"CURL_LOG="+curlLog,
		"QR_ARGS_LOG="+qrArgsLog,
		"QR_INPUT_LOG="+qrInputLog,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run gen_qr: %v\n%s", err, output)
	}
	if raw, err := os.ReadFile(curlLog); err == nil && len(raw) > 0 {
		t.Fatalf("gen_qr called curl: %s", raw)
	}
	args, _ := os.ReadFile(qrArgsLog)
	input, _ := os.ReadFile(qrInputLog)
	return string(output), string(args), string(input)
}

func TestVlessServerLocalQRUsesQREncodeWithoutNetwork(t *testing.T) {
	output, args, input := runVlessServerQRHelper(t, true)
	if !strings.Contains(output, "LOCAL-ANSI-QR") {
		t.Fatalf("local QR output missing: %q", output)
	}
	if !strings.Contains(args, "-t ANSIUTF8") {
		t.Fatalf("qrencode args=%q, want ANSIUTF8", args)
	}
	if input != "vless://uuid:password@example.test:443?psk=secret" {
		t.Fatalf("qrencode stdin=%q, want exact share link", input)
	}
}

func TestVlessServerLocalQRFallsBackToRawLinkAndInstallHint(t *testing.T) {
	output, args, _ := runVlessServerQRHelper(t, false)
	if args != "" {
		t.Fatalf("qrencode unexpectedly ran: %q", args)
	}
	if !strings.Contains(output, "vless://uuid:password@example.test:443?psk=secret") {
		t.Fatalf("fallback omitted raw share link: %q", output)
	}
	if !strings.Contains(output, "qrencode") || !strings.Contains(output, "安装") {
		t.Fatalf("fallback omitted local installation hint: %q", output)
	}
}

func TestVlessServerAdvertisesAndExplainsUDPAccurately(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "subscription-assets", "vless-server.sh"))
	if err != nil {
		t.Fatalf("read vless server script: %v", err)
	}
	script := string(raw)
	for _, want := range []string{
		`packetEncoding=none`,
		`type: snell`,
		`udp-relay=true`,
		`show_udp_setup_hint`,
		`snell.conf 不存在 udp=true 配置项`,
		`Snell 的 UDP 会封装在现有 TCP 会话中`,
		`当前 SS2022 + ShadowTLS 组合只转发 TCP`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("vless server script missing UDP behavior %q", want)
		}
	}
}
