package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

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
