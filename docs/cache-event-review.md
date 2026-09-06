# Cache event review

Reference: [How the controller-runtime Cache Actually Works](https://kubernetes.io/blog/2026/07/29/controller-runtime-cache-explained/),
including the August 6 corrections. Reviewed against controller-runtime v0.25.0.

## Decisions

- Keep the existing namespace/label scope, per-resource projections, and
  `ReaderFailOnMissingInformer`. These already bound the cache and prevent
  accidental informers for unrelated types.
- Filter updates using the projected routing fields. Node heartbeats, image lists,
  resource versions, and unrelated metadata previously caused full route planning,
  three netlink reconciliation passes, and worker probes. Filtering reduces that
  work without changing what the informer receives or stores.
- Keep predicates beside the projections so their field contract is explicit.
  Predicates only read shared informer objects. Create and delete events still
  enqueue work; Node/Pod readiness, deletion, addresses, Pod binding, PodCIDRs,
  and Cilium health address updates remain actionable.
- Keep `RequeueAfter` for probe checks and kernel route drift. Equal-object resync
  updates are filtered, so informer resync is deliberately not the safety net.
  Probe thresholds remain counts of probe results, not wall-clock guarantees.
- Narrow the reconciler dependency to `client.Reader`. Projected API objects are
  inputs to planning and must not become incomplete write payloads.
- Keep default deep copies. The current projections already limit their cost;
  disabling copies introduces ownership constraints without measured benefit.
- Do not add per-node indexes: this controller computes a global route plan and
  needs every worker and matching Cilium Pod once per reconciliation. Per-worker
  lookups would add indexes and repeated queries without shrinking the input.
- Keep startup discovery on a direct client and runtime reads on the cache.
  ConfigMaps are read only during discovery and do not need permanent informers.
  Metadata-only watches cannot supply the status/spec fields required by routing.

## Validation

Local checks: `make verify` (race tests, vet, Linux lint) and Linux amd64/arm64
cross-builds passed. Regression tests exercise the projections and predicate
together, including irrelevant updates, relevant routing changes, resync, lifecycle
events, and shared-object immutability.

On the dedicated test cluster, a candidate container replaced one control-plane
Static Pod temporarily. The other two control planes retained their original
controllers. Both workers were healthy before fault injection.

| Check | Observed result |
| --- | --- |
| Original controller: 20 Node annotation updates over 10 seconds | 22 reconciliations |
| Candidate: same 20 updates over 10 seconds | 1 reconciliation |
| CiliumNode health address invalidation and restoration | Each converged in less than 1 second at second-resolution timing |
| Block one health endpoint on the candidate host | Service next hop withdrawn after 31 seconds; both workers remained API-ready |
| Remove the health endpoint block | Both healthy Service next hops restored |
| Delete one owned PodCIDR route without an API change | Periodic reconciliation restored it within the 20-second test deadline |
| Privileged netlink lifecycle test | Passed, including ECMP, prune, and foreign-protocol conflict |
| PodIP / ClusterIP / Kubernetes Service | HTTP 307 / 307 / 200 |
| API Server logs / exec | Passed |
| API Server port-forward | HTTP 307 |
| Restore original Static Pod | Original manifest matched byte-for-byte; original controller became ready |
| Final three-control-plane check | Each ready with three owned routes and two healthy workers; Cilium 2/2 |

Injected health changes, firewall rules, and test annotations were removed. The
candidate image was exercised on one control plane; the final consistency checks
covered all three. No large-cluster benchmark was performed.

The annotation experiment demonstrates about 95% fewer reconciliations for that
specific event workload. It does not establish a general CPU/memory percentage or
a large-cluster throughput claim. Watch traffic, cache population, and global
planning complexity are unchanged. The test deployment is temporary, not a release.

## GitHub Actions image deployment

The subsequent validation on September 7 used the image published by
[GitHub Actions run 34048497746](https://github.com/zijiren233/route-controller/actions/runs/34048497746)
for commit `ae900f918602c1f99a10f8c12d2d7987fbd4a171`. Both architecture builds
and the final image publication succeeded.

Image: `ghcr.io/zijiren233/route-controller:sha-ae900f9`

Pinned OCI index digest:
`sha256:bd6ba8bb4bf422328eef6975af078a7146fc4f0d225b685606c30e2ac3c877d1`.

All three test control planes pulled that digest and were updated sequentially.
The running container on each reported the exact commit above and `linux/amd64`.
Every node passed the complete test sequence:

| Check | Control plane 1 | Control plane 2 | Control plane 3 |
| --- | --- | --- | --- |
| Reconciliations for 20 annotation updates | 1 in 9 seconds | 1 in 9 seconds | 1 in 10 seconds |
| Watched field withdrawal / restoration | <1s / <1s | 1s / <1s | <1s / <1s |
| Probe failure withdrawal | 31s | 30s | 31s |
| Probe recovery and periodic route repair | Passed | Passed | Passed |
| Privileged netlink lifecycle | Passed | Passed | Passed |
| PodIP / ClusterIP / API Service | 307 / 307 / 200 | 307 / 307 / 200 | 307 / 307 / 200 |
| Logs / exec / port-forward | Passed | Passed | Passed |

The final state retains the Actions image on all three nodes, each ready with
three owned routes and two healthy workers. Cilium is 2/2. Injected health changes,
test annotations, and firewall rules were removed. Original manifests were backed
up before each update for rollback. This supersedes the temporary local-image
deployment described above; arm64 was built by Actions but not deployed here.
