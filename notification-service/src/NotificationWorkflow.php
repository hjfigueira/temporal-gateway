<?php

declare(strict_types=1);

namespace App;

use Temporal\Workflow;

/**
 * NotificationWorkflow is the workflow http-worker.php starts for a
 * cascaded event - but only once BusinessRulesWorkflow::isEventRelevant
 * (queried in dispatch(), before this workflow is ever started) has
 * confirmed the event is worth acting on. Relevance is no longer this
 * workflow's own concern: an irrelevant event now results in no
 * NotificationWorkflow run at all, rather than one that starts only to
 * fail (see BusinessRulesWorkflow's docblock for why).
 *
 * A real implementation would send the actual notification here (email,
 * SMS, push, ...); this sample just acknowledges receipt, since the point
 * being demonstrated is where the relevance decision lives, not delivery.
 */
#[Workflow\WorkflowInterface]
class NotificationWorkflow
{
    #[Workflow\WorkflowMethod(name: 'NotificationWorkflow')]
    public function handle(array $order): array
    {
        return ['status' => 'SENT', 'order' => $order];
    }
}
