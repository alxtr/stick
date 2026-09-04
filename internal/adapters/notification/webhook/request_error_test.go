package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"stick/internal/notification"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRequestErrorRedactsWebhookURL(t *testing.T) {
	const secret = "webhook-secret"
	notifier, err := New(Config{URL: "https://hooks.example.test/hook?token=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	notifier.client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("token") != secret {
			t.Fatalf("request lost webhook token")
		}
		return nil, fmt.Errorf("transport failed for %s", request.URL.String())
	})}

	err = notifier.Notify(context.Background(), notification.Notification{})
	if err == nil {
		t.Fatal("expected request error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "?token=") {
		t.Fatalf("request error exposed webhook credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "hooks.example.test") {
		t.Fatalf("request error omitted safe endpoint: %v", err)
	}
}
