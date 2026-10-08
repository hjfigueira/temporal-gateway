<?php

declare(strict_types=1);

namespace App;

use Temporal\Workflow;

/**
 * BusinessRulesWorkflow is the long-running workflow http-worker.php queries,
 * synchronously, before it ever starts NotificationWorkflow: a single
 * standing instance (WORKFLOW_ID below) that answers isEventRelevant via a
 * Temporal Query - a read-only, replay-safe call that never touches
 * workflow state or history - so relevance can be decided without creating
 * any run for events that turn out not to matter. Unlike the earlier
 * design (validate() as NotificationWorkflow's own first step, which always
 * left a FAILED run behind for an irrelevant event, see git history), an
 * irrelevant event here never causes a workflow to exist at all.
 *
 * isEventRelevant() is a pure function of its input today, same rule
 * cmd/order-service/main.go's validateOrderPayload applies in Go, so this
 * workflow's own execution does nothing but keep itself alive - the point
 * being demonstrated is *where* a Query lets that decision live (a
 * long-running, externally-queryable workflow) rather than any real rule
 * complexity. A production version would grow real state here (fetched via
 * Activity, updated via Signal) that isEventRelevant reads.
 */
#[Workflow\WorkflowInterface]
class BusinessRulesWorkflow
{
    /**
     * Fixed, well-known workflow ID: there is exactly one standing instance
     * of this workflow per namespace, not one per event - http-worker.php
     * starts it lazily (see queryEventRelevance) and then always queries
     * this same ID.
     */
    public const WORKFLOW_ID = 'notification-business-rules';

    /**
     * How long a single run keeps answering queries before continuing as
     * new. Bounds this workflow's history growth the standard way for a
     * long-running/entity workflow that otherwise has no natural end -
     * 30 days is arbitrary for this sample; a production instance would
     * size it to however often real rule state actually changes.
     */
    private const RENEW_INTERVAL_SECONDS = 30 * 24 * 60 * 60;

    #[Workflow\WorkflowMethod(name: 'BusinessRulesWorkflow')]
    public function handle()
    {
        yield Workflow::awaitWithTimeout(self::RENEW_INTERVAL_SECONDS, fn(): bool => false);

        return yield Workflow::newContinueAsNewStub(self::class)->handle();
    }

    /**
     * isEventRelevant is the business rule NotificationWorkflow's owner
     * applies before any run is created for a cascaded event: an order with
     * no items has nothing to notify about. Query methods must never
     * mutate workflow state or block - this one just reads $order, so it
     * trivially satisfies that.
     */
    #[Workflow\QueryMethod(name: 'isEventRelevant')]
    public function isEventRelevant(array $order): ?string
    {
        if (empty($order['orderId'])) {
            return 'payload is missing orderId';
        }
        if (empty($order['items'])) {
            return 'order has no items - nothing to notify about';
        }
        return null;
    }
}
