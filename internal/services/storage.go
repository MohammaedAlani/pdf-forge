package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"pdf-forge/internal/models"
)

// StorageService handles file storage operations
type StorageService struct {
	client *http.Client
	logger *slog.Logger
}

// NewStorageService creates a new storage service
func NewStorageService(logger *slog.Logger) *StorageService {
	return &StorageService{
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
		logger: logger,
	}
}

// Upload uploads a file to the configured storage
func (s *StorageService) Upload(ctx context.Context, config *models.StorageConfig, data []byte, contentType string) (*models.StorageResult, error) {
	if config == nil {
		return nil, fmt.Errorf("storage config is required")
	}

	switch config.Provider {
	case "s3":
		return s.uploadToS3(ctx, config, data, contentType)
	case "local":
		return s.uploadToLocal(ctx, config, data)
	default:
		return nil, fmt.Errorf("unsupported storage provider: %s", config.Provider)
	}
}

// uploadToS3 uploads to S3 or S3-compatible storage (MinIO, DigitalOcean Spaces, etc.)
func (s *StorageService) uploadToS3(ctx context.Context, config *models.StorageConfig, data []byte, contentType string) (*models.StorageResult, error) {
	// Construct endpoint
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.s3.%s.amazonaws.com", config.Bucket, config.Region)
	}

	// Build path
	path := config.Path
	if config.Filename != "" {
		path = filepath.Join(path, config.Filename)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Determine content type
	if contentType == "" {
		contentType = config.ContentType
	}
	if contentType == "" {
		contentType = "application/pdf"
	}

	// Build URL
	url := fmt.Sprintf("%s%s", endpoint, path)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Set ACL if provided
	if config.ACL != "" {
		req.Header.Set("x-amz-acl", config.ACL)
	}

	// Add custom metadata
	for k, v := range config.Metadata {
		req.Header.Set("x-amz-meta-"+k, v)
	}

	// Sign the request using AWS Signature V4
	if config.AccessKeyID != "" && config.SecretAccessKey != "" {
		s.signS3Request(req, config, data)
	}

	// Send request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Build result URL
	resultURL := url
	if config.ACL == "public-read" {
		resultURL = url
	}

	s.logger.Info("File uploaded to S3",
		"bucket", config.Bucket,
		"path", path,
		"size", len(data),
	)

	return &models.StorageResult{
		Provider: "s3",
		Bucket:   config.Bucket,
		Path:     path,
		URL:      resultURL,
		Size:     int64(len(data)),
	}, nil
}

// signS3Request signs an S3 request using AWS Signature V4 (simplified version)
func (s *StorageService) signS3Request(req *http.Request, config *models.StorageConfig, payload []byte) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	region := config.Region
	if region == "" {
		region = "us-east-1"
	}

	// Set required headers
	req.Header.Set("x-amz-date", amzDate)

	// Calculate payload hash
	payloadHash := sha256Hash(payload)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	// Create canonical request
	canonicalHeaders := s.getCanonicalHeaders(req)
	signedHeaders := s.getSignedHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// Create string to sign
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hash([]byte(canonicalRequest)),
	}, "\n")

	// Calculate signature
	signingKey := s.getSignatureKey(config.SecretAccessKey, dateStamp, region, "s3")
	signature := hmacSHA256Hex(signingKey, stringToSign)

	// Add authorization header
	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		config.AccessKeyID,
		credentialScope,
		signedHeaders,
		signature,
	)
	req.Header.Set("Authorization", authHeader)
}

func (s *StorageService) getCanonicalHeaders(req *http.Request) string {
	headers := make([]string, 0)
	headerMap := make(map[string]string)

	for key := range req.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "host" || strings.HasPrefix(lowerKey, "x-amz-") || lowerKey == "content-type" {
			headerMap[lowerKey] = strings.TrimSpace(req.Header.Get(key))
		}
	}

	// Add host if not present
	if _, ok := headerMap["host"]; !ok {
		headerMap["host"] = req.URL.Host
	}

	keys := make([]string, 0, len(headerMap))
	for k := range headerMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		headers = append(headers, fmt.Sprintf("%s:%s", k, headerMap[k]))
	}

	return strings.Join(headers, "\n") + "\n"
}

func (s *StorageService) getSignedHeaders(req *http.Request) string {
	headers := make([]string, 0)

	for key := range req.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "host" || strings.HasPrefix(lowerKey, "x-amz-") || lowerKey == "content-type" {
			headers = append(headers, lowerKey)
		}
	}

	// Add host
	headers = append(headers, "host")

	sort.Strings(headers)
	return strings.Join(headers, ";")
}

func (s *StorageService) getSignatureKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

// uploadToLocal saves to local filesystem
func (s *StorageService) uploadToLocal(ctx context.Context, config *models.StorageConfig, data []byte) (*models.StorageResult, error) {
	// Build full path
	basePath := config.Bucket
	if basePath == "" {
		basePath = "/tmp/pdf-forge"
	}

	fullPath := filepath.Join(basePath, config.Path)
	if config.Filename != "" {
		fullPath = filepath.Join(fullPath, config.Filename)
	}

	// Create directory if needed
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Write file
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	s.logger.Info("File saved locally",
		"path", fullPath,
		"size", len(data),
	)

	return &models.StorageResult{
		Provider: "local",
		Bucket:   basePath,
		Path:     fullPath,
		URL:      "file://" + fullPath,
		Size:     int64(len(data)),
	}, nil
}

// Download downloads a file from storage
func (s *StorageService) Download(ctx context.Context, config *models.StorageConfig) ([]byte, error) {
	switch config.Provider {
	case "s3":
		return s.downloadFromS3(ctx, config)
	case "local":
		return s.downloadFromLocal(ctx, config)
	default:
		return nil, fmt.Errorf("unsupported storage provider: %s", config.Provider)
	}
}

func (s *StorageService) downloadFromS3(ctx context.Context, config *models.StorageConfig) ([]byte, error) {
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.s3.%s.amazonaws.com", config.Bucket, config.Region)
	}

	path := config.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	url := fmt.Sprintf("%s%s", endpoint, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if config.AccessKeyID != "" && config.SecretAccessKey != "" {
		s.signS3Request(req, config, nil)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (s *StorageService) downloadFromLocal(ctx context.Context, config *models.StorageConfig) ([]byte, error) {
	fullPath := filepath.Join(config.Bucket, config.Path)
	return os.ReadFile(fullPath)
}

// Helper functions
func sha256Hash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hmacSHA256Hex(key []byte, data string) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}
