# ADR-006: `TerminateIfRunning` reuse policy mapped to non-deprecated primitives

**Status:** Accepted

**Related features:** [`start-workflow-semantics.feature`](../../features/start-workflow-semantics.feature)

## Context

Temporal's SDK marks `WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING`
deprecated, recommending `AllowDuplicate` + `WorkflowIdConflictPolicy
TerminateExisting` instead — but that's a two-field combination, awkward to
expose as a single intuitive YAML value.

## Decision

`x-temporal.idReusePolicy: TerminateIfRunning` is accepted as a convenience
value that the dispatcher (`internal/temporal/policy.go`'s
`resolveReuseAndConflictPolicy`) translates into `AllowDuplicate` +
`TerminateExisting` under the hood. Because `TerminateIfRunning` already
implies a specific conflict policy, combining it with an explicit
`workflowIdConflictPolicy` in the same trigger is rejected at
spec-validation time (`internal/spec/validation.go`) rather than silently
resolved by precedence.

## Consequences

- The YAML surface stays close to how operators think ("terminate the
  running one and start fresh") without perpetuating a deprecated SDK enum
  value in new code.
- The combination is caught before the gateway starts serving traffic, not
  as a confusing runtime behavior difference.
