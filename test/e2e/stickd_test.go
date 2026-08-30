// Package e2e_test exercises end-to-end behavior.
package e2e_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"uuid"

	"stick/test/testsupport/mongotest"
)

func TestRunningBinarySupportsOIDCSessionCSRFAndReleaseWebhook(t *testing.T) {
	runReleaseNotificationE2E(t, "webhook", func(body []byte, createdID string) error {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("webhook payload: %w", err)
		}
		if payload["stick_id"] != createdID || payload["recipient_email"] != "admin@example.com" {
			return fmt.Errorf("webhook payload = %s", body)
		}
		return nil
	})
}

func TestRunningBinarySupportsOIDCSessionCSRFAndReleaseTeams(t *testing.T) {
	runReleaseNotificationE2E(t, "teams", func(body []byte, createdID string) error {
		var payload struct {
			Type     string `json:"@type"`
			Context  string `json:"@context"`
			Title    string `json:"title"`
			Sections []struct {
				Facts []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"facts"`
			} `json:"sections"`
			PotentialActions []struct {
				Targets []struct {
					URI string `json:"uri"`
				} `json:"targets"`
			} `json:"potentialAction"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("Teams payload: %w", err)
		}
		if payload.Type != "MessageCard" || payload.Context != "http://schema.org/extensions" {
			return fmt.Errorf("Teams card metadata = %+v", payload)
		}
		if payload.Title != "Deploy Key is available" || len(payload.Sections) != 1 || len(payload.Sections[0].Facts) < 1 {
			return fmt.Errorf("Teams card content = %+v", payload)
		}
		if payload.Sections[0].Facts[0].Value != "Deploy Key" {
			return fmt.Errorf("Teams stick fact = %+v", payload.Sections[0].Facts[0])
		}
		if len(payload.PotentialActions) != 1 || len(payload.PotentialActions[0].Targets) != 1 ||
			!strings.HasSuffix(payload.PotentialActions[0].Targets[0].URI, "/sticks/"+createdID) {
			return fmt.Errorf("Teams action = %+v", payload.PotentialActions)
		}
		return nil
	})
}

func TestRunningBinarySupportsMultipleInstancesOfOneNotificationBackend(t *testing.T) {
	first := newWebhookEndpoint(t)
	second := newWebhookEndpoint(t)
	notificationConfig := fmt.Sprintf("  webhook:\n    - url: %s\n    - url: %s", yamlString(first.server.URL), yamlString(second.server.URL))
	runReleaseNotificationConfigE2E(t, filepath.Join(t.TempDir(), "stick.db"), notificationConfig, []*webhookEndpoint{first, second}, func(body []byte, createdID string) error {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("webhook payload: %w", err)
		}
		if payload["stick_id"] != createdID || payload["recipient_email"] != "admin@example.com" {
			return fmt.Errorf("webhook payload = %s", body)
		}
		return nil
	})
}

func TestRunningBinarySupportsMongoDBPersistence(t *testing.T) {
	baseURL := mongotest.Start(t)
	databaseURL := mongotest.IsolatedURL(t, baseURL, "stick_e2e")
	endpoint := newWebhookEndpoint(t)
	notificationConfig := fmt.Sprintf("  webhook:\n    url: %s", yamlString(endpoint.server.URL))
	runReleaseNotificationConfigE2E(t, databaseURL, notificationConfig, []*webhookEndpoint{endpoint}, func(body []byte, createdID string) error {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("webhook payload: %w", err)
		}
		if payload["stick_id"] != createdID || payload["recipient_email"] != "admin@example.com" {
			return fmt.Errorf("webhook payload = %s", body)
		}
		return nil
	})
}

type webhookEndpoint struct {
	server *httptest.Server
	calls  chan []byte
}

func newWebhookEndpoint(t *testing.T) *webhookEndpoint {
	t.Helper()
	endpoint := &webhookEndpoint{calls: make(chan []byte, 1)}
	endpoint.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		endpoint.calls <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(endpoint.server.Close)
	return endpoint
}

func runReleaseNotificationE2E(t *testing.T, backend string, validate func([]byte, string) error) {
	endpoint := newWebhookEndpoint(t)
	notificationConfig := fmt.Sprintf("  %s:\n    url: %s", backend, yamlString(endpoint.server.URL))
	runReleaseNotificationConfigE2E(t, filepath.Join(t.TempDir(), "stick.db"), notificationConfig, []*webhookEndpoint{endpoint}, validate)
}

