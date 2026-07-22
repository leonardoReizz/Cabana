# Service templates

One YAML file per service in the catalogue. These are embedded into the cabana
binary at build time, so this directory is the canonical location — adding a
service means adding a file here and nothing else.

A template is validated at boot: a malformed file fails the process rather than
failing later when someone clicks Install. Run `go test ./internal/catalog/...`
to check a new one without starting cabana.

See `redis.yaml` for the smallest complete example.
