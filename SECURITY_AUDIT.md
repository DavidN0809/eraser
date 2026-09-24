# Security and privacy audit

Date: 2026-09-24. Upstream: `digisamroc/eraser`, commit
`ff6cd50e84dfca809a2e08c18b9358a7b3e2e448`. Maintained implementation: this fork.

The baseline repository-wide audit covered all 50 tracked files, including CLI,
HTTP/UI, SMTP, IMAP/MIME, browser automation, SQLite, config, templates, embedded
assets, dependency manifests and the 764-entry broker catalog. An independent
baseline audit, architecture review, focused mail/privacy review and parent
source validation were combined into 10 distinct source findings. The immutable
baseline report is retained in the Codex Security scan
`57103918-1a64-4a1d-b8f8-45e484dcd95a`. Dependency and image scans are separate
measured checks, not a claim that every dependency's source was manually audited.

No real profile, application credentials, inbox, or broker requests were used.
Tests use synthetic `.invalid` addresses and local stub listeners. Forge/build
credentials were used only for the authorized repository and release workflow.

## Severity and disposition

Severity reflects the original **loopback-only** application and concrete
prerequisites. Exposing that version to a network without authentication would
increase several risks. No Critical source finding was established. One High
and nine Medium source findings were validated. Lower-level observations and
remaining Low risks follow below. Scanner advisory counts are not additional
independently validated exploitable application findings.

| ID | Severity | Finding and original evidence | Resolution in this fork |
|---|---|---|---|
| A1 | High | `internal/browser/browser.go:117-168`, `filler.go:61-270`, `inbox/parser.go:374-415`: unapproved email-derived or redirected form origins receive the entire profile while fields are filled, even without submission. Stored inbox path requires a compatible old DB because the fresh schema is broken. | Removed Chrome/form filling and all inbox-to-network automation. No remote page receives profile values from this application. Manual follow-up is required. |
| A2 | Medium | `internal/web/server.go:371-442,577-582`, `templates/settings.html:192-193`: no UI authentication; GET settings embeds the saved IMAP password. Setup also reflects SMTP passwords. Loopback limits direct remote reachability. | Mandatory random-token Basic authentication, exact Host policy, no credential-editing/reflection routes. SMTP password is loaded from a mounted file only at send construction. |
| A3 | Medium | `internal/web/server.go:684-732,802-810,907-929,302-368`: web single/bulk/startup resume ignores YAML dry-run and recipient exclusions/regions. CLI dry-run did work. | One shared service policy, exact approved ID/email binding, exclusions and region filtering, default dry-run, independent environment gate, per-request preview. Removed bulk and resume. CLI dry-run never constructs a sender. |
| A4 | Medium | `internal/template/template.go:89-110` and all three templates: every populated DOB/address/phone/email is automatically disclosed to every recipient. | Per-broker field allowlist; no profile fields by default. Full exact-message preview. Sender email disclosure is unavoidable and stated explicitly. |
| A5 | Medium | `internal/browser/confirm.go:64-89,127-136`: initial domain approval is lost on redirects, allowing GET requests to other/private destinations. | Removed the confirmation HTTP client and command. No application HTTP fetch of broker/inbox URLs. |
| A6 | Medium | `internal/inbox/monitor.go:349-398`, `parser.go:283-341`, `cmd/eraser/main.go:1508-1561`: forged bounce subjects/body addresses can cause catalog deletion when `--remove` is used. | Removed mailbox-driven mutations. Catalog is immutable in the runtime image and changed only through reviewed Git updates. |
| A7 | Medium | `internal/web/templates/layout.html:11-27`, `server.go:461-469`: mutable external JavaScript executes in credential-bearing pages; external fonts also undermine privacy. | Self-contained HTML/CSS, no JavaScript, CDN, fonts, analytics or browser asset requests. CSP default-src none; form-action self; no unsafe-eval. The misleading upstream .css file containing JavaScript was removed. |
| A8 | Medium | `internal/inbox/monitor.go:130-173,234-264`: complete unbounded recent email bodies are buffered before broker filtering. | Removed inbox ingestion, MIME parser and mailbox credentials. Also eliminates untrusted sender-based status updates and hazardous mailbox-wide expunge behavior. |
| A9 | Medium | `internal/config/config.go:104-107,151-160`: saving preserves existing loose file modes; loading merely warns. New files/private directories were already protected. | Read-only strict config schema, separate secrets, rejection of world-readable/writable or group-writable files. Accept only private 0600/0640-style access; projected K8s 0440 Secrets supported. No config save/echo wizard. SQLite explicitly 0600; process umask 0077. |
| A10 | Medium | `internal/email/smtp.go:44-54`: no-auth SMTP permits plaintext fallback. Authenticated implicit TLS already verified certificates. | Verified TLS 1.2+ mandatory for every message, explicit implicit/STARTTLS mode, no downgrade path. Context-aware connect and deadline/cancellation on the connection. |

