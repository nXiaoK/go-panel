package manifest

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
)

// Limits 限制遍历更新包时的条目数、单个文件大小与总大小，防止解压炸弹。
type Limits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultLimits 是更新包的默认上限：现有包约 16 个条目、解包后约 112 MiB。
var DefaultLimits = Limits{
	MaxEntries:    4096,
	MaxFileBytes:  256 << 20,
	MaxTotalBytes: 1 << 30,
}

// ErrUnsafeArchive 表示更新包包含不安全或不符合预期的条目。
var ErrUnsafeArchive = errors.New("更新包不安全")

// WalkArchive 安全地遍历 gzip 压缩的 tar 更新包。所有条目必须位于 root/ 之下；拒绝绝对路径、
// 路径穿越、反斜杠、NUL、符号链接、硬链接、设备文件、FIFO、重复条目和截断的数据。
// 只对 AllowedPath 允许的普通文件调用 fn（传入相对 root 的路径），其余普通文件跳过但计入上限；
// fn 未读完的数据由 tar 读取器在下一次 Next 时丢弃。
func WalkArchive(r io.Reader, root string, limits Limits, fn func(path string, size int64, body io.Reader) error) error {
	if root == "" || strings.ContainsAny(root, `/\`) || root == "." || root == ".." {
		return fmt.Errorf("%w：root %q 不合法", ErrUnsafeArchive, root)
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("%w：不是 gzip 文件：%v", ErrUnsafeArchive, err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := make(map[string]bool)
	entries := 0
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w：读取条目失败：%v", ErrUnsafeArchive, err)
		}
		entries++
		if entries > limits.MaxEntries {
			return fmt.Errorf("%w：条目超过 %d 个", ErrUnsafeArchive, limits.MaxEntries)
		}
		rel, err := archiveRelativePath(header.Name, root)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("%w：%q 是不支持的条目类型 %q（只允许普通文件与目录）", ErrUnsafeArchive, header.Name, header.Typeflag)
		}
		if rel == "" {
			return fmt.Errorf("%w：root 不能是普通文件", ErrUnsafeArchive)
		}
		if seen[rel] {
			return fmt.Errorf("%w：条目 %q 重复", ErrUnsafeArchive, rel)
		}
		seen[rel] = true
		if header.Size < 0 || header.Size > limits.MaxFileBytes {
			return fmt.Errorf("%w：%q 大小 %d 超过上限", ErrUnsafeArchive, rel, header.Size)
		}
		total += header.Size
		if total > limits.MaxTotalBytes {
			return fmt.Errorf("%w：解包总大小超过 %d 字节", ErrUnsafeArchive, limits.MaxTotalBytes)
		}
		if !AllowedPath(rel) {
			continue
		}
		if err := fn(rel, header.Size, reader); err != nil {
			return err
		}
	}
}

func archiveRelativePath(name, root string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w：条目名 %q 不合法", ErrUnsafeArchive, name)
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == ".." {
			return "", fmt.Errorf("%w：条目 %q 包含路径穿越", ErrUnsafeArchive, name)
		}
	}
	clean := path.Clean(name)
	if clean == root {
		return "", nil
	}
	rel, ok := strings.CutPrefix(clean, root+"/")
	if !ok || rel == "" {
		return "", fmt.Errorf("%w：条目 %q 不在 %s/ 之下", ErrUnsafeArchive, name, root)
	}
	return rel, nil
}

// DescribeArchive 计算更新包中会被安装的文件列表（按路径排序），供发布工具生成清单。
func DescribeArchive(archivePath, root string, limits Limits) ([]File, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var files []File
	err = WalkArchive(file, root, limits, func(rel string, size int64, body io.Reader) error {
		hash := sha256.New()
		written, err := io.Copy(hash, body)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", rel, err)
		}
		if written != size {
			return fmt.Errorf("%w：%s 数据不完整", ErrUnsafeArchive, rel)
		}
		files = append(files, File{Path: rel, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Mode: ExecutableMode})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// HashFile 返回文件的大小与 SHA-256。
func HashFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}
