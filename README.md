# Stick

Stick is a small, self-hosted coordination service for shared operational resources. Users claim a stick with a reason, release it when finished, and can subscribe to release notifications.

- Uses OIDC for login.
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

Edit `.env` and set the OIDC values, a random session secret, and the public URL. HTTPS is required for non-local deployments.

Register `<public-url>/auth/callback` as the OIDC client's redirect URI.

```sh
openssl rand -hex 32 # use the result for STICK_AUTH_SESSION_SECRET
docker compose up -d --build
```

Stick is available at `STICK_SERVER_PUBLIC_URL`. The Compose file binds it to loopback, so put it behind a TLS reverse proxy for remote access.

### Kubernetes

Create and edit the application configuration. Keep this file private because it contains credentials:

```sh
cp example.config.yaml config.yaml
# edit config.yaml and set a random session_secret
kubectl create namespace stick
kubectl -n stick create secret generic stick-config --from-file=config.yaml=./config.yaml
```

Edit `example.kubernetes.yaml` with your image name and apply it:

```sh
kubectl apply -f example.kubernetes.yaml
```

This creates a single-replica Deployment, Service, and persistent volume claim.

Expose the Service through an Ingress or Gateway, use its HTTPS URL as `STICK_SERVER_PUBLIC_URL`, and register `<public-url>/auth/callback` with the OIDC provider. 

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

- Configuration can be provided in YAML, environment variables, or both.
- Non-empty `STICK_*` variables override YAML values. 

See `.env.example` and `example.config.yaml` for the available settings.

If you use MongoDB, it must be deployed as a replica set because Stick uses multi-document transactions.

## Development

```sh
make test
make check
make image
```

The `compose.idp.yaml` override also starts Keycloak for local OIDC development.

Set `IDP_ADMIN_PASSWORD` in `.env`, configure Keycloak as your OIDC provider, and run it with:

```sh
docker compose -f compose.yaml -f compose.idp.yaml up -d --build
```