These resolutions include **feature removal**, not a claim that the legacy
browser/inbox implementation has been repaired. Bringing those features back
requires a new design/review: authenticated email provenance, bounded parsing,
per-broker origin/redirect and IP policies, field approval before filling,
isolated browser execution and reliable mailbox mutation semantics.

## Additional checks and corrections

- SMTP From/To and Subject reject CR/LF/header delimiters before dialing. Bare
  single email addresses are required. No SQL injection was found in upstream;
  the new history store continues parameterized statements. Go html/template
  escapes catalog and profile text; adversarial HTML fixtures verify output.
- CSRF: every state-changing HTTP route is POST, uses a random CSRF token,
  rejects foreign Origin/fetch-site requests and enforces the configured Host.
  Web delivery additionally consumes a single-use, five-minute preview approval
  bound to the exact message. No GET changes application state or sends mail.
- Startup never loads pending jobs. The first-boot missing-config path was
  corrected after independent review (`errors.Is` for wrapped ENOENT).
- SMTP acknowledgement semantics were corrected after independent review:
  accepted DATA remains success even if QUIT fails; lost acknowledgement is
  recorded as `uncertain`, not safely retryable failure. An attempt is recorded
  before transport. There is no automatic retry.
- SQLite holds only broker ID, result and timestamp for new deliveries. It does
  not store SMTP passwords, raw mail, profile snapshots, message bodies or
  confirmation tokens. Legacy database tables are not migrated/read; retire old
  volumes separately because prior PII may still exist. A single replica with
  Recreate strategy avoids multiple pod writers. SQLite is **not encrypted**.
- Broker IDs are checked for syntax/duplicates; recipients for bare address
  syntax; URLs for scheme/host. These checks do not prove recipient ownership.
  Approval binds ID to exact email, so a catalog email update cannot silently
  reuse approval. The runtime has no catalog updater or bounce cleaner.
- Build context is an explicit source allowlist. Runtime is scratch with a
  static Go binary and CA roots, UID/GID 65532, no shell/package manager/browser.
  Compose/Kubernetes use read-only root, no capabilities, no privilege escalation,
  resource limits and private persistence. K8s has no service-account token and
  denies egress/ingress by default. No scheduled workflow sends mail.

## Tool results and reproducibility

Results are point-in-time observations. See [scan-results](docs/scan-results/) and
GitHub Actions artifacts for the exact checked release. An empty staticcheck/vet
report is success, not a skipped run.