func runReleaseNotificationConfigE2E(
	t *testing.T,
	databaseURL string,
	notificationConfig string,
	endpoints []*webhookEndpoint,
	validate func([]byte, string) error,
) {
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "stickd")
	build := exec.Command("go", "build", "-o", binary, "./cmd/stickd")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stickd: %v\n%s", err, output)
	}

	listenAddr := reserveAddress(t)
	oidc := newFakeOIDCProvider(t, "client-id")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(`database: %s
server:
  public_url: %s
  listen_addr: %s
auth:
  oidc:
    issuer: %s
    client_id: client-id
    client_secret: client-secret
  session_secret: 0123456789abcdef0123456789abcdef
  admin_emails:
    - admin@example.com
notifications:
%s
  `, yamlString(databaseURL), yamlString("http://"+listenAddr+"/stick"), yamlString(listenAddr), yamlString(oidc.server.URL), notificationConfig)
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	process := exec.Command(binary, "-config", configPath)
	process.Dir = root
	process.Stdout = &logs
	process.Stderr = &logs
	if err := process.Start(); err != nil {
		t.Fatalf("start stickd: %v", err)
	}
	t.Cleanup(func() {
		if process.ProcessState == nil || !process.ProcessState.Exited() {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	})

	baseURL := "http://" + listenAddr
	waitForURL(t, baseURL+"/stick/healthz", process, &logs)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	loginResponse := mustRequest(t, client, http.MethodGet, baseURL+"/stick/auth/login", "", nil)
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login final status = %d: %s", loginResponse.StatusCode, readResponseBody(t, loginResponse))
	}
	loginResponse.Body.Close()

	uiResponse := mustRequest(t, client, http.MethodGet, baseURL+"/stick/", "", nil)
	if uiResponse.StatusCode != http.StatusOK {
		t.Fatalf("authenticated UI status = %d: %s", uiResponse.StatusCode, readResponseBody(t, uiResponse))
	}
	uiResponse.Body.Close()

	newStickResponse := mustRequest(t, client, http.MethodGet, baseURL+"/stick/sticks/new", "", nil)
	if newStickResponse.StatusCode != http.StatusOK {
		t.Fatalf("new stick form status = %d: %s", newStickResponse.StatusCode, readResponseBody(t, newStickResponse))
	}
	newStickBody := readResponseBody(t, newStickResponse)
	csrfToken := formValue(t, newStickBody, "csrf_token")
	createForm := url.Values{"csrf_token": {csrfToken}, "name": {"Deploy Key"}}
	createResponse := mustRequest(t, client, http.MethodPost, baseURL+"/stick/sticks/new", createForm.Encode(), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	if createResponse.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d: %s", createResponse.StatusCode, readResponseBody(t, createResponse))
	}
	dashboardBody := readResponseBody(t, createResponse)
	createdMatch := regexp.MustCompile(`href="/stick/sticks/([^"]+)"`).FindStringSubmatch(dashboardBody)
	if len(createdMatch) != 2 {
		t.Fatalf("created stick link not found in dashboard: %s", dashboardBody)
	}
	createdID := createdMatch[1]
	if _, err := uuid.Parse(createdID); err != nil {
		t.Fatalf("created stick ID = %q, want UUID: %v", createdID, err)
	}

	detailResponse := mustRequest(t, client, http.MethodGet, baseURL+"/stick/sticks/"+createdID, "", nil)
	detailBody := readResponseBody(t, detailResponse)
	csrfToken = formValue(t, detailBody, "csrf_token")
	version := formValue(t, detailBody, "version")
	if csrfToken == "" || version == "" {
		t.Fatalf("detail form missing CSRF token or version: %s", detailBody)
	}
	subscribeForm := url.Values{"csrf_token": {csrfToken}, "version": {version}}
	subscribeResponse := mustRequest(t, client, http.MethodPost, baseURL+"/stick/sticks/"+createdID+"/notify", subscribeForm.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if subscribeResponse.StatusCode != http.StatusOK {
		t.Fatalf("subscribe status = %d: %s", subscribeResponse.StatusCode, readResponseBody(t, subscribeResponse))
	}
	subscribedDetailBody := readResponseBody(t, subscribeResponse)
	csrfToken = formValue(t, subscribedDetailBody, "csrf_token")
	version = formValue(t, subscribedDetailBody, "version")
	claimForm := url.Values{"csrf_token": {csrfToken}, "version": {version}, "reason": {"deploying"}}
	claimResponse := mustRequest(t, client, http.MethodPost, baseURL+"/stick/sticks/"+createdID+"/claim", claimForm.Encode(), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	if claimResponse.StatusCode != http.StatusOK {
		t.Fatalf("claim status = %d: %s", claimResponse.StatusCode, readResponseBody(t, claimResponse))
	}
	claimBody := readResponseBody(t, claimResponse)
	if !strings.Contains(claimBody, "admin@example.com") || !strings.Contains(claimBody, "deploying") {
		t.Fatalf("claim page did not retain the authenticated holder: %s", claimBody)
	}
	csrfToken = formValue(t, claimBody, "csrf_token")
	version = formValue(t, claimBody, "version")
	releaseForm := url.Values{"csrf_token": {csrfToken}, "version": {version}}
	releaseResponse := mustRequest(t, client, http.MethodPost, baseURL+"/stick/sticks/"+createdID+"/release", releaseForm.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if releaseResponse.StatusCode != http.StatusOK {
		t.Fatalf("release status = %d: %s\nprocess logs:\n%s", releaseResponse.StatusCode, readResponseBody(t, releaseResponse), logs.String())
	}
	releaseResponse.Body.Close()

	for i, endpoint := range endpoints {
		select {
		case body := <-endpoint.calls:
			if err := validate(body, createdID); err != nil {
				t.Fatalf("notification endpoint %d: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("release notification endpoint %d was not delivered; process logs:\n%s", i+1, logs.String())
		}
	}

	if err := process.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt stickd: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("stickd exit: %v\n%s", err, logs.String())
	}
}

func TestComposeDeclaresContainerRuntimeHardening(t *testing.T) {
	root := repositoryRoot(t)
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	compose = bytes.Replace(compose, []byte("- .env"), []byte("- "+envFile), 1)
	composePath := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(composePath, compose, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("docker", "compose", "--project-directory", root, "-f", composePath, "config", "--format", "json")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}
	var config struct {
		Services map[string]struct {
			ReadOnly    bool     `json:"read_only"`
			CapDrop     []string `json:"cap_drop"`
			SecurityOpt []string `json:"security_opt"`
			Tmpfs       []string `json:"tmpfs"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &config); err != nil {
		t.Fatal(err)
	}
	service, ok := config.Services["stickd"]
	if !ok {
		t.Fatal("compose configuration has no stickd service")
	}
	if !service.ReadOnly {
		t.Fatal("stickd container is not read-only")
	}
	if !containsString(service.CapDrop, "ALL") {
		t.Fatalf("cap_drop = %v, want ALL", service.CapDrop)
	}
	if !containsString(service.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("security_opt = %v, want no-new-privileges:true", service.SecurityOpt)
	}
	if !containsPrefix(service.Tmpfs, "/tmp:") {
		t.Fatalf("tmpfs = %v, want /tmp mount", service.Tmpfs)
	}
}

type fakeOIDCProvider struct {
	server *httptest.Server
}

func newFakeOIDCProvider(t *testing.T, clientID string) fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var providerServer *httptest.Server
	var idToken string
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": providerServer.URL, "authorization_endpoint": providerServer.URL + "/authorize",
				"token_endpoint": providerServer.URL + "/token", "jwks_uri": providerServer.URL + "/keys",
			})
		case "/authorize":
			redirectURL, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			if err != nil {
				http.Error(w, "invalid redirect", http.StatusBadRequest)
				return
			}
			query := redirectURL.Query()
			query.Set("code", "e2e-code")
			query.Set("state", r.URL.Query().Get("state"))
			redirectURL.RawQuery = query.Encode()
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-token", "token_type": "Bearer", "id_token": idToken})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "e2e-key",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	claims := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": providerServer.URL, "sub": "admin-sub", "aud": clientID,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"name": "Admin", "email": "admin@example.com", "email_verified": true,
	})
	claims.Header["kid"] = "e2e-key"
	idToken, err = claims.SignedString(key)
	if err != nil {
		providerServer.Close()
		t.Fatal(err)
	}
	t.Cleanup(providerServer.Close)
	return fakeOIDCProvider{server: providerServer}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func waitForURL(t *testing.T, target string, process *exec.Cmd, logs *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		if process.ProcessState != nil && process.ProcessState.Exited() {
			t.Fatalf("%s process exited with %s:\n%s", target, process.ProcessState, logs.String())
		}
		response, err := http.Get(target)
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
			_ = body
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not become ready: %v\nprocess logs:\n%s", target, err, logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func mustRequest(t *testing.T, client *http.Client, method, target, body string, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readResponseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func formValue(t *testing.T, body, name string) string {
	t.Helper()
	match := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("form value %q not found in %s", name, body)
	}
	return html.UnescapeString(match[1])
}

func yamlString(value string) string { return strconv.Quote(value) }

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
