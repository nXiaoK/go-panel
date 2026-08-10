package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// r2StoredObject 是保留策略所需的最小对象元数据。
type r2StoredObject struct {
	Key          string
	LastModified time.Time
	Size         int64
}

// r2ObjectStore 隔离 S3 兼容实现，便于在测试中验证上传和保留策略而不访问真实 R2。
type r2ObjectStore interface {
	HeadBucket(ctx context.Context, bucket string) error
	PutFile(ctx context.Context, bucket, key, filePath, sha256Hex string, size int64) error
	ListObjects(ctx context.Context, bucket, prefix string) ([]r2StoredObject, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

type awsR2ObjectStore struct {
	client *s3.Client
}

// newAWSR2ObjectStore 仅构造 Cloudflare 官方 R2 S3 端点；账号 ID 已在设置层严格校验，
// 避免管理员配置被利用为任意内网请求地址。R2 使用 region=auto 和路径风格寻址。
func newAWSR2ObjectStore(settings r2ResolvedSettings) (r2ObjectStore, error) {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", settings.AccountID)
	// 单次客户端最长 2 分钟；手动接口和自动任务还会通过 context 施加更短或相同上限，
	// 避免 R2 网络故障无限占用数据库操作门控。
	return newAWSR2ObjectStoreAt(settings, endpoint, &http.Client{Timeout: 2 * time.Minute})
}

func newAWSR2ObjectStoreAt(settings r2ResolvedSettings, endpoint string, httpClient *http.Client) (*awsR2ObjectStore, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("R2 HTTP 客户端不能为空")
	}
	provider := credentials.NewStaticCredentialsProvider(settings.AccessKeyID, settings.SecretAccessKey, "")
	awsConfig := aws.Config{
		// Cloudflare R2 固定使用 region=auto；静态凭据只驻留当前进程内存。
		Region:      "auto",
		Credentials: aws.NewCredentialsCache(provider),
		HTTPClient:  httpClient,
		// 仅在协议要求时计算/验证校验和，避免发送 R2 不需要的额外 SDK 校验头；
		// 备份本身另有 SHA-256 对象元数据供恢复前人工核对。
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		// BaseEndpoint 在生产路径只能来自已校验 Account ID；路径风格兼容 R2 桶寻址。
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
		// SDK 最多尝试 3 次；外层 context/HTTP 超时仍是最终上限。
		options.RetryMaxAttempts = 3
	})
	return &awsR2ObjectStore{client: client}, nil
}

func (s *awsR2ObjectStore) HeadBucket(ctx context.Context, bucket string) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	return err
}

func (s *awsR2ObjectStore) PutFile(ctx context.Context, bucket, key, filePath, sha256Hex string, size int64) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("打开备份文件失败: %w", err)
	}
	defer file.Close()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          file,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String("application/vnd.sqlite3"),
		CacheControl:  aws.String("no-store"),
		Metadata: map[string]string{
			"sha256":     sha256Hex,
			"created-by": "flux-panel",
		},
	})
	return err
}

func (s *awsR2ObjectStore) ListObjects(ctx context.Context, bucket, prefix string) ([]r2StoredObject, error) {
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	objects := make([]r2StoredObject, 0)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Contents {
			if object.Key == nil {
				continue
			}
			item := r2StoredObject{Key: *object.Key}
			if object.LastModified != nil {
				item.LastModified = *object.LastModified
			}
			if object.Size != nil {
				item.Size = *object.Size
			}
			objects = append(objects, item)
		}
	}
	return objects, nil
}

func (s *awsR2ObjectStore) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err
}
