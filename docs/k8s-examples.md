# Explicit live-network policy example

Keep the default deny policy. Add a separate policy only after selecting a relay.
This example is deliberately **not** in `k8s/` and uses a non-routable documentation
address. Replace it with the narrow relay IP and actual resolver labels before
applying. It grants no web access and does not itself enable sending.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: eraser-smtp
  namespace: eraser
spec:
  podSelector:
    matchLabels: {app: eraser}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
          podSelector:
            matchLabels: {k8s-app: kube-dns}
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    - to:
        - ipBlock: {cidr: 192.0.2.10/32}
      ports:
        - {protocol: TCP, port: 465}
```

Some clusters use NodeLocal DNS or Cilium DNS policies; adapt to the actual
resolver. Standard NetworkPolicy has no domain allowlist, so provider IP changes
need reviewed updates. Prefer a dedicated stable relay. NetworkPolicy cannot
limit SMTP message recipients; application approval checks do that.
