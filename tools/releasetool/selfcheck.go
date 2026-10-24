package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

func runSelfcheck(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("selfcheck", stderr)
	manifestPath := flags.String("manifest", "", "update-manifest.json")
	binary := flags.String("binary", "", "要核对的 go-panel 二进制（须能在当前平台执行）")
	if err := parseFlags(flags, args, map[string]*string{"manifest": manifestPath, "binary": binary}); err != nil {
		return err
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	report, err := selfCheck(raw, *binary, runVersionJSON)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s（%s/%s）与清单一致\n", report.Version, report.GOOS, report.GOARCH)
	return nil
}

// selfCheck 执行二进制的 version --json，确认其版本、commit、兼容声明与清单完全一致，
// 且二进制本身与清单中对应平台的 go-panel 哈希相同，防止清单常量与实际产物脱节。
func selfCheck(raw []byte, binary string, version func(string) (protocol.VersionReport, error)) (protocol.VersionReport, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return protocol.VersionReport{}, err
	}
	report, err := version(binary)
	if err != nil {
		return protocol.VersionReport{}, err
	}
	if report.Version != m.Version || report.Commit != m.Commit {
		return report, fmt.Errorf("二进制报告 %s（%s），清单为 %s（%s）", report.Version, report.Commit, m.Version, m.Commit)
	}
	if report.Compat != m.Compat {
		return report, fmt.Errorf("二进制的兼容声明 %+v 与清单 %+v 不一致", report.Compat, m.Compat)
	}
	artifact, err := m.ArtifactFor(report.GOOS, report.GOARCH)
	if err != nil {
		return report, err
	}
	want, ok := artifact.File(manifest.BinaryPath)
	if !ok {
		return report, errors.New("清单缺少 go-panel 条目")
	}
	size, sum, err := manifest.HashFile(binary)
	if err != nil {
		return report, err
	}
	if size != want.Size || sum != want.SHA256 {
		return report, fmt.Errorf("二进制哈希与清单中 %s/%s 的 go-panel 不一致", report.GOOS, report.GOARCH)
	}
	return report, nil
}

// runVersionJSON 以最小环境执行 version --json，不向被检查的程序传递 CI 中的任何密钥。
func runVersionJSON(binary string) (protocol.VersionReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "version", "--json")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return protocol.VersionReport{}, fmt.Errorf("执行 %s version --json 失败: %w（%s）", binary, err, bytes.TrimSpace(stderr.Bytes()))
	}
	var report protocol.VersionReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return protocol.VersionReport{}, fmt.Errorf("解析 version --json 输出失败: %w", err)
	}
	return report, nil
}
