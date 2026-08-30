package config

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAMLProvider loads configuration from a YAML file. An empty Path uses
// config.yaml in the working directory; that default file is optional. A
// non-empty Path must exist.
type YAMLProvider struct {
	Path string
}

// Apply reads and strictly decodes the configured YAML document before
// applying the values present in it to config. The YAML representation is
// private to this provider; the rest of the application only sees Config.
func (p YAMLProvider) Apply(ctx context.Context, config *Config) error {
	filePath := p.Path
	optional := filePath == ""
	if optional {
		filePath = "config.yaml"
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading config file %q: %w", filePath, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	source, err := decodeYAMLConfig(data)
	if err != nil {
		return fmt.Errorf("parsing config file %q: %w", filePath, err)
	}

	updated := *config
	if err := applyYAMLConfig(source, &updated); err != nil {
		return err
	}
	*config = updated
	return nil
}

func decodeYAMLConfig(data []byte) (yamlConfig, error) {
	source := yamlConfig{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&source); err != nil {
		return yamlConfig{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple YAML documents are not supported")
		}
		return yamlConfig{}, err
	}
	return source, nil
}

type yamlConfig struct {
	Server        yamlServerConfig        `yaml:"server" json:"server"`
	Database      *string                 `yaml:"database" json:"database"`
	Auth          yamlAuthConfig          `yaml:"auth" json:"auth"`
	Timezone      *string                 `yaml:"timezone" json:"timezone"`
	Notifications yamlNotificationsConfig `yaml:"notifications" json:"notifications"`
}

type yamlServerConfig struct {
	PublicURL  *string `yaml:"public_url" json:"public_url"`
	ListenAddr *string `yaml:"listen_addr" json:"listen_addr"`
}

type yamlAuthConfig struct {
	IDPEndpoint *string   `yaml:"idp_endpoint" json:"idp_endpoint"`
	Audience    *string   `yaml:"audience" json:"audience"`
	Scope       *string   `yaml:"scope" json:"scope"`
	AdminEmails *[]string `yaml:"admin_emails" json:"admin_emails"`
}

type yamlNotificationsConfig struct {
	SMTP    yamlSMTPConfigs    `yaml:"smtp" json:"smtp"`
	Webhook yamlWebhookConfigs `yaml:"webhook" json:"webhook"`
	Teams   yamlTeamsConfigs   `yaml:"teams" json:"teams"`
}

type yamlSMTPConfigs []*yamlSMTPConfig
type yamlWebhookConfigs []*yamlWebhookConfig
type yamlTeamsConfigs []*yamlTeamsConfig

type yamlSMTPConfig struct {
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	TLSMode  string `yaml:"tls_mode" json:"tls_mode"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
	From     string `yaml:"from" json:"from"`
	Subject  string `yaml:"subject" json:"subject"`
	Body     string `yaml:"body" json:"body"`
}

type yamlWebhookConfig struct {
	URL string `yaml:"url" json:"url"`
}

type yamlTeamsConfig struct {
	URL string `yaml:"url" json:"url"`
}

func applyYAMLConfig(source yamlConfig, config *Config) error {
	if source.Database != nil {
		if err := setDatabase(config, *source.Database); err != nil {
			return err
		}
	}
	if source.Server.PublicURL != nil {
		if err := setPublicURL(config, *source.Server.PublicURL); err != nil {
			return err
		}
	}
	if source.Server.ListenAddr != nil {
		config.Server.ListenAddr = strings.TrimSpace(*source.Server.ListenAddr)
	}
	if source.Auth.IDPEndpoint != nil {
		config.Auth.IDPEndpoint = *source.Auth.IDPEndpoint
	}
	if source.Auth.Audience != nil {
		config.Auth.Audience = *source.Auth.Audience
	}
	if source.Auth.Scope != nil {
		config.Auth.Scope = *source.Auth.Scope
	}
	if source.Auth.AdminEmails != nil {
		config.Auth.AdminEmails = append([]string(nil), (*source.Auth.AdminEmails)...)
	}
	if source.Timezone != nil {
		if err := setTimezone(config, *source.Timezone); err != nil {
			return err
		}
	}
	if source.Notifications.SMTP != nil {
		smtp, err := applyYAMLSMTP(source.Notifications.SMTP)
		if err != nil {
			return err
		}
		config.Notifications.SMTP = smtp
	}
	if source.Notifications.Webhook != nil {
		webhooks, err := applyYAMLWebhooks(source.Notifications.Webhook)
		if err != nil {
			return err
		}
		config.Notifications.Webhook = webhooks
	}
	if source.Notifications.Teams != nil {
		teams, err := applyYAMLTeams(source.Notifications.Teams)
		if err != nil {
			return err
		}
		config.Notifications.Teams = teams
	}
	return nil
}

func applyYAMLSMTP(source yamlSMTPConfigs) ([]*SMTPConfig, error) {
	result := make([]*SMTPConfig, 0, len(source))
	for _, item := range source {
		if item == nil {
			return nil, fmt.Errorf("notifications.smtp entries must be mappings")
		}
		result = append(result, &SMTPConfig{
			Host:     strings.TrimSpace(item.Host),
			Port:     item.Port,
			TLSMode:  strings.ToLower(strings.TrimSpace(item.TLSMode)),
			Username: item.Username,
			Password: item.Password,
			From:     strings.TrimSpace(item.From),
			Subject:  item.Subject,
			Body:     item.Body,
		})
	}
	return result, nil
}

func applyYAMLWebhooks(source yamlWebhookConfigs) ([]*WebhookConfig, error) {
	result := make([]*WebhookConfig, 0, len(source))
	for _, item := range source {
		if item == nil {
			return nil, fmt.Errorf("notifications.webhook entries must be mappings")
		}
		result = append(result, &WebhookConfig{URL: strings.TrimSpace(item.URL)})
	}
	return result, nil
}

func applyYAMLTeams(source yamlTeamsConfigs) ([]*TeamsConfig, error) {
	result := make([]*TeamsConfig, 0, len(source))
	for _, item := range source {
		if item == nil {
			return nil, fmt.Errorf("notifications.teams entries must be mappings")
		}
		result = append(result, &TeamsConfig{URL: strings.TrimSpace(item.URL)})
	}
	return result, nil
}

func (c *yamlSMTPConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeYAMLNotificationConfigs[yamlSMTPConfig](node, "smtp", []string{
		"host", "port", "tls_mode", "username", "password", "from", "subject", "body",
	})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func (c *yamlWebhookConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeYAMLNotificationConfigs[yamlWebhookConfig](node, "webhook", []string{"url"})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func (c *yamlTeamsConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeYAMLNotificationConfigs[yamlTeamsConfig](node, "teams", []string{"url"})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func decodeYAMLNotificationConfigs[T any](node *yaml.Node, name string, fields []string) ([]*T, error) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return nil, nil
	}

	if node.Kind == yaml.MappingNode {
		if err := validateYAMLNotificationFields(node, name, fields); err != nil {
			return nil, err
		}
		var item T
		if err := node.Decode(&item); err != nil {
			return nil, err
		}
		return []*T{&item}, nil
	}

	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("notifications.%s must be a mapping or sequence", name)
	}
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && item.Tag == "!!null" {
			continue
		}
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("notifications.%s entries must be mappings", name)
		}
		if err := validateYAMLNotificationFields(item, name, fields); err != nil {
			return nil, err
		}
	}

	var items []*T
	if err := node.Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func validateYAMLNotificationFields(node *yaml.Node, name string, fields []string) error {
	known := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		known[field] = struct{}{}
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, ok := known[key]; !ok {
			return fmt.Errorf("field %s not found in notifications.%s", key, name)
		}
	}
	return nil
}