| Check | Observed result |
|---|---|
| `go test -race ./...` (Go 1.27.1) | Pass, including auth/Host/CSRF, preview/replay/change rejection, recipient/exclusion gates, minimization, missing/legacy-secret config, restart, dry-run, mandatory STARTTLS, local TLS success/certificate rejection/uncertain SMTP. |
| `go vet ./...` | Pass. |
| Staticcheck v0.8.1 | Pass. |
| Gosec v2.29.0 | No findings after review. Four precise G304 suppressions document trusted operator filesystem paths; no blanket rule exclusion. |
| Govulncheck v1.8.0, upstream | 10 reachable advisories in x/net/html and gorilla/csrf; 4 additional imported-package and 8 required-module advisory matches were not shown reachable. |
| Govulncheck, hardened tree | No vulnerabilities found. Vulnerable parser/CSRF dependency paths and unused API-provider SDKs removed; SQLite/Cobra/toolchain updated. |
| Gitleaks v8.30.1, upstream Git history | No leaks detected across 3 scanned commits. This does not prove no unknown credential format exists. |
| Gitleaks, hardened working files | No leaks detected, with redaction enabled. |
| Trivy v0.74.0, built image | No High/Critical vulnerability or secret findings in the built runtime image. Scans inspect the Go executable even though scratch has no OS packages. |
| Trivy config | Dockerfile and restrictive manifests pass; one Medium KSV-0125 registry-policy match: custom `git.nicholstech.org` is outside Trivy's generic trusted-registry list. This is the intended authoritative homelab registry, not an unexpected source. No blanket suppression added. |
| Actionlint | Workflow validation passes. |
| Kubernetes runtime | PVC, restricted pod startup, absent/present Secret, authentication, health and restart persistence pass in a disposable namespace. See [deployment evidence](docs/scan-results/deployment-validation.md). |

Upstream reachable advisory IDs reported by govulncheck:
`GO-2026-5030`, `GO-2026-5029`, `GO-2026-5028`, `GO-2026-5027`,
`GO-2026-5025`, `GO-2026-4441`, `GO-2026-4440`, `GO-2025-3884`,
`GO-2025-3595`, `GO-2024-3333`. Reachable symbols are evidence for upgrading or
removing dependencies; parser-specific XSS advisories do not automatically prove
an exploitable rendered-XSS route in this application.

The image workflow gates publication on tests, static analysis, dependency and
secret scans, a candidate-image vulnerability scan, and runtime/Compose smoke
checks. Only the scanned candidate is tagged/pushed; there is no second unscanned
release build. The builder and third-party Actions are pinned; scanners are
version-pinned, with current advisory databases. Weekly checks and upstream-watch
provide maintenance signals, not automatic security guarantees or automatic
upstream merges.

## Remaining risks and operating requirements

- **Medium — recipient trust/irreversible disclosure:** no automated test can
  establish that a broker owns its current mailbox or already holds your data.
  Verify destinations yourself. Sending even a minimal message exposes its From
  address, timing, SMTP metadata and any approved fields. TLS to your provider
  is not end-to-end encryption to the broker.
- **Medium — plaintext persistent state/backups and privileged administrators:**
  config, history and web token are accessible to the application identity and
  host/cluster admins. Use disk/backup encryption, restrictive RBAC and a private
  secret manager/SOPS workflow. Kubernetes Secrets alone are not encryption.
- **Medium if misconfigured — HTTP exposure:** Basic credentials require HTTPS
  beyond localhost/SSH tunneling. Exact public origin must match your proxy and
  Host. Do not log Authorization headers or expose raw HTTP through another
  route. This is a single-user app, not tenant isolation or fine-grained RBAC.
- **Low — authorized misuse/availability:** an authenticated operator can send
  multiple individually approved requests, and repeated CLI invocations have
  no shared global quota. The account/provider must impose sensible delivery
  limits. Random high-entropy authentication replaces guessable passwords, but
  there is no account lockout; use proxy rate limiting for externally reachable
  deployments. The supported operation model is private access, not a public
  service.
- **Low — retention and migration:** new history persists until you retire it;
  backups can retain old data indefinitely. Old inbox/profile tables are not
  scrubbed automatically. Use a fresh volume for migration and a documented
  retention policy. Secure deletion on copy-on-write/SSD storage is not assured.
