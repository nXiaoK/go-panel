// Package manifest 定义发布更新清单（update-manifest.json）及其签名信封，并提供验签、
// 严格校验与更新包（tar.gz）的安全遍历。字段含义见 docs/soft-update/README.md §4.2。
//
// 信任链：内置公钥 → 清单签名 → 清单中的压缩包与逐文件 SHA-256。先验签、后解析；
// 清单的未知字段一律忽略以保持向前兼容，但已知字段必须全部通过严格校验。
package manifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
)

const (
	// Schema 是清单与签名信封的格式版本。
	Schema = 1
	// Product 是清单必须声明的产品名。
	Product = "go-panel"
	// SignatureDomain 是签名消息的域分隔前缀，防止同一密钥的签名被挪作他用。
	SignatureDomain = "go-panel-update-manifest-v1\n"
	// Algorithm 是唯一支持的签名算法。
	Algorithm = "ed25519"

	// FileName 与 SignatureName 是 Release 中的资产名。
	FileName      = "update-manifest.json"
	SignatureName = "update-manifest.json.sig"

	// MaxManifestBytes 与 MaxSignatureBytes 限制下载与解析的大小。
	MaxManifestBytes  = 256 << 10
	MaxSignatureBytes = 16 << 10
	// MaxArchiveBytes 是单个更新包的大小上限（现有约 42 MiB）。
	MaxArchiveBytes = 512 << 20

	// ExecutableMode 是安装后文件的唯一权限；tar 内原始权限不参与比对。
	ExecutableMode = "0755"
	// BinaryPath 是面板程序在包内（相对 root）的路径。
	BinaryPath = "go-panel"
)

var (
	// ErrNoTrustedKeys 表示没有内置任何发布公钥，软更新保持关闭。
	ErrNoTrustedKeys = errors.New("未配置发布签名公钥")
	// ErrUntrusted 表示签名无效或来自不受信任的密钥。
	ErrUntrusted = errors.New("更新清单签名无效或来自不受信任的密钥")
	// ErrSchemaTooNew 表示清单或签名信封由更新的格式写成，需要先更新镜像。
	ErrSchemaTooNew = errors.New("更新清单格式过新，请先更新镜像")
	// ErrInvalid 表示清单字段不合法。
	ErrInvalid = errors.New("更新清单内容不合法")
	// ErrArtifactMissing 表示清单中没有当前平台的产物。
	ErrArtifactMissing = errors.New("更新清单中没有当前平台的产物")
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	platformPattern   = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	nodeAssetPattern  = regexp.MustCompile(`^node-assets/[A-Za-z0-9._-]{1,64}$`)
)

// Archive 描述一个平台的更新包。
type Archive struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// File 描述更新包中会被安装的一个文件（路径相对包内 root）。
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode"`
}

// Artifact 是一个平台的更新包与其中需要安装的文件。
type Artifact struct {
	OS      string  `json:"os"`
	Arch    string  `json:"arch"`
	Root    string  `json:"root"`
	Archive Archive `json:"archive"`
	Files   []File  `json:"files"`
}

// Manifest 是 update-manifest.json 的内容。
type Manifest struct {
	Schema     int             `json:"schema"`
	Product    string          `json:"product"`
	Repository string          `json:"repository"`
	Version    string          `json:"version"`
	Commit     string          `json:"commit"`
	BuildTime  string          `json:"buildTime"`
	Compat     protocol.Compat `json:"compat"`
	Artifacts  []Artifact      `json:"artifacts"`
}

// Signature 是一把密钥对清单的签名。
type Signature struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

// SignatureEnvelope 是 update-manifest.json.sig 的内容；轮换密钥期间可包含多个签名。
type SignatureEnvelope struct {
	Schema     int         `json:"schema"`
	Signatures []Signature `json:"signatures"`
}

// KeyRing 是受信任的发布公钥集合，按 keyId 索引。
type KeyRing struct {
	keys map[string]ed25519.PublicKey
}

// NewKeyRing 由公钥构造密钥环；长度不正确的公钥会被忽略。
func NewKeyRing(pubs ...ed25519.PublicKey) KeyRing {
	ring := KeyRing{keys: make(map[string]ed25519.PublicKey, len(pubs))}
	for _, pub := range pubs {
		if len(pub) != ed25519.PublicKeySize {
			continue
		}
		ring.keys[KeyID(pub)] = slices.Clone(pub)
	}
	return ring
}

// Len 返回受信任公钥的数量。
func (r KeyRing) Len() int {
	return len(r.keys)
}

