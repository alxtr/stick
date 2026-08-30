// Package smtp sends release notifications over SMTP.
package smtp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"stick/internal/application"
)

const defaultTimeout = 10 * time.Second

const defaultSubject = `{{.StickName}} is available`

const defaultBody = `Hey {{.RecipientName}},

The stick you were waiting for just dropped.

{{.StickName}} was held by {{.HolderEmail}} for {{.Duration}}.
Released at {{.ReleasedAt}}

→ Claim it now: {{.BaseURL}}/sticks/{{.StickID}}`

const (
	// TLSModeStartTLS upgrades a plaintext SMTP connection before sending
	// credentials or messages. It is the default mode.
	TLSModeStartTLS = "starttls"
	// TLSModeImplicit establishes TLS before beginning the SMTP exchange.
	TLSModeImplicit = "implicit"
)

// Config holds SMTP connection settings.
type Config struct {
	Host     string
	Port     int
	TLSMode  string
	Username string
	Password string
	From     string
}

// Templates contains the Go text/templates used to render an email.
type Templates struct {
	Subject string
	Body    string
}

type sendContextFunc func(ctx context.Context, addr, tlsMode string, auth netsmtp.Auth, from string, to []string, msg []byte) error

// Notifier sends notifications via SMTP.
type Notifier struct {
	cfg         Config
	subjectTmpl *template.Template
	bodyTmpl    *template.Template
	send        sendContextFunc
}

var _ application.Notifier = (*Notifier)(nil)

// New creates an SMTP notifier, parsing the subject and body as Go text/templates.
func New(cfg Config, templates Templates) (*Notifier, error) {
	return newNotifier(cfg, templates, sendMailContext)
}

func newNotifier(cfg Config, templates Templates, send sendContextFunc) (*Notifier, error) {
	validated, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if templates.Subject == "" {
		templates.Subject = defaultSubject
	}
	if templates.Body == "" {
		templates.Body = defaultBody
	}
	st, err := template.New("subject").Parse(templates.Subject)
	if err != nil {
		return nil, fmt.Errorf("subject template: %w", err)
	}
	bt, err := template.New("body").Parse(templates.Body)
	if err != nil {
		return nil, fmt.Errorf("body template: %w", err)
	}
	if send == nil {
		send = sendMailContext
	}
	return &Notifier{
		cfg:         validated,
		subjectTmpl: st,
		bodyTmpl:    bt,
		send:        send,
	}, nil
}

// Notify sends one notification through SMTP.
func (n *Notifier) Notify(ctx context.Context, notification application.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMailbox("recipient", notification.RecipientEmail); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	var subBuf, bodyBuf bytes.Buffer
	if err := n.subjectTmpl.Execute(&subBuf, notification); err != nil {
		return fmt.Errorf("render subject: %w", err)
	}
	if err := n.bodyTmpl.Execute(&bodyBuf, notification); err != nil {
		return fmt.Errorf("render body: %w", err)
	}

	renderedSubject := subBuf.String()
	if containsNewline(renderedSubject) {
		return fmt.Errorf("subject must not contain CR or LF characters")
	}
	subject := strings.TrimSpace(renderedSubject)
	body := bodyBuf.String()

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", n.cfg.From)
	fmt.Fprintf(&msg, "To: %s\r\n", notification.RecipientEmail)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.WriteString(body)

	addr := net.JoinHostPort(n.cfg.Host, strconv.Itoa(n.cfg.Port))
	var auth netsmtp.Auth
	if n.cfg.Username != "" {
		auth = netsmtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return n.send(ctx, addr, n.cfg.TLSMode, auth, n.cfg.From, []string{notification.RecipientEmail}, msg.Bytes())
}

func normalizeConfig(cfg Config) (Config, error) {
	if containsNewline(cfg.Host) {
		return Config{}, fmt.Errorf("SMTP host must not contain CR or LF characters")
	}
	if containsNewline(cfg.From) {
		return Config{}, fmt.Errorf("SMTP from address must not contain CR or LF characters")
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.TLSMode = strings.ToLower(strings.TrimSpace(cfg.TLSMode))
	if cfg.TLSMode == "" {
		cfg.TLSMode = TLSModeStartTLS
	}

	if cfg.Host == "" {
		return Config{}, fmt.Errorf("SMTP host is required")
	}
	if strings.Contains(cfg.Host, ":") && net.ParseIP(cfg.Host) == nil {
		return Config{}, fmt.Errorf("SMTP host must not include a port")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("SMTP port must be between 1 and 65535")
	}
	if cfg.TLSMode != TLSModeStartTLS && cfg.TLSMode != TLSModeImplicit {
		return Config{}, fmt.Errorf("unsupported SMTP TLS mode %q", cfg.TLSMode)
	}
	if err := validateMailbox("SMTP from address", cfg.From); err != nil {
		return Config{}, err
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return Config{}, fmt.Errorf("SMTP username and password must be configured together")
	}
	return cfg, nil
}

func validateMailbox(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if containsNewline(value) {
		return fmt.Errorf("%s must not contain CR or LF characters", field)
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return fmt.Errorf("%s must be a valid email address", field)
	}
	return nil
}

func containsNewline(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}