- **Residual supply-chain risk:** scans cannot detect all unknown flaws or a
  malicious upstream change. Review every update and maintain pinned deployment
  digests. The workflow's package-only Gitea credential belongs to a dedicated non-admin
  eraser-builder account and is stored as an encrypted GitHub Actions secret;
  rotate/revoke it if CI is compromised. The current upstream
  revision contains no LICENSE file; licensing is unresolved.

No absolute claim that the program is vulnerability-free is made. No real
provider/broker delivery, production TLS proxy, DNS-specific egress policy,
backup restore or multi-architecture runtime was exercised against real data.
The published image targets linux/amd64. The removed automation cannot run in
this image. Keep delivery disabled until your own exact-recipient review is
complete.

The publishing job runs on a separate clean runner, downloads the checked
candidate artifact, verifies its checksum and rescans it before login/push.
Source tests and pull requests run with contents-read permission only and never
receive publishing secrets. The publishing runner never checks out or executes
repository source code.

## Discovery-first feature delta (2026-09-24)

`feature/discovery-first` adds explicit, one-broker-at-a-time Brave index searches
and requires reviewed evidence before delivery. It does not restore the removed
browser, form filling or inbox automation. This section supplements the baseline
review above; it is not a new independent repository-wide audit.

| Severity | Risk | Controls and remaining limits |
|---|---|---|
| Medium | Search terms disclose identifiers to a third-party index provider. | Disabled deployment gate; separately mounted API secret; exact query preview and explicit approval. Only selected name/city/state/email/phone; no DOB/address. One selected broker/domain, no automatic search or retry. Provider sees query/IP/account and may retain data; verify your plan. |
| Medium | False positives/stale snippets could cause new disclosure to a broker. | Results remain pending until human confirmation. Every actual send checks current confirmed evidence plus existing exact-recipient, field, preview and SMTP gates. Profile/target/query changes, rescan, rejection, deletion and 30-day expiry revoke eligibility. Search index coverage is incomplete; no-result is never proof of absence. Humans can still misidentify a result. |
| Medium | Discovery creates a new local store of sensitive URL/title/snippet evidence. | SQLite 0600, parameterized statements, secure_delete, bounded fields/results, no API keys or raw profile/query snapshots. Evidence expires after 30 days; cleanup at startup/next approved search and explicit deletion. Disk/backup encryption remains an operator responsibility. Fingerprints can be sensitive to offline guessing and are protected like PII. |
| Low | Untrusted search results, redirects or API errors could introduce SSRF/XSS/log leakage. | Fixed HTTPS API endpoint, verified TLS1.2+, no environment proxy, cookies, redirect following, result fetching or remote assets. Domain-filtered HTTPS candidates, escaped HTML/plain-text URLs, terminal-quoted evidence, generic errors. 15s client deadline, 1MiB response, 20 results, bounded strings and no retries. API/provider compromise can still fabricate misleading evidence. |

All discovery routes retain authentication/Host/CSRF enforcement. Search approvals
are single-use, five-minute and bound to the current plan. CLI execution requires
the exact query/profile digest. `discover --dry-run` never constructs a client;
mail `options.dry_run` still blocks every delivery but intentionally permits an
explicitly approved active search. Discovery has no SMTP dependency or call path.
A confirmed candidate never bypasses existing mail approval controls. No live
search API credentials, personal data or broker requests were used in development.

The baseline statement that new delivery rows hold only metadata still applies
to `delivery_history`; the new discovery tables explicitly contain sensitive
evidence. Old images do not enforce the new confirmed-match gate: rolling back
to main must keep sending disabled until that behavior is reviewed. Added tables
are additive; rollback does not erase their PII.

Validation adds local TLS search fixtures, domain/redirect filtering, bounded
responses, redacted errors, query minimization/injection checks, disabled-secret
gates, persistent confirmation/revocation/expiry, send denial without current
confirmed evidence, and authenticated CSRF-protected search approval/replay tests.
The same CI suite builds/scans feature candidates without publishing `stable`.
