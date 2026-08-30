// Package teams sends release notifications to Microsoft Teams incoming
// webhooks.
package teams

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

	"stick/internal/application"
	"stick/internal/netutil"
)

const defaultTimeout = 10 * time.Second

// Config controls Microsoft Teams webhook delivery.
type Config struct {
	URL     string
	Timeout time.Duration
}

// Payload is a Microsoft Teams MessageCard payload.
type Payload struct {
	Type             string            `json:"@type"`
	Context          string            `json:"@context"`
	Summary          string            `json:"summary"`
	ThemeColor       string            `json:"themeColor"`
	Title            string            `json:"title"`
	Sections         []Section         `json:"sections"`
	PotentialActions []PotentialAction `json:"potentialAction,omitempty"`
}

// Section contains the release details displayed in the card.
type Section struct {
	Facts    []Fact `json:"facts"`
	Markdown bool   `json:"markdown"`
}

// Fact is a name/value pair displayed in a MessageCard section.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PotentialAction is an action presented below the card.
type PotentialAction struct {
	Type    string   `json:"@type"`
	Name    string   `json:"name"`
	Targets []Target `json:"targets"`
}

// Target identifies the URL for a MessageCard action.
type Target struct {
	OS  string `json:"os"`
	URI string `json:"uri"`
}

// Notifier sends notifications to Microsoft Teams.
type Notifier struct {
	url    string
	client *http.Client
}

var _ application.Notifier = (*Notifier)(nil)

// New creates a Microsoft Teams webhook notifier.
func New(cfg Config) (*Notifier, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || !netutil.IsHTTPSOrLoopbackHTTP(parsed) || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("invalid Teams webhook URL")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Notifier{
		url:    parsed.String(),
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// Notify sends one release notification to Microsoft Teams.
func (n *Notifier) Notify(ctx context.Context, notification application.Notification) error {
	card := Payload{
		Type:       "MessageCard",
		Context:    "http://schema.org/extensions",
		Summary:    fmt.Sprintf("%s is available", notification.StickName),
		ThemeColor: "0078D4",
		Title:      fmt.Sprintf("%s is available", notification.StickName),
		Sections: []Section{{
			Facts: []Fact{
				{Name: "Stick", Value: notification.StickName},
				{Name: "Previously held by", Value: notification.HolderName},
				{Name: "Held for", Value: notification.Duration},
				{Name: "Released at", Value: notification.ReleasedAt},
			},
		}},
	}
	if notification.BaseURL != "" {
		card.PotentialActions = []PotentialAction{{
			Type: "OpenUri",
			Name: "Claim it now",
			Targets: []Target{{
				OS:  "default",
				URI: strings.TrimRight(notification.BaseURL, "/") + "/sticks/" + url.PathEscape(notification.StickID),
			}},
		}}
	}

	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := n.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return requestError(n.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("teams webhook returned %d", resp.StatusCode)
	}
	return nil
}

func requestError(endpoint string, err error) error {
	message := fmt.Sprintf("post Teams webhook request to %s failed", netutil.SafeEndpoint(endpoint))
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
