// releasetool 是维护者与 CI 使用的发布辅助工具：生成签名密钥、生成并签名更新清单、
// 复验发布产物。它不进入面板镜像，也不随 Release 发布。用法见 RELEASING.md。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/trust"
	"github.com/nXiaoK/go-panel/internal/selfupdate/versioninfo"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usageText = `用法：go run ./tools/releasetool <子命令> [参数]

子命令：
  keygen    --out <文件>                 生成 Ed25519 发布密钥：私钥 seed 写入文件（0600），只在终端打印公钥
  manifest  --release-dir <目录> --version <vX.Y.Z> --commit <sha> --build-time <时间> [--repository owner/repo] --out <文件>
                                         扫描各架构压缩包，生成 update-manifest.json
  sign      --manifest <文件> --key-env <变量名> [--optional-key-env <变量名>] --out <文件>
                                         用环境变量中的私钥 seed 签名清单（可多次指定以双签）
  verify    --manifest <文件> --sig <文件> --release-dir <目录> [--repository owner/repo]
                                         用内置公钥复验签名、压缩包与逐文件哈希
  selfcheck --manifest <文件> --binary <go-panel 路径>
                                         执行二进制的 version --json，核对版本、commit、兼容声明与文件哈希
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = runKeygen(args[1:], stdout, stderr)
	case "manifest":
		err = runManifest(args[1:], stdout, stderr)
	case "sign":
		err = runSign(args[1:], stdout, stderr)
	case "verify":
		err = runVerify(args[1:], stdout, stderr)
	case "selfcheck":
		err = runSelfcheck(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "未知子命令 %q\n\n%s", args[0], usageText)
		return 2
	}
	var usage usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &usage):
		fmt.Fprintln(stderr, usage.Error())
		return 2
	default:
		fmt.Fprintf(stderr, "%s 失败：%v\n", args[0], err)
		return 1
	}
}

type usageError string

func (e usageError) Error() string { return string(e) }

// stringList 支持重复出现的参数，例如多次 --key-env。
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func parseFlags(flags *flag.FlagSet, args []string, required map[string]*string) error {
	if err := flags.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() > 0 {
		return usageError("不支持的位置参数：" + strings.Join(flags.Args(), " "))
	}
	for name, value := range required {
		if strings.TrimSpace(*value) == "" {
			return usageError("缺少参数 --" + name)
		}
	}
	return nil
}

func runKeygen(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("keygen", stderr)
	out := flags.String("out", "", "私钥 seed 的输出文件（权限 0600，已存在时拒绝覆盖）")
	if err := parseFlags(flags, args, map[string]*string{"out": out}); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("创建私钥文件失败（不会覆盖已有文件）: %w", err)
	}
	_, writeErr := file.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n")
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		_ = os.Remove(*out)
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	id := manifest.KeyID(pub)
	fmt.Fprintf(stdout, "私钥 seed 已写入 %s（权限 0600）。按 RELEASING.md 存入 GitHub Environment secret 后，删除本地明文或转入离线保管。\n\n", *out)
	fmt.Fprintf(stdout, "公钥（base64）：%s\nkeyId：%s\n\n", encoded, id)
	fmt.Fprintf(stdout, "追加到 internal/selfupdate/trust/keys.go 的 releaseKeys：\n\t%q, // keyId %s，生成于 %s\n",
		encoded, id, time.Now().Format("2006-01-02"))
	return nil
}

func runManifest(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("manifest", stderr)
	releaseDir := flags.String("release-dir", "", "build-release.sh 的输出目录")
	version := flags.String("version", "", "发布版本，例如 v0.2.0")
	commit := flags.String("commit", "", "40 位 Git commit")
	buildTime := flags.String("build-time", "", "UTC 构建时间")
	repository := flags.String("repository", buildinfo.Repository, "发布仓库 owner/repository")
	out := flags.String("out", "", "清单输出路径")
	platforms := flags.String("platforms", "linux/amd64,linux/arm64", "需要写入清单的平台，逗号分隔")
	if err := parseFlags(flags, args, map[string]*string{
		"release-dir": releaseDir, "version": version, "commit": commit, "build-time": buildTime, "out": out,
	}); err != nil {
		return err
	}
	raw, err := buildManifest(*releaseDir, *version, *commit, *buildTime, *repository, strings.Split(*platforms, ","))
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "更新清单已写入 %s\n", *out)
	return nil
}

// buildManifest 扫描各平台压缩包生成清单，并用与面板相同的校验规则自检。
func buildManifest(releaseDir, version, commit, buildTime, repository string, platforms []string) ([]byte, error) {
	m := manifest.Manifest{
		Schema:     manifest.Schema,
		Product:    manifest.Product,
		Repository: strings.TrimSpace(repository),
		Version:    strings.TrimSpace(version),
		Commit:     strings.ToLower(strings.TrimSpace(commit)),
		BuildTime:  strings.TrimSpace(buildTime),
		Compat:     versioninfo.CurrentCompat(),
	}
	for _, platform := range platforms {
		goos, goarch, ok := strings.Cut(strings.TrimSpace(platform), "/")
		if !ok || goos == "" || goarch == "" {
			return nil, fmt.Errorf("平台 %q 必须写成 os/arch", platform)
		}
		root := "go-panel-" + m.Version + "-" + goos + "-" + goarch
		archivePath := filepath.Join(releaseDir, root+".tar.gz")
		size, sum, err := manifest.HashFile(archivePath)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %w", archivePath, err)
		}
		files, err := manifest.DescribeArchive(archivePath, root, manifest.DefaultLimits)
		if err != nil {
			return nil, fmt.Errorf("扫描 %s 失败: %w", archivePath, err)
		}
		m.Artifacts = append(m.Artifacts, manifest.Artifact{
			OS:      goos,
			Arch:    goarch,
			Root:    root,
			Archive: manifest.Archive{Name: root + ".tar.gz", Size: size, SHA256: sum},
			Files:   files,
		})
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if _, err := manifest.Parse(raw); err != nil {
		return nil, fmt.Errorf("生成的清单未通过校验: %w", err)
	}
	return raw, nil
}

func runSign(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("sign", stderr)
	manifestPath := flags.String("manifest", "", "要签名的 update-manifest.json")
	out := flags.String("out", "", "签名文件输出路径")
	var required, optional stringList
	flags.Var(&required, "key-env", "保存私钥 seed 的环境变量名（必填，可重复）")
	flags.Var(&optional, "optional-key-env", "可选的私钥环境变量名，为空时跳过（轮换期双签）")
	if err := parseFlags(flags, args, map[string]*string{"manifest": manifestPath, "out": out}); err != nil {
		return err
	}
	if len(required) == 0 {
		return usageError("至少需要一个 --key-env")
	}
	var seeds []string
	for _, name := range required {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return fmt.Errorf("环境变量 %s 未设置或为空，无法签名", name)
		}
		seeds = append(seeds, value)
	}
	for _, name := range optional {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			seeds = append(seeds, value)
		}
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	envelope, keyIDs, err := signManifest(raw, seeds)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, envelope, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已签名 %s，keyId：%s\n", *manifestPath, strings.Join(keyIDs, ", "))
	return nil
}

// signManifest 只签名通过校验的清单，返回签名文件内容与使用的 keyId（不含任何私钥信息）。
func signManifest(raw []byte, seeds []string) ([]byte, []string, error) {
	if _, err := manifest.Parse(raw); err != nil {
		return nil, nil, fmt.Errorf("拒绝签名不合法的清单: %w", err)
	}
	var signatures []manifest.Signature
	var keyIDs []string
	for _, seed := range seeds {
		priv, err := manifest.ParsePrivateKeySeed(seed)
		if err != nil {
			return nil, nil, err
		}
		signature := manifest.Sign(raw, priv)
		signatures = append(signatures, signature)
		keyIDs = append(keyIDs, signature.KeyID)
	}
	envelope, err := manifest.EncodeSignatures(signatures...)
	if err != nil {
		return nil, nil, err
	}
	return envelope, keyIDs, nil
}

func runVerify(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("verify", stderr)
	manifestPath := flags.String("manifest", "", "update-manifest.json")
	sigPath := flags.String("sig", "", "update-manifest.json.sig")
	releaseDir := flags.String("release-dir", "", "压缩包所在目录")
	repository := flags.String("repository", buildinfo.Repository, "清单应属于的仓库 owner/repository")
	if err := parseFlags(flags, args, map[string]*string{"manifest": manifestPath, "sig": sigPath, "release-dir": releaseDir}); err != nil {
		return err
	}
	ring := trust.Default()
	if ring.Len() == 0 {
		return errors.New("面板未内置任何发布公钥：请先按 RELEASING.md 把公钥写入 internal/selfupdate/trust/keys.go")
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		return err
	}
	m, err := verifyRelease(raw, sig, *releaseDir, *repository, ring)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s %s 验证通过：签名可信，%d 个平台的压缩包与逐文件哈希一致\n", m.Product, m.Version, len(m.Artifacts))
	return nil
}

// verifyRelease 先验签，再逐个平台核对压缩包大小、哈希以及包内文件列表，与面板安装时的检查一致。
func verifyRelease(raw, sig []byte, releaseDir, repository string, ring manifest.KeyRing) (*manifest.Manifest, error) {
	m, err := manifest.VerifyAndParse(raw, sig, ring, repository)
	if err != nil {
		return nil, err
	}
	for _, artifact := range m.Artifacts {
		archivePath := filepath.Join(releaseDir, artifact.Archive.Name)
		size, sum, err := manifest.HashFile(archivePath)
		if err != nil {
			return nil, err
		}
		if size != artifact.Archive.Size || sum != artifact.Archive.SHA256 {
			return nil, fmt.Errorf("%s 与清单不一致（大小 %d / %d）", artifact.Archive.Name, size, artifact.Archive.Size)
		}
		files, err := manifest.DescribeArchive(archivePath, artifact.Root, manifest.DefaultLimits)
		if err != nil {
			return nil, err
		}
		if len(files) != len(artifact.Files) {
			return nil, fmt.Errorf("%s 包含 %d 个安装文件，清单记录 %d 个", artifact.Archive.Name, len(files), len(artifact.Files))
		}
		for _, file := range files {
			want, ok := artifact.File(file.Path)
			if !ok || want != file {
				return nil, fmt.Errorf("%s 中的 %s 与清单不一致", artifact.Archive.Name, file.Path)
			}
		}
	}
	return m, nil
}
