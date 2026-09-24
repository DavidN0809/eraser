# Deployment validation — 2026-09-24

Synthetic data only; application delivery disabled throughout.

- Source commit: `641b7ed` (initial hardening implementation).
- Local multi-stage build: Buildah, scratch runtime, static Go binary, UID/GID
  `65532:65532`, no shell or package manager.
- Exact locally tested registry image:
  `git.nicholstech.org/nichols-homelab/eraser@sha256:44cf8f1c7692e97e0a946b4601ceb0d3c031ffbbe186135c01931108ac0fae26`.
  This is a validation artifact; production images are published by Actions.
- Trivy remote-image scan: 0 High/Critical vulnerability or secret findings.
- Empty-config native startup: health 200, anonymous UI 401, authenticated UI
  200, generated token mode 0600. No deliveries.
- Portable `k8s/` manifests applied unchanged except for temporary namespace
  and validation image tag. Default `ha-storage` PVC bound at 1 GiB RWO; one
  replica became Ready.
- Actual pod context: UID/GID/fsGroup 65532, non-root, RuntimeDefault seccomp,
  read-only root filesystem, privilege escalation false, capabilities ALL
  dropped, no service-account token, default-deny NetworkPolicy.
- Through kubectl port-forward: anonymous UI 401 and authenticated UI 200 in
  preview mode.
- Created synthetic `eraser-secrets` from the example configuration and empty
  SMTP file; restarted. Mounted 0440 config loaded, pod became Ready, health
  passed, web token hash matched before/after restart, and delivery history
  remained empty.
- The temporary namespace/PVC is removed after validation. No durable homelab
  deployment, public ingress, real mailbox credentials, or broker traffic was
  introduced.

The initial complete Actions run passed all checks, including restricted Docker
runtime and Docker Compose smoke tests, image scan, SBOM generation and publication:
https://github.com/DavidN0809/eraser/actions/runs/36023767619

See subsequent Actions runs for the exact release being deployed.
