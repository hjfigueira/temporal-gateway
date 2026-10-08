<?php

declare(strict_types=1);

require __DIR__ . '/vendor/autoload.php';

use App\BusinessRulesWorkflow;
use App\NotificationWorkflow;
use Nyholm\Psr7\Factory\Psr17Factory;
use Nyholm\Psr7\Response;
use Psr\Http\Message\ServerRequestInterface;
use Spiral\RoadRunner\Http\PSR7Worker;
use Spiral\RoadRunner\Worker;
use Temporal\Client\ClientOptions;
use Temporal\Client\GRPC\ServiceClient;
use Temporal\Client\WorkflowClient;
use Temporal\Client\WorkflowOptions;
use Temporal\Exception\Client\WorkflowExecutionAlreadyStartedException;

// This is notification-service's hand-rolled implementation of the Nexus
// HTTP protocol's synchronous-operation path (POST /{service}/{operation},
// 200 + JSON result on success). It exists at all only because the PHP SDK
// has no equivalent of go.temporal.io/sdk/worker's RegisterNexusService -
// see github.com/temporalio/sdk-php issue #580 - so there's no worker
// abstraction to plug into here; the wire format below was verified by hand
// against a real Temporal server (see README.md), not just read off a spec.
//
// Relevance is decided before anything is started: dispatch() queries the
// standing BusinessRulesWorkflow instance (src/BusinessRulesWorkflow.php)
// via isEventRelevant, genuine Temporal PHP SDK Query code, the same
// mechanism any other PHP SDK user would use to read a workflow's state
// from outside it. Only once that query says the event is relevant does
// dispatch() start NotificationWorkflow at all - an irrelevant event is
// rejected right here, so no NotificationWorkflow run is ever created for
// it, same guarantee cmd/order-service's Go validateOrderPayload gives.
// This file's remaining job is translating that outcome back into the
// Nexus contract CascadeEvent (internal/temporal/cascade.go) expects: 200
// on success, 400 + metadata.type "nexus.HandlerError" on a non-retryable
// rejection.
const SERVICE_NAME = 'TemporalGatewayDispatch';
const OPERATION_NAME = 'Dispatch';

$worker = Worker::create();
$psr17 = new Psr17Factory();
$psr7 = new PSR7Worker($worker, $psr17, $psr17, $psr17);

$namespace = getenv('TEMPORAL_NOTIFICATIONS_NAMESPACE') ?: 'notifications';

// ClientOptions defaults to the "default" namespace - without this, every
// workflow this handler starts would silently land in the wrong namespace
// (found by hand: NotificationWorkflow runs showed up under "default"
// instead of "notifications" until this was added).
$client = WorkflowClient::create(
    ServiceClient::create(getenv('TEMPORAL_HOST') ?: 'localhost:7233'),
    (new ClientOptions())->withNamespace($namespace),
);
$taskQueue = getenv('NOTIFICATIONS_TASK_QUEUE') ?: 'notifications-task-queue';

while (true) {
    try {
        $request = $psr7->waitRequest();
        if ($request === null) {
            break;
        }
    } catch (\Throwable $e) {
        $psr7->respond(new Response(400));
        continue;
    }

    try {
        $psr7->respond(dispatch($request, $client, $taskQueue));
    } catch (\Throwable $e) {
        // Unclassified failure: no "metadata.type: nexus.HandlerError"
        // body, so this is treated as HandlerErrorTypeInternal by whoever
        // called us - retryable, same default Go's dispatch_service.go
        // gets from returning a plain error.
        $psr7->respond(handlerError(500, $e->getMessage()));
        $worker->error((string) $e);
    }
}

/**
 * dispatch handles one Nexus StartOperation request: validates the request
 * shape (protocol-level correctness only), queries BusinessRulesWorkflow
 * for business-rule relevance (see the file header) and rejects here if
 * it isn't, or else starts NotificationWorkflow and waits for its result -
 * so this synchronous Nexus operation can report the real outcome either
 * way.
 */