// KeyIDs 返回排序后的 keyId 列表，用于日志与诊断。
func (r KeyRing) KeyIDs() []string {
	ids := make([]string, 0, len(r.keys))
	for id := range r.keys {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// KeyID 取公钥 SHA-256 的前 8 字节，写成小写十六进制。
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ParsePublicKey 解析 base64 编码的 Ed25519 公钥。
func ParsePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("Ed25519 公钥必须是 32 字节的 base64 编码")
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKeySeed 解析 base64 编码的 Ed25519 私钥 seed（32 字节）。
func ParsePrivateKeySeed(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != ed25519.SeedSize {
		return nil, errors.New("Ed25519 私钥 seed 必须是 32 字节的 base64 编码")
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

func signedMessage(raw []byte) []byte {
	message := make([]byte, 0, len(SignatureDomain)+len(raw))
	message = append(message, SignatureDomain...)
	return append(message, raw...)
}

// Sign 对清单原始字节签名。
func Sign(raw []byte, priv ed25519.PrivateKey) Signature {
	pub := priv.Public().(ed25519.PublicKey)
	return Signature{
		KeyID:     KeyID(pub),
		Algorithm: Algorithm,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signedMessage(raw))),
	}
}

// EncodeSignatures 生成签名文件内容。
func EncodeSignatures(signatures ...Signature) ([]byte, error) {
	if len(signatures) == 0 {
		return nil, errors.New("至少需要一个签名")
	}
	raw, err := json.MarshalIndent(SignatureEnvelope{Schema: Schema, Signatures: signatures}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Verify 验证清单原始字节：任一签名来自受信任的密钥且验证通过即可。
func Verify(raw, sig []byte, ring KeyRing) error {
	if ring.Len() == 0 {
		return ErrNoTrustedKeys
	}
	if len(sig) > MaxSignatureBytes {
		return fmt.Errorf("%w：签名文件超过 %d 字节", ErrUntrusted, MaxSignatureBytes)
	}
	var envelope SignatureEnvelope
	if err := json.Unmarshal(sig, &envelope); err != nil {
		return fmt.Errorf("%w：签名文件格式错误", ErrUntrusted)
	}
	if envelope.Schema > Schema {
		return ErrSchemaTooNew
	}
	if envelope.Schema != Schema {
		return fmt.Errorf("%w：签名文件缺少 schema", ErrUntrusted)
	}
	message := signedMessage(raw)
	for _, signature := range envelope.Signatures {
		if signature.Algorithm != Algorithm {
			continue
		}
		pub, ok := ring.keys[strings.ToLower(strings.TrimSpace(signature.KeyID))]
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(signature.Signature)
		if err != nil || len(decoded) != ed25519.SignatureSize {
			continue
		}
		if ed25519.Verify(pub, message, decoded) {
			return nil
		}
	}
	return ErrUntrusted
}

// VerifyAndParse 先验签、再严格解析，最后确认清单属于预期的仓库（不区分大小写）。
func VerifyAndParse(raw, sig []byte, ring KeyRing, repository string) (*Manifest, error) {
	if len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("%w：清单超过 %d 字节", ErrInvalid, MaxManifestBytes)
	}
	if err := Verify(raw, sig, ring); err != nil {
		return nil, err
	}
	manifest, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(manifest.Repository, strings.TrimSpace(repository)) {
		return nil, fmt.Errorf("%w：清单属于仓库 %s，而不是 %s", ErrInvalid, manifest.Repository, repository)
	}
	return manifest, nil
}

// Parse 严格校验清单内容。调用方必须先用 Verify 确认签名，Parse 本身不代表可信。
func Parse(raw []byte) (*Manifest, error) {
	if len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("%w：清单超过 %d 字节", ErrInvalid, MaxManifestBytes)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	if manifest.Schema > Schema {
		return nil, ErrSchemaTooNew
	}
	if err := manifest.validate(); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	return &manifest, nil
}

func (m *Manifest) validate() error {
	if m.Schema != Schema {
		return fmt.Errorf("不支持的 schema %d", m.Schema)
	}
	if m.Product != Product {
		return fmt.Errorf("产品名 %q 不是 %s", m.Product, Product)
	}
	if !repositoryPattern.MatchString(m.Repository) {
		return fmt.Errorf("仓库 %q 必须是 owner/repository", m.Repository)
	}
	if !canonicalStable(m.Version) {
		return fmt.Errorf("版本 %q 不是规范的稳定版本", m.Version)
	}
	if !commitPattern.MatchString(m.Commit) {
		return fmt.Errorf("commit %q 必须是 40 位小写十六进制", m.Commit)
	}
	if strings.TrimSpace(m.BuildTime) == "" || len(m.BuildTime) > 64 {
		return errors.New("缺少构建时间")
	}
	compat := m.Compat
	if compat.RuntimeABI < 1 || compat.SchemaEpoch < 1 || compat.MinReaderEpoch < 1 || compat.MinReaderEpoch > compat.SchemaEpoch {
		return fmt.Errorf("兼容声明不合法：%+v", compat)
	}
	if compat.SupervisorProtocol.Min < 1 || compat.SupervisorProtocol.Max < compat.SupervisorProtocol.Min {
		return fmt.Errorf("supervisor 协议范围不合法：%+v", compat.SupervisorProtocol)
	}
	if !canonicalStable(compat.MinUpgradeFrom) {
		return fmt.Errorf("minUpgradeFrom %q 不是规范的稳定版本", compat.MinUpgradeFrom)
	}
	if len(m.Artifacts) == 0 || len(m.Artifacts) > 16 {
		return errors.New("产物数量必须在 1–16 之间")
	}
	platforms := make(map[string]bool, len(m.Artifacts))
	for _, artifact := range m.Artifacts {
		platform := artifact.OS + "/" + artifact.Arch
		if platforms[platform] {
			return fmt.Errorf("平台 %s 重复", platform)
		}
		platforms[platform] = true
		if err := artifact.validate(m.Version); err != nil {
			return fmt.Errorf("%s：%w", platform, err)
		}
	}
	return nil
}

func (a *Artifact) validate(version string) error {
	if !platformPattern.MatchString(a.OS) || !platformPattern.MatchString(a.Arch) {
		return errors.New("平台名称不合法")
	}
	if want := "go-panel-" + version + "-" + a.OS + "-" + a.Arch; a.Root != want {
		return fmt.Errorf("root %q 应为 %q", a.Root, want)
	}
	if a.Archive.Name != a.Root+".tar.gz" {
		return fmt.Errorf("压缩包名 %q 应为 %q", a.Archive.Name, a.Root+".tar.gz")
	}
	if a.Archive.Size <= 0 || a.Archive.Size > MaxArchiveBytes {
		return fmt.Errorf("压缩包大小 %d 超出范围", a.Archive.Size)
	}
	if !sha256Pattern.MatchString(a.Archive.SHA256) {
		return errors.New("压缩包 SHA-256 格式不合法")
	}
	if len(a.Files) == 0 || len(a.Files) > DefaultLimits.MaxEntries {
		return errors.New("文件数量超出范围")
	}
	seen := make(map[string]bool, len(a.Files))
	var total int64
	for _, file := range a.Files {
		if !AllowedPath(file.Path) {
			return fmt.Errorf("文件路径 %q 不在允许范围内", file.Path)
		}
		if seen[file.Path] {
			return fmt.Errorf("文件 %q 重复", file.Path)
		}
		seen[file.Path] = true
		if file.Size <= 0 || file.Size > DefaultLimits.MaxFileBytes {
			return fmt.Errorf("文件 %q 大小 %d 超出范围", file.Path, file.Size)
		}
		total += file.Size
		if !sha256Pattern.MatchString(file.SHA256) {
			return fmt.Errorf("文件 %q 的 SHA-256 格式不合法", file.Path)
		}
		if file.Mode != ExecutableMode {
			return fmt.Errorf("文件 %q 的权限 %q 只能是 %s", file.Path, file.Mode, ExecutableMode)
		}
	}
	if total > DefaultLimits.MaxTotalBytes {
		return errors.New("文件总大小超出范围")
	}
	if !seen[BinaryPath] {
		return errors.New("缺少面板程序 go-panel")
	}
	return nil
}

// ArtifactFor 返回指定平台的产物。
func (m *Manifest) ArtifactFor(goos, goarch string) (*Artifact, error) {
	for i := range m.Artifacts {
		if m.Artifacts[i].OS == goos && m.Artifacts[i].Arch == goarch {
			return &m.Artifacts[i], nil
		}
	}
	return nil, fmt.Errorf("%w：%s/%s", ErrArtifactMissing, goos, goarch)
}

// File 返回产物中指定路径的文件描述。
func (a *Artifact) File(path string) (File, bool) {
	for _, file := range a.Files {
		if file.Path == path {
			return file, true
		}
	}
	return File{}, false
}

// AllowedPath 判断包内相对路径是否属于会被安装的文件：面板程序与 node-assets 下的单层文件。
func AllowedPath(path string) bool {
	if path == BinaryPath {
		return true
	}
	if !nodeAssetPattern.MatchString(path) {
		return false
	}
	name := strings.TrimPrefix(path, "node-assets/")
	return name != "." && name != ".."
}

func canonicalStable(version string) bool {
	parsed, ok := semver.Parse(version)
	return ok && parsed.String() == version
}
