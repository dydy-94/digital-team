package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client S3 客户端封装
type S3Client struct {
	client        *s3.Client
	presigner     *s3.PresignClient
	bucket        string
	presignExpiry time.Duration
}

// NewS3Client 创建 S3 客户端
func NewS3Client(cfg *Config) (*S3Client, error) {
	if !cfg.IsS3Configured() {
		slog.Warn("S3 未配置，跳过 S3 客户端初始化")
		return nil, nil
	}

	// 创建静态密钥凭证
	creds := credentials.NewStaticCredentialsProvider(
		cfg.S3.AccessKeyID,
		cfg.S3.SecretAccessKey,
		"",
	)

	// AWS SDK v2 配置
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.S3.Region),
		config.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("加载 AWS 配置失败: %w", err)
	}

	// 创建 S3 客户端
	var client *s3.Client
	if cfg.S3.Endpoint != "" {
		// 使用自定义 endpoint（如 MinIO）
		client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			o.UsePathStyle = true // 兼容 MinIO
		})
	} else {
		client = s3.NewFromConfig(awsCfg)
	}

	// 创建 Presign 客户端
	presigner := s3.NewPresignClient(client)

	s3Client := &S3Client{
		client:        client,
		presigner:     presigner,
		bucket:        cfg.S3.Bucket,
		presignExpiry: cfg.GetPresignExpiry(),
	}

	// 确保 bucket 存在
	if err := s3Client.EnsureBucket(); err != nil {
		return nil, fmt.Errorf("创建 bucket 失败: %w", err)
	}

	return s3Client, nil
}

// EnsureBucket 确保 bucket 存在，不存在则创建
func (s *S3Client) EnsureBucket() error {
	if s == nil {
		return fmt.Errorf("S3 客户端未初始化")
	}

	ctx := context.Background()

	// 检查 bucket 是否存在
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err == nil {
		log.Printf("[INFO] [S3] Bucket 已存在: %s", s.bucket)
		return nil
	}

	// bucket 不存在，创建它
	_, err = s.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("创建 bucket 失败: %w", err)
	}

	log.Printf("[INFO] [S3] Bucket 创建成功: %s", s.bucket)
	return nil
}

// GenerateUploadPresignedURL 生成上传 Presigned URL
func (s *S3Client) GenerateUploadPresignedURL(key string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("S3 客户端未初始化")
	}

	ctx := context.Background()
	request, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(s.presignExpiry))
	if err != nil {
		return "", fmt.Errorf("生成上传 Presigned URL 失败: %w", err)
	}

	return request.URL, nil
}

// GenerateDownloadPresignedURL 生成下载 Presigned URL
func (s *S3Client) GenerateDownloadPresignedURL(key string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("S3 客户端未初始化")
	}

	ctx := context.Background()
	request, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(s.presignExpiry))
	if err != nil {
		return "", fmt.Errorf("生成下载 Presigned URL 失败: %w", err)
	}

	return request.URL, nil
}

// UploadFile 上传文件（通过 Presigned URL 客户端直传）
// 返回 Presigned PUT URL
func (s *S3Client) UploadFile(key string, contentType string) (string, error) {
	return s.GenerateUploadPresignedURL(key)
}

// DownloadFile 下载文件（通过 Presigned URL 客户端直传）
// 返回 Presigned GET URL
func (s *S3Client) DownloadFile(key string) (string, error) {
	return s.GenerateDownloadPresignedURL(key)
}

// GetObject 直接获取对象（用于服务器端下载）
func (s *S3Client) GetObject(key string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("S3 客户端未初始化")
	}

	ctx := context.Background()
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("获取 S3 对象失败: %w", err)
	}
	defer result.Body.Close()

	return io.ReadAll(result.Body)
}

// PutObject 直接上传对象
func (s *S3Client) PutObject(key string, body []byte, contentType string) error {
	if s == nil {
		return fmt.Errorf("S3 客户端未初始化")
	}

	ctx := context.Background()
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("上传 S3 对象失败: %w", err)
	}

	return nil
}

// DeleteObject 删除对象
func (s *S3Client) DeleteObject(key string) error {
	if s == nil {
		return fmt.Errorf("S3 客户端未初始化")
	}

	ctx := context.Background()
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("删除 S3 对象失败: %w", err)
	}

	return nil
}

// GenerateS3Key 生成 S3 对象 key
func GenerateS3Key(transferID, fileName string) string {
	// 格式: transfers/{transfer_id}/{filename}
	return fmt.Sprintf("transfers/%s/%s", transferID, url.PathEscape(fileName))
}
