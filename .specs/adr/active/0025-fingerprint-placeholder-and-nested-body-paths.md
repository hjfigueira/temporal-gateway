# ADR-025: `{fingerprint(...)}` placeholder and nested body paths

**Status:** Accepted. Extends [ADR-004](0004-origin-field-placeholders-for-workflow-id-templating.md) and [ADR-020](0020-unresolved-workflow-id-placeholders-are-rejected.md).

**Related features:** [`workflow-id-templating.feature`](../../features/workflow-id-templating.feature)

## Context

Workflow IDs are how Temporal deduplicates. A caller often wants "the same
request content → the same workflow" without a natural business key, or
wants to key on a nested value. Before this, templates could reach only
top-level body fields (`{body.orderId}`) and had no way to derive an ID
from content.

## Decision

- **Nested body paths:** `{body.customer.id}`, `{body.items[2].sku}`,
  `{body[0]}`. A key is followed with `.`, an array index with `[n]`.
  Path, query and header names are still taken literally (`{query.a.b}`
  is the query parameter named `a.b`). A body key that contains a dot is no
  longer reachable as `{body.a.b}`; that now means nested `a` → `b`.
- **`{fingerprint(ref)}`:** `ref` is any reference above, or the whole
  `body`. It renders the first 16 hex characters (64 bits) of the SHA-256
  of the referenced value's canonical JSON. `encoding/json` sorts object
  keys, and a decoded number is always a float64. So key order and `1` vs
  `1.0` don't change the fingerprint. A path, query or header value hashes
  as a JSON string.
- **A bare `{body}`** is rejected at spec load. A whole JSON document isn't
  a usable ID component; the error points to `{fingerprint(body)}`.
- **One parser** (`spec.ParsePlaceholder`) serves both startup validation
  and request-time rendering, so what loads is exactly what renders.
- Unresolved references, including fingerprints, follow ADR-020: **422**,
  and nothing is dispatched. A missing body, a missing key, an index out
  of range, or indexing into a non-array all count as unresolved.

## Consequences

- 64 bits makes collisions negligible for deduplication (about 1 in 10⁹ at
  190k distinct bodies per ID prefix), and keeps IDs short. The format is
  part of the contract: changing the length or algorithm changes every
  derived ID, so in-flight deduplication would break across a deploy.
- Integers above 2⁵³ lose precision when decoded as float64, so two such
  bodies differing only there fingerprint the same. Raise this if exact
  big-integer IDs matter.
- Every field in the body counts. Volatile fields (timestamps, request IDs)
  defeat deduplication; fingerprint a stable sub-tree such as
  `{fingerprint(body.order)}` instead.
