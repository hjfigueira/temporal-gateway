# ADR-033: Typed dispatch outcomes, action tables, and action-neutral batch statuses

**Status:** Accepted. Revises the `statusGetter` consequence of [ADR-010](0010-response-envelope-separates-gateway-status-from-http-status.md) and extends the batch statuses of [ADR-002](0002-triggers-list-and-returnstrategy-for-multi-workflow-fan-out.md).

**Related features:** [`multi-trigger-dispatch.feature`](../../features/multi-trigger-dispatch.feature), [`other-temporal-actions.feature`](../../features/other-temporal-actions.feature)

## Context

- `gateway.Dispatcher.Dispatch` returned `any`. The gateway decided
  success by checking whether the value happened to have a `GetStatus`
  method, so any other result counted as success without being checked.
- Adding a Temporal action meant editing four switches or maps: the
  action constants, spec validation, the temporal dispatch switch, and the
  gateway's `statusByAction`.
- A batch's top-level status always said `WORKFLOW_STARTED` /
  "N of M workflow(s) started", even when every trigger was a signal.

## Decision

- `Dispatch` returns `response.Outcome{Body, Status}`. `response.Ack`
  builds one for an acknowledgement (start/signal/cancel/terminate) and
  carries its envelope status. `response.Raw` builds one for a
  query/getResult result, with no Status.
- The 2xx code comes from the outcome: a raw result gets 200 and an
  acknowledgement gets 202. That's the mapping `statusByAction` used to
  hard-code, so the map is gone.
- Each action is one table entry in `spec` (`actionRules`, its required
  fields) and one in `temporal` (`actions`, its implementation).
  `TestEverySpecActionIsImplemented` fails if the two tables disagree.
- A batch keeps the `WORKFLOW_*` statuses when every trigger is a
  `startWorkflow`, so existing clients see no change. Any other batch uses
  `BATCH_SUCCEEDED` / `BATCH_PARTIALLY_SUCCEEDED` / `BATCH_FAILED` and the
  message "N of M trigger(s) succeeded". The HTTP codes and
  `returnStrategy` rules are unchanged.

## Consequences

- A client that handled a signal/cancel batch by matching `WORKFLOW_*`
  must also accept `BATCH_*`.
- A new action whose result isn't an acknowledgement has to return
  `response.Raw`, which gets it 200.
