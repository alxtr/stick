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
