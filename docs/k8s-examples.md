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

## Optional active discovery with Cilium

The separate example below allows only DNS to kube-dns and HTTPS to the fixed
Brave API host. It requires Cilium FQDN policy support; confirm resolver labels
and DNS visibility for your cluster. Apply it only when enabling discovery.
It grants no broker website or SMTP access. Keep the standard default-deny
policy. For another CNI, use its supported domain policy or maintain reviewed
provider IP rules; do not replace this with unrestricted TCP/443 egress.

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: eraser-discovery
  namespace: eraser
spec:
  endpointSelector:
    matchLabels: {app: eraser}
  egress:
    - toEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: kube-system
            k8s:k8s-app: kube-dns
      toPorts:
        - ports:
            - {port: "53", protocol: ANY}
          rules:
            dns:
              - matchName: api.search.brave.com
    - toFQDNs:
        - matchName: api.search.brave.com
      toPorts:
        - ports:
            - {port: "443", protocol: TCP}
```

Retain the Secret and this policy through encrypted Flux configuration for a
permanent Nichols deployment. No cluster changes are needed to review the branch.
