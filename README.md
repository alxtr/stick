# Stick

Stick is a small, self-hosted coordination service for shared operational resources. Users claim a stick with a reason, release it when finished, and can subscribe to release notifications.

- Provides a JSON REST API with bearer-token authentication and optimistic
  concurrency using ETags.
- Supports different storage backends:
  - SQLite (default)
  - PostgreSQL
  - MongoDB
- Supports multiple notification modes:
  - Email
  - Webhooks
  - Microsoft Teams

## Install

Clone the repository:

```sh
git clone https://github.com/alxtr/the-stick.git
cd the-stick
```

### Docker Compose

Copy the example environment file:

```sh
cp .env.example .env
```

Edit `.env` and set the identity-provider endpoint and expected audience and
scope. The public URL is optional; HTTPS is required for non-local URLs.

```sh
docker compose up -d --build
```

The API is available at `STICK_SERVER_PUBLIC_URL/api/v1`. The Compose file
binds it to loopback, so put it behind a TLS reverse proxy for remote access.

### Kubernetes

Create and edit the application configuration. Keep this file private because it contains credentials:

```sh
cp example.config.yaml config.yaml
# edit config.yaml and set the API authentication values
kubectl create namespace stick
kubectl -n stick create secret generic stick-config --from-file=config.yaml=./config.yaml
```

Edit `example.kubernetes.yaml` with your image name and apply it:

```sh
kubectl apply -f example.kubernetes.yaml
```

This creates a single-replica Deployment, Service, and persistent volume claim.

Expose the Service through an Ingress or Gateway, and use its HTTPS URL as
`STICK_SERVER_PUBLIC_URL`.

If the application is mounted under a path, include that path in the probe URLs as well.

## Build from source

Go 1.27 or newer is required.

```sh
make build
```

This creates `./stickd`. To run the binary, copy and edit `example.config.yaml`, then run:

```sh
cp example.config.yaml config.yaml
# edit config.yaml
install -d -m 0700 local-data
STICK_DATABASE="$PWD/local-data/stick.db" ./stickd -config config.yaml
```

- Configuration can be provided in YAML, environment variables, Azure App
  Configuration, or a combination of providers.
- Non-empty `STICK_*` variables override values from earlier providers.

See `.env.example` and `example.config.yaml` for the available settings.

### Azure App Configuration

Stick can also load configuration from Azure App Configuration. Select it with
the bootstrap setting below; `environment` remains last so ordinary `STICK_*`
overrides retain precedence:

```sh
STICK_CONFIG_PROVIDERS=azure-app-config,environment
STICK_AZURE_APPCONFIG_ENDPOINT=https://stick-prod.azconfig.io
STICK_AZURE_APPCONFIG_LABEL=production
STICK_AZURE_APPCONFIG_KEY_PREFIX=stick/
STICK_AZURE_APPCONFIG_SEPARATOR=/
```

When Azure is the only structured source, omit `-config`; when combining it
with a file, use `STICK_CONFIG_PROVIDERS=yaml,azure-app-config,environment`.

Use slash-separated keys such as `stick/database`,
`stick/server/public_url`, and `stick/auth/idp_endpoint`. Notification lists
should be stored as JSON values with the `application/json` content type.
The hierarchy separator is configurable (supported values include `/`, `.`,
`:` and `__`) and defaults to `/`.
Empty labels select unlabeled settings. Azure App Configuration Key Vault
references are resolved automatically when the identity can read the referenced
secrets.

Authentication uses Azure's default credential chain. In production, prefer a
managed identity or workload identity and grant it the **App Configuration Data
Reader** role (and **Key Vault Secrets User** when Key Vault references are
used). No Azure credentials are stored in the Stick configuration.

The provider loads a snapshot during startup. Configuration changes require a
Stick restart.

If you use MongoDB, it must be deployed as a replica set because Stick uses multi-document transactions.

## Development

```sh
make test
make check
make image
```

Send an external JWT in the `Authorization: Bearer <token>` header. Resource
updates require the current `If-Match` ETag; reads return an ETag and support
`If-None-Match`.

The API provides stick listing, creation, rename, archive/unarchive, claim,
release, history, and notification subscription endpoints below
`/api/v1`.