function dispatch(ServerRequestInterface $request, WorkflowClient $client, string $taskQueue): Response
{
    $expectedPath = '/' . SERVICE_NAME . '/' . OPERATION_NAME;
    if ($request->getMethod() !== 'POST' || $request->getUri()->getPath() !== $expectedPath) {
        return handlerError(404, "unknown Nexus operation, expected POST {$expectedPath}");
    }

    $input = json_decode((string) $request->getBody(), true);
    if (!\is_array($input) || !isset($input['binding']) || !\is_array($input['binding'])) {
        return handlerError(400, 'malformed Dispatch input: missing binding');
    }

    $workflowId = $input['binding']['WorkflowID'] ?? null;
    if (!\is_string($workflowId) || $workflowId === '') {
        return handlerError(400, 'malformed Dispatch input: missing binding.WorkflowID');
    }

    $order = $input['body'] ?? null;

    if ($reason = queryEventRelevance($client, $taskQueue, is_array($order) ? $order : [])) {
        // Reject before NotificationWorkflow is ever started: this
        // cascaded event isn't relevant, so there's nothing for a run to
        // do. metadata.type "nexus.HandlerError" + details.type
        // "BAD_REQUEST" is what classifies this as non-retryable - a plain
        // error body (or any 5xx) would be retried indefinitely, and
        // eventually trip the endpoint's circuit breaker, instead of
        // failing once, immediately, the way a validation rejection
        // should - same reasoning as order-service's Go
        // HandlerErrorTypeBadRequest.
        return handlerError(400, "notification-service: rejected cascaded event: {$reason}");
    }

    $options = WorkflowOptions::new()
        ->withWorkflowId($workflowId)
        ->withTaskQueue($taskQueue);

    $stub = $client->newWorkflowStub(NotificationWorkflow::class, $options);
    $run = $client->start($stub, $order);

    // Blocks until NotificationWorkflow completes - it does no real work
    // beyond acknowledging receipt, so this resolves almost immediately,
    // well within the Nexus operation's own timeout (Request-Timeout,
    // ~10s as observed against the real server). A genuine failure here
    // (unlike an irrelevant event, already rejected above) is a real
    // problem, not a validation outcome, so it's left to the catch-all in
    // the request loop below - which reports it as retryable (500), not
    // as this endpoint's non-retryable BAD_REQUEST.
    $result = $run->getResult();

    $body = json_encode([
        'result' => [
            'workflowId' => $run->getExecution()->getID(),
            'runId' => $run->getExecution()->getRunID(),
            'notification' => $result,
        ],
    ]);

    return new Response(200, ['Content-Type' => 'application/json'], $body);
}

/**
 * queryEventRelevance ensures the standing BusinessRulesWorkflow instance
 * is running (starting it on the very first call this process ever makes;
 * every call after that, and every call from every other process, finds it
 * already started and just moves on) and returns whatever its
 * isEventRelevant query says: null if $order is relevant, a reason string
 * if not. dispatch() calls this before starting NotificationWorkflow so an
 * irrelevant event never causes a run to be created at all.
 */
function queryEventRelevance(WorkflowClient $client, string $taskQueue, array $order): ?string
{
    $options = WorkflowOptions::new()
        ->withWorkflowId(BusinessRulesWorkflow::WORKFLOW_ID)
        ->withTaskQueue($taskQueue);

    try {
        $client->start($client->newWorkflowStub(BusinessRulesWorkflow::class, $options));
    } catch (WorkflowExecutionAlreadyStartedException) {
        // Expected on every call after the first: exactly one standing
        // instance of this workflow is meant to exist per namespace (see
        // BusinessRulesWorkflow::WORKFLOW_ID), so finding one already
        // running is success, not an error.
    }

    return $client
        ->newRunningWorkflowStub(BusinessRulesWorkflow::class, BusinessRulesWorkflow::WORKFLOW_ID)
        ->isEventRelevant($order);
}

function handlerError(int $status, string $message): Response
{
    $body = json_encode([
        'message' => $message,
        'metadata' => ['type' => 'nexus.HandlerError'],
        'details' => ['type' => $status === 400 ? 'BAD_REQUEST' : 'INTERNAL'],
    ]);
    return new Response($status, ['Content-Type' => 'application/json'], $body);
}
