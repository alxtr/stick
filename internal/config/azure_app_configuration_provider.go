package config

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/AppConfiguration-GoProvider/azureappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// AzureAppConfigurationProvider loads settings from Azure App Configuration
// using an Azure Identity credential. Settings are selected by KeyPrefix (or
// KeyFilter) and Label, then applied to the accumulated configuration.
//
// Azure App Configuration keys use slash-separated paths such as
// "stick/server/public_url". A KeyPrefix, when configured, is removed before
// the path is interpreted. The default label selects unlabeled settings.
// Notification settings should be stored as JSON values with the appropriate
// JSON content type, for example under "stick/notifications/webhook".
// Separator controls how hierarchical keys are constructed. It defaults to
// "/" to match the examples above.
//
// When Credential is nil, Apply uses azidentity.DefaultAzureCredential. This
// supports managed identity and workload identity without putting credentials
// in application configuration. Key Vault references are resolved using the
// same credential.
type AzureAppConfigurationProvider struct {
	Endpoint   string
	Label      string
	KeyPrefix  string
	KeyFilter  string
	Separator  string
	Credential azcore.TokenCredential
	Options    *azureappconfiguration.Options
}

// Apply loads the selected Azure App Configuration settings and applies them
// atomically to the accumulated configuration.
func (p AzureAppConfigurationProvider) Apply(ctx context.Context, config *Config) error {
	endpoint, err := validateAzureAppConfigurationEndpoint(p.Endpoint)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	credential := p.Credential
	if credential == nil {
		credential, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return fmt.Errorf("creating Azure credential: %w", err)
		}
	}

	separator, err := normalizeAzureAppConfigurationSeparator(p.Separator)
	if err != nil {
		return err
	}
	options := azureAppConfigurationOptions(p.Options, p.KeyPrefix, p.KeyFilter, p.Label, separator, credential)
	loaded, err := azureappconfiguration.Load(ctx, azureappconfiguration.AuthenticationOptions{
		Endpoint:   endpoint,
		Credential: credential,
	}, options)
	if err != nil {
		return fmt.Errorf("loading Azure App Configuration: %w", err)
	}

	data, err := loaded.GetBytes(&azureappconfiguration.ConstructionOptions{Separator: separator})
	if err != nil {
		return fmt.Errorf("constructing Azure App Configuration values: %w", err)
	}
	source, err := decodeYAMLConfig(data)
	if err != nil {
		return fmt.Errorf("parsing Azure App Configuration values: %w", err)
	}

	updated := *config
	if err := applyYAMLConfig(source, &updated); err != nil {
		return fmt.Errorf("applying Azure App Configuration values: %w", err)
	}
	*config = updated
	return nil
}

func validateAzureAppConfigurationEndpoint(value string) (string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Azure App Configuration endpoint: HTTPS URL without credentials, query, or fragment is required")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("invalid Azure App Configuration endpoint: URL path is not supported")
	}
	return endpoint, nil
}

func azureAppConfigurationOptions(
	base *azureappconfiguration.Options,
	keyPrefix, keyFilter, label, separator string,
	credential azcore.TokenCredential,
) *azureappconfiguration.Options {
	options := azureappconfiguration.Options{}
	if base != nil {
		options = *base
		options.TrimKeyPrefixes = append([]string(nil), base.TrimKeyPrefixes...)
		options.Selectors = append([]azureappconfiguration.Selector(nil), base.Selectors...)
	}

	keyPrefix = normalizeAzureAppConfigurationPrefix(keyPrefix, separator)
	keyFilter = strings.TrimSpace(keyFilter)
	if keyFilter == "" {
		keyFilter = keyPrefix + "*"
	}
	label = strings.TrimSpace(label)
	options.Selectors = []azureappconfiguration.Selector{{
		KeyFilter:   keyFilter,
		LabelFilter: label,
	}}
	if keyPrefix != "" {
		options.TrimKeyPrefixes = []string{keyPrefix}
	}

	// A single identity can authenticate to both App Configuration and Key
	// Vault. Preserve custom Key Vault authentication or a custom resolver,
	// while making the normal workload-identity path work out of the box.
	if options.KeyVaultOptions.Credential == nil {
		options.KeyVaultOptions.Credential = credential
	}
	return &options
}

const azureAppConfigurationDefaultSeparator = "/"

func normalizeAzureAppConfigurationSeparator(value string) (string, error) {
	separator := strings.TrimSpace(value)
	if separator == "" {
		return azureAppConfigurationDefaultSeparator, nil
	}
	switch separator {
	case ".", ",", ";", "-", "_", "__", "/", ":":
		return separator, nil
	default:
		return "", fmt.Errorf("invalid Azure App Configuration hierarchy separator %q", separator)
	}
}

func normalizeAzureAppConfigurationPrefix(value, separator string) string {
	prefix := strings.TrimSpace(value)
	if prefix == "" {
		return ""
	}
	if !strings.HasSuffix(prefix, separator) {
		prefix += separator
	}
	return prefix
}
