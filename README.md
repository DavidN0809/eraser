# Eraser — maintained self-hosted fork

An authenticated, single-user application for reviewing and sending **individual,
explicitly approved** data-broker removal requests. No real requests are sent on
installation, startup, preview, restart, CI, or dry-run.

- Source of truth: [Nichols-HomeLab/eraser on Gitea](https://git.nicholstech.org/Nichols-HomeLab/eraser)
- GitHub fork/builds: [DavidN0809/eraser](https://github.com/DavidN0809/eraser)
- Upstream: [digisamroc/eraser](https://github.com/digisamroc/eraser), retained as `upstream`
- Image: `git.nicholstech.org/nichols-homelab/eraser:stable`
- Build mirror: `ghcr.io/davidn0809/eraser:stable`
- Audit and limitations: [SECURITY_AUDIT.md](SECURITY_AUDIT.md)

This is a deliberately narrower fork. The unauthenticated wizard, bulk sends,
automatic resumption, inbox ingestion/archiving, bounce-driven catalog deletion,
and browser form/confirmation automation have been removed. Follow up manually
in your mail client. The broker catalog remains a set of **unverified leads**;
it is not evidence that any broker holds your information. Sending even an empty
request reveals your sender email and can create a new association.

## Docker Compose

Requires Docker Engine with Compose v2 on Linux. From this checkout:

```sh
sudo ./scripts/prepare-compose.sh  # synthetic config + empty SMTP secret, mode 0600
# Edit secrets/config.yaml locally; keep dry_run: true initially.
docker compose up -d
docker compose exec eraser /eraser auth-token
```

Open **http://localhost:8080**. Username: `eraser`. Password: the random token from
the explicit `auth-token` command. It is generated on the private data volume,
never logged, and has no default value. Treat the command's output as a secret.
No personal data or SMTP password is needed to start the UI. The initial
configuration deliberately approves no recipients.

The bootstrap script needs root only to set ownership on **host secret files**.
The application always runs as UID/GID 65532. Compose file secrets preserve
host ownership, so keep `secrets/config.yaml` and `secrets/smtp-password` owned
by 65532 with mode 0600. Never commit them. An empty SMTP secret is acceptable
in preview mode. For a local build: `docker compose build --pull && docker compose up -d`.

`eraser-data` persists SQLite history and the bootstrap authentication token.
The root filesystem is read-only, all capabilities are dropped, privilege
escalation is disabled, and resource limits apply. Only loopback port 8080 is
published. Remote access should use an SSH tunnel or a trusted HTTPS reverse
proxy. Do not expose the HTTP port directly.

For a supplied web authentication secret, mount a mode-0600 file and set
`ERASER_AUTH_TOKEN_FILE` to its container path. It must contain at least 32
random bytes (for example 32 random bytes hex-encoded), not a memorable password.
Restart after rotating it. Browser Basic authentication has no logout flow;
close the browser session when finished.

## Kubernetes

Requires a default StorageClass with ReadWriteOnce/filesystem ownership support,
a CNI enforcing NetworkPolicy, and nodes capable of running linux/amd64 images.
The manifests use a single replica and Recreate updates for SQLite.

For an authenticated, empty preview installation:

```sh
kubectl apply -f k8s/
kubectl -n eraser rollout status deployment/eraser
kubectl -n eraser exec deployment/eraser -- /eraser auth-token
kubectl -n eraser port-forward service/eraser 8080:8080
```

Open http://localhost:8080 using the credentials described above. The optional
Secret can be absent on first boot; there are then no configured recipients and
no delivery capability. Nothing resumes from legacy job files.

To load a private configuration and SMTP secret, prepare local files from the
example, then create the Secret **without putting values in shell arguments**:

```sh
kubectl -n eraser create secret generic eraser-secrets \
  --from-file=config.yaml=./secrets/config.yaml \
  --from-file=smtp_password=./secrets/smtp-password \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n eraser rollout restart deployment/eraser
```

For the Nichols homelab, store the encrypted Secret and workload manifests in
`k3s-fluxcd` using its SOPS/Flux conventions before a durable deployment. The
commands above are the portable standalone option; do not introduce unmanaged
permanent cluster state into the homelab. Kubernetes Secrets are base64-encoded,
not encrypted merely because they are Secrets: restrict RBAC, enable storage
and backup encryption, and avoid printing/dumping them.

The PVC requests 1 GiB and uses the default StorageClass. Set `storageClassName`
to a suitable RWO block-backed class if no default exists. Avoid shared NFS for
SQLite. The deployment disables service-account token mounting, runs non-root,
uses RuntimeDefault seccomp, drops every capability, and has a read-only root.
The Service is ClusterIP; there is no Ingress or LoadBalancer.

NetworkPolicy denies ingress and egress by default. `kubectl port-forward` is
the intended initial access path. If your CNI restricts port-forward traffic,
use its documented narrowly scoped access policy. Before live SMTP, allow DNS
to your cluster resolver and TCP to the **specific trusted SMTP relay IP/port**.
Do not allow arbitrary HTTP(S) egress or inbound access from all namespaces.
See [k8s-examples](docs/k8s-examples.md). A TLS proxy requires an ingress rule
limited to that proxy and `ERASER_PUBLIC_ORIGIN=https://your-exact-host`.
The proxy must preserve Host and suppress authentication-header logging.

## Approve a recipient and minimize disclosure

Use `config.example.yaml` as the schema. Configuration is loaded once; restart
after changes. Unknown fields, legacy embedded passwords, and world-readable
files are rejected. The config itself contains PII and must be protected even
though credentials are stored separately.

```yaml
options:
  dry_run: true
  template: generic
  rate_limit_ms: 2000
  regions: []
  excluded_brokers: []
  approved_brokers:
    example-broker-id:
      email: privacy@example.invalid
      fields: [name]
```

Replace the example ID/email only after independently verifying the actual
recipient. The exact catalog email must match the approval. Changed upstream
recipients fail closed until reapproved. An empty field list sends no profile
fields; the sender address remains visible in SMTP. Optional fields are `name`,
`email`, `address`, `city`, `state`, `zip_code`, `country`, `phone`,
`date_of_birth`. DOB, address, phone, and profile email are never included merely
because they exist in your profile. Do not approve more than the broker needs.

Web: click Preview, inspect the exact recipient and complete body, then confirm
one request. The approval expires after five minutes, is single-use, and is
invalidated if the rendered request changes. There is no send-all endpoint.

CLI preview also prints the exact message; **its output can contain PII**, so
keep it out of shared logs:

```sh
docker compose exec eraser /eraser send --broker example-broker-id --dry-run
```

## Deliberately enable live mail

Do this only after reviewing the audit and a preview. Both controls are required:

1. Set `options.dry_run: false` in the private config.
2. Set deployment environment `ERASER_ENABLE_SEND: "true"`, then recreate/restart.

SMTP supports `tls_mode: implicit` (usually 465) or `starttls` (usually 587).
Certificate/hostname verification is mandatory, TLS is at least 1.2, and STARTTLS
must be advertised and succeed before AUTH/MAIL. There is no plaintext fallback
or certificate-validation bypass. `password_file` points to the mounted secret;
SMTP secrets are never echoed to the UI, serialized in YAML, or recorded in
SQLite. Use a dedicated low-privilege app password/mail account. API providers
and inbox passwords are not supported in this fork.

Then use the web confirmation or pass the CLI's reviewed `--approve-sha256`
digest. `--dry-run` always wins, regardless of the live environment switch.
There are no periodic sends or retry jobs. An `attempted` history record without
a later result means delivery is uncertain: check the mail provider before
retrying. A returned SMTP error can also occur after the provider accepted DATA;
this application intentionally does not retry automatically.

## Updates, backups, and rollback

The build pipeline tests, runs Go vet/staticcheck/gosec/govulncheck, scans Git
history/current files with Gitleaks, builds a candidate, scans that **actual
image** with Trivy, and exercises restricted runtime/Compose startup. Only then
does it publish `stable` and `sha-<commit>` to Gitea and GHCR. PRs receive checks
but no registry secrets/publication. Actions are commit-pinned and the Go builder
is digest-pinned. Weekly rebuilds rerun current vulnerability databases;
Dependabot proposes Go/base-image/Action updates. Failed scans do not update
`stable`. See the Actions run and artifacts for each release.

Prefer a registry digest in long-term deployments; `stable` is a convenience
tag, not an immutable release. Back up first, review the change/audit, then:

```sh
docker compose pull
docker compose up -d
# Kubernetes: edit the image digest in k8s/20-deployment.yaml, then:
kubectl apply -f k8s/
kubectl -n eraser rollout status deployment/eraser
```

To roll back, use the prior image digest. Stop the app before copying SQLite
(and any sidecars) or snapshotting its volume, then restart. Back up the private
configuration and secrets separately using encryption/access controls. Do not
publish backups. Restore into an isolated preview deployment first. UID 65532
must own restored state. The volume includes the web token: protect it and rotate
it after an untrusted restore. Never restore a legacy `config.yaml` directly;
migrate field allowlists/secret files and start with a new data volume. The fork
does not load legacy inbox/body/profile/job tables; old database files can still
contain that data until you retire them securely.

Upstream maintenance:

```sh
git remote add upstream https://github.com/digisamroc/eraser.git # if absent
./scripts/update-upstream.sh
```

The script fetches current Gitea main and upstream, creates a dedicated worktree,
and stages a merge for review without executing upstream code or pushing. Expect
conflicts in security-sensitive components. Review the catalog diff separately;
never infer ownership from a matching domain or broker-list membership. Preserve
all safety regression tests, update SECURITY_AUDIT.md, run the same checks as CI,
commit and push **Gitea main first**, then fast-forward the GitHub build fork.
`upstream-watch` reports new upstream commits weekly; it never auto-merges them.
Treat GitHub dependency PRs similarly: integrate through Gitea before publication.

The source currently retains upstream's original module path to keep merge
history understandable. No LICENSE file was present in the audited upstream
revision; clarify licensing with the upstream maintainer before redistribution
beyond the requested fork/build workflow.

## Local verification

Use Go 1.27.1. No application credentials are needed.

```sh
go mod verify
go test -race ./...
go vet ./...
staticcheck ./...
gosec ./...
govulncheck ./...
gitleaks git . --redact
gitleaks dir . --redact
docker build -t eraser:test .
trivy image --scanners vuln,secret --severity HIGH,CRITICAL --exit-code 1 eraser:test
```

Fixtures use `.invalid` domains and local stub servers only. Do not supply real
SMTP credentials or enable actual broker requests while developing tests.
