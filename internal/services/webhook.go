package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"pdf-forge/internal/models"
)

// WebhookService handles webhook deliveries
type WebhookService struct {
	client  *http.Client
	logger  *slog.Logger
	retries int
}

// WebhookPayload is the data sent to webhook endpoints
type WebhookPayload struct {
	Event       string    `json:"event"`       // conversion.completed, conversion.failed
	RequestID   string    `json:"request_id"`
	Timestamp   time.Time `json:"timestamp"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	
	// Conversion details
	ConversionType string `json:"conversion_type,omitempty"`
	FileSize       int64  `json:"file_size,omitempty"`
	Duration       int64  `json:"duration_ms,omitempty"`
	
	// Optional: PDF data (if include_pdf is true)
	PDF string `json:"pdf,omitempty"` // Base64 encoded
	
	// Storage result (if storage was configured)
	Storage *models.StorageResult `json:"storage,omitempty"`
}

// NewWebhookService creates a new webhook service
func NewWebhookService(logger *slog.Logger) *WebhookService {
	return &WebhookService{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:  logger,
		retries: 3,
	}
}

// Send delivers a webhook payload
func (s *WebhookService) Send(ctx context.Context, config *models.WebhookConfig, payload *WebhookPayload) error {
	if config == nil || config.URL == "" {
		return nil
	}

	// Serialize payload
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Determine method
	method := config.Method
	if method == "" {
		method = http.MethodPost
	}

	var lastErr error
	maxRetries := config.RetryCount
	if maxRetries <= 0 {
		maxRetries = s.retries
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(attempt*attempt) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, config.URL, bytes.NewReader(body))
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "PDF-Forge-Webhook/2.0")
		req.Header.Set("X-Webhook-Event", payload.Event)
		req.Header.Set("X-Request-ID", payload.RequestID)

		// Add custom headers
		for k, v := range config.Headers {
			req.Header.Set(k, v)
		}

		// Add HMAC signature if secret is provided
		if config.Secret != "" {
			signature := s.signPayload(body, config.Secret)
			req.Header.Set("X-Webhook-Signature", signature)
			req.Header.Set("X-Webhook-Signature-256", "sha256="+signature)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			s.logger.Warn("Webhook delivery failed",
				"attempt", attempt+1,
				"url", config.URL,
				"error", err.Error(),
			)
			continue
		}

		// Read response body for logging
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Check for success (2xx status codes)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			s.logger.Info("Webhook delivered successfully",
				"url", config.URL,
				"status", resp.StatusCode,
				"request_id", payload.RequestID,
			)
			return nil
		}

		lastErr = fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(respBody))
		s.logger.Warn("Webhook delivery failed",
			"attempt", attempt+1,
			"url", config.URL,
			"status", resp.StatusCode,
			"response", string(respBody),
		)
	}

	return fmt.Errorf("webhook delivery failed after %d attempts: %w", maxRetries+1, lastErr)
}

// SendAsync delivers a webhook in the background
func (s *WebhookService) SendAsync(config *models.WebhookConfig, payload *WebhookPayload) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := s.Send(ctx, config, payload); err != nil {
			s.logger.Error("Async webhook delivery failed",
				"url", config.URL,
				"error", err.Error(),
				"request_id", payload.RequestID,
			)
		}
	}()
}

// signPayload creates HMAC-SHA256 signature
func (s *WebhookService) signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature verifies webhook signature (for incoming webhooks)
func VerifySignature(payload []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// CreateSuccessPayload creates a payload for successful conversion
func CreateSuccessPayload(requestID string, convType string, pdfData []byte, duration time.Duration, includePDF bool) *WebhookPayload {
	payload := &WebhookPayload{
		Event:          "conversion.completed",
		RequestID:      requestID,
		Timestamp:      time.Now().UTC(),
		Success:        true,
		ConversionType: convType,
		FileSize:       int64(len(pdfData)),
		Duration:       duration.Milliseconds(),
	}

	if includePDF {
		payload.PDF = base64.StdEncoding.EncodeToString(pdfData)
	}

	return payload
}

// CreateErrorPayload creates a payload for failed conversion
func CreateErrorPayload(requestID string, convType string, err error, duration time.Duration) *WebhookPayload {
	return &WebhookPayload{
		Event:          "conversion.failed",
		RequestID:      requestID,
		Timestamp:      time.Now().UTC(),
		Success:        false,
		Error:          err.Error(),
		ConversionType: convType,
		Duration:       duration.Milliseconds(),
	}
}
