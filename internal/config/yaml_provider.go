package config

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// YAMLProvider loads configuration from a YAML file. An empty Path uses
// config.yaml in the working directory; that default file is optional. A
// non-empty Path must exist.
type YAMLProvider struct {
	Path string
}

// Apply reads and strictly decodes the configured YAML document into raw. YAML
// decoding preserves fields that are not present in the document, allowing
// this provider to be used at any position in the provider list.
func (p YAMLProvider) Apply(ctx context.Context, raw *rawConfig) error {
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

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(raw); err != nil {
		return fmt.Errorf("parsing config file %q: %w", filePath, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple YAML documents are not supported")
		}
		return fmt.Errorf("parsing config file %q: %w", filePath, err)
	}
	return nil
}

// The notification fields accept both the original mapping form for one
// backend and a sequence form for multiple instances of that backend.
type rawSMTPConfigs []*rawSMTPConfig
type rawWebhookConfigs []*rawWebhookConfig
type rawTeamsConfigs []*rawTeamsConfig

func (c *rawSMTPConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeNotificationConfigs[rawSMTPConfig](node, "smtp", []string{
		"host", "port", "tls_mode", "username", "password", "from", "subject", "body",
	})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func (c *rawWebhookConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeNotificationConfigs[rawWebhookConfig](node, "webhook", []string{"url"})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func (c *rawTeamsConfigs) UnmarshalYAML(node *yaml.Node) error {
	items, err := decodeNotificationConfigs[rawTeamsConfig](node, "teams", []string{"url"})
	if err != nil {
		return err
	}
	*c = items
	return nil
}

func decodeNotificationConfigs[T any](node *yaml.Node, name string, fields []string) ([]*T, error) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return nil, nil
	}

	if node.Kind == yaml.MappingNode {
		if err := validateNotificationFields(node, name, fields); err != nil {
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
		if err := validateNotificationFields(item, name, fields); err != nil {
			return nil, err
		}
	}

	var items []*T
	if err := node.Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func validateNotificationFields(node *yaml.Node, name string, fields []string) error {
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
