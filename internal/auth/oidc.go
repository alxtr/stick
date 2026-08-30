package auth

// OIDCConfig contains the provider credentials used by the authentication
// flow.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}
