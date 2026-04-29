package storage

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"path"
	"strings"
	"time"

	"labnana2api/internal/config"
	"labnana2api/internal/httpclient"
	"labnana2api/internal/logging"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	cfg        config.ObjectStorageConfig
	minio      *minio.Client
	httpClient *http.Client
	logger     *logging.Logger
}

func New(cfg config.ObjectStorageConfig, logger *logging.Logger) (*Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	config.NormalizeObjectStorageConfig(&cfg)
	if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.SecretAccessKey) == "" || strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("object storage enabled but config is incomplete")
	}

	directHTTPClient := httpclient.NewDirect(120 * time.Second)
	minioClient, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:    cfg.UseSSL,
		Region:    cfg.Region,
		Transport: directHTTPClient.Transport,
	})
	if err != nil {
		return nil, err
	}

	client := &Client{
		cfg:        cfg,
		minio:      minioClient,
		httpClient: directHTTPClient,
		logger:     logger,
	}
	if err := client.ensureBucket(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) UploadBytes(data []byte, mimeType, sourceName string) (string, string, error) {
	if c == nil || c.minio == nil {
		return "", "", fmt.Errorf("object storage client is not initialized")
	}
	if len(data) == 0 {
		return "", "", fmt.Errorf("empty data")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	contentType := normalizeContentType(mimeType)
	objectKey := c.objectKey(data, sourceName, extensionFromContentType(contentType))
	if exists, err := c.objectExists(ctx, objectKey); err == nil && exists {
		url, err := c.publicURL(ctx, objectKey)
		return url, objectKey, err
	}

	_, err := c.minio.PutObject(ctx, c.cfg.Bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", "", err
	}

	url, err := c.publicURL(ctx, objectKey)
	if err != nil {
		return "", "", err
	}
	return url, objectKey, nil
}

func (c *Client) MirrorRemoteImage(rawURL string) (string, string, error) {
	sourceURL := strings.TrimSpace(rawURL)
	if sourceURL == "" {
		return "", "", fmt.Errorf("empty image url")
	}

	req, err := http.NewRequest(http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("source image responded with status %d", resp.StatusCode)
	}

	data, err := ioReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	return c.UploadBytes(data, resp.Header.Get("Content-Type"), path.Base(sourceURL))
}

func (c *Client) ensureBucket() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := c.minio.BucketExists(ctx, c.cfg.Bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if !c.cfg.AutoCreateBucket {
		return fmt.Errorf("bucket %s does not exist", c.cfg.Bucket)
	}
	if err := c.minio.MakeBucket(ctx, c.cfg.Bucket, minio.MakeBucketOptions{Region: c.cfg.Region}); err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.Info("object storage bucket created: %s", c.cfg.Bucket)
	}
	return nil
}

func (c *Client) objectExists(ctx context.Context, objectKey string) (bool, error) {
	_, err := c.minio.StatObject(ctx, c.cfg.Bucket, objectKey, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

func (c *Client) objectKey(data []byte, sourceName, ext string) string {
	sum := sha1.Sum(data)
	name := hex.EncodeToString(sum[:])
	if trimmed := strings.TrimSuffix(path.Base(strings.TrimSpace(sourceName)), path.Ext(sourceName)); trimmed != "" && trimmed != "." {
		if len(trimmed) > 48 {
			trimmed = trimmed[:48]
		}
		name = trimmed + "-" + name[:16]
	}
	prefix := strings.Trim(strings.TrimSpace(c.cfg.KeyPrefix), "/")
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if prefix == "" {
		return name + ext
	}
	return prefix + "/" + name + ext
}

func (c *Client) publicURL(ctx context.Context, objectKey string) (string, error) {
	if base := strings.TrimRight(strings.TrimSpace(c.cfg.PublicBaseURL), "/"); base != "" {
		return base + "/" + neturl.PathEscape(objectKey), nil
	}
	u, err := c.minio.PresignedGetObject(ctx, c.cfg.Bucket, objectKey, 24*time.Hour, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func normalizeContentType(raw string) string {
	contentType := strings.TrimSpace(raw)
	if contentType == "" {
		return "application/octet-stream"
	}
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		return mediaType
	}
	return contentType
}

func extensionFromContentType(contentType string) string {
	switch strings.ToLower(contentType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "image/svg+xml":
		return ".svg"
	default:
		return ""
	}
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

func ioReadAll(body interface{ Read([]byte) (int, error) }) ([]byte, error) {
	return io.ReadAll(struct{ io.Reader }{body})
}
