# Contributing

Bug reports and pull requests are welcome in the
[issue tracker](https://github.com/Sam-H101/grafana-drill-datasource/issues).

## Building and testing

Everything runs in Docker; nothing else needs installing. The toolbox
container, the dev stack (Grafana plus three Drill servers) and every command
are described in [devenv/README.md](devenv/README.md). In short:

```sh
devenv/run.sh npm ci
devenv/run.sh npm run build            # frontend
devenv/run.sh mage -v build:linux      # backend
docker compose up -d --build --wait    # Grafana on http://localhost:3000
```

Before opening a pull request, run the checks CI runs:

```sh
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci
devenv/run.sh mage test
devenv/run.sh golangci-lint run ./...
devenv/run.sh go test -tags integration ./tests/integration/...
devenv/run.sh npm run e2e
```

## Layout

| Path | Contents |
| --- | --- |
| `src/` | Frontend: config editor, query editor, variable support |
| `pkg/drill/` | Drill REST client, response decoding, type mapping |
| `pkg/macros/` | Time macros (`$__timeFilter`, `$__timeGroup`, ...) |
| `pkg/plugin/` | Query, health check and resource handlers |
| `tests/e2e/`, `tests/integration/` | Playwright and Go tests against the dev stack |
| `provisioning/` | Data source, demo dashboard and alert rule for the dev stack |
| `docs/drill-rest-notes.md` | Drill REST behaviour the backend relies on |

Files under `.config/` come from `@grafana/create-plugin`; change them with
`npx @grafana/create-plugin@latest update` rather than by hand.

## Installing a build without the catalog

Unzip a release archive into Grafana's plugins directory, so that you have
`<plugins>/smartquinoa1504-apachedrill-datasource/plugin.json`. Archives from
this repository are unsigned until the plugin is approved for the Grafana
catalog, so allow it in `grafana.ini` and restart Grafana:

```ini
[plugins]
allow_loading_unsigned_plugins = smartquinoa1504-apachedrill-datasource
```

## Releasing

1. Set the version in `package.json` (and `package-lock.json`) and add a dated
   entry to `CHANGELOG.md`.
2. Push a tag that matches it, for example `v0.1.1`. The Release workflow
   builds the plugin for every platform and creates a draft GitHub release
   with the zip and its SHA-1.

Signing is controlled by repository variables, described at the top of
`.github/workflows/release.yml`.
