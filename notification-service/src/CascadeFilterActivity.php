<?php

declare(strict_types=1);

namespace App;

/**
 * CascadeFilterActivity holds the actual business rule behind
 * internal/temporal.CascadeFilterActivityName ("IsEventRelevant") -
 * CascadeEvent's per-trigger relevance check (see the Go gateway's
 * internal/temporal/cascade.go). It is plain PHP, not Temporal PHP SDK
 * workflow/activity code: cascade-filter-worker.php calls it directly, as
 * the request handler behind a small RoadRunner worker pool that the Go
 * "cascade_filter" plugin (notification-service/rrbuild/cascadefilter)
 * spawns and forwards every activity task into. That plugin owns the
 * Temporal SDK side (dialing the "cascade" namespace, polling the task
 * queue) entirely in Go - the business rule itself stays in PHP, same as
 * NotificationWorkflow/BusinessRulesWorkflow's real logic does behind the
 * stock "temporal" plugin.
 *
 * The same "not every cascaded event is relevant" rule this repo
 * demonstrates three ways: cmd/order-service/main.go's validateOrderPayload
 * (inline Go, decided before ExecuteWorkflow) is the sibling for the
 * "default" namespace's own Dispatch operation; this is the one CascadeEvent
 * itself consults, once per trigger, before attempting that trigger's Nexus
 * dispatch at all.
 */
class CascadeFilterActivity
{
    /**
     * isEventRelevant mirrors internal/temporal.CascadeFilterInput/
     * CascadeFilterOutput's JSON shape by hand (cascade-filter-worker.php
     * decodes the request payload into $input and encodes this return
     * value straight back as the response body - no Temporal SDK
     * marshalling involved on this side at all): $input['namespace'] is
     * this trigger's target (spec.CascadeTrigger.Namespace), passed
     * precisely so the same call can decide differently per trigger even
     * though every trigger for one event shares the same $input['body'] -
     * "notifications" additionally only cares about multi-item orders,
     * while "default" (order-service, which has to fulfill the order
     * regardless of how small it is) doesn't apply that rule at all. A
     * single-item order is a real example of one trigger firing and the
     * other not: CascadeEvent still starts OrderWorkflow, but
     * NotificationWorkflow never runs.
     */
    public function isEventRelevant(array $input): array
    {
        $namespace = $input['namespace'] ?? '';
        $order = $input['body'] ?? null;
        $order = \is_array($order) ? $order : [];

        if (empty($order['orderId'])) {
            return ['reason' => 'payload is missing orderId'];
        }
        if (empty($order['items'])) {
            return ['reason' => 'order has no items - nothing to fulfill'];
        }
        if ($namespace === 'notifications' && \count($order['items']) < 2) {
            return ['reason' => 'order has only one item - not worth notifying about'];
        }
        return ['reason' => ''];
    }
}
