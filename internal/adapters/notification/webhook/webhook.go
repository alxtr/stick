// Package webhook sends release notifications to HTTP endpoints.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"stick/internal/netutil"
	"stick/internal/notification"
)

const defaultTimeout = 10 * time.Second

// Config controls webhook delivery.
type Config struct {
	URL     string
	Timeout time.Duration
}

// Payload is the stable JSON contract sent to webhooks.
type Payload struct {
	StickID        string `json:"stick_id"`
	StickName      string `json:"stick_name"`
	HolderName     string `json:"holder_name"`
	HolderEmail    string `json:"holder_email"`
	Duration       string `json:"duration"`
	ReleasedAt     string `json:"released_at"`
	BaseURL        string `json:"base_url"`
	RecipientName  string `json:"recipient_name"`
	RecipientEmail string `json:"recipient_email"`
}

// Notifier POSTs a JSON-encoded Payload to a URL.
type Notifier struct {
	url    string
	client *http.Client
}

var _ notification.Notifier = (*Notifier)(nil)

// New creates a webhook notifier.
func New(cfg Config) (*Notifier, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || !netutil.IsHTTPSOrLoopbackHTTP(parsed) || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("invalid webhook URL")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Notifier{
		url:    parsed.String(),
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// Notify sends one notification to the configured webhook.
func (w *Notifier) Notify(ctx context.Context, notification notification.Notification) error {
	payload := Payload{
		StickID:        notification.StickID,
		StickName:      notification.StickName,
		HolderName:     notification.HolderName,
		HolderEmail:    notification.HolderEmail,
		Duration:       notification.Duration,
		ReleasedAt:     notification.ReleasedAt,
		BaseURL:        notification.BaseURL,
		RecipientName:  notification.RecipientName,
		RecipientEmail: notification.RecipientEmail,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := w.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return requestError(w.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func requestError(endpoint string, err error) error {
	message := fmt.Sprintf("post webhook request to %s failed", netutil.SafeEndpoint(endpoint))
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w", message, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", message, context.DeadlineExceeded)
	default:
		// Do not include the transport error: net/http wraps failures in a
		// url.Error, whose message contains the complete URL and may expose a
		// webhook token from its query string.
		return errors.New(message)
	}
}
