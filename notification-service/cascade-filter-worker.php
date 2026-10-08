<?php

declare(strict_types=1);

require __DIR__ . '/vendor/autoload.php';

use App\CascadeFilterActivity;
use Spiral\RoadRunner\Payload;
use Spiral\RoadRunner\Worker;

// Entry point for cascade_filter's own PHP pool (see .rr.yaml's
// cascade_filter.pool section, and notification-service/rrbuild/
// cascadefilter/plugin.go, the Go plugin that spawns and calls this pool).
// Unlike worker.php/http-worker.php, this is a plain RoadRunner worker
// (Spiral\RoadRunner\Worker) - no Temporal PHP SDK involved on this side at
// all: the calling Go plugin already speaks the Temporal Go SDK itself for
// the "cascade" namespace, and just needs *some* PHP process to hand each
// activity task's input to and get CascadeFilterActivity's verdict back
// from, over the same raw request/response payload protocol every RR pool
// worker speaks.
$worker = Worker::create();
$activity = new CascadeFilterActivity();

while ($ctx = $worker->waitPayload()) {
    try {
        $input = json_decode($ctx->body, true, flags: JSON_THROW_ON_ERROR);
        $output = $activity->isEventRelevant(is_array($input) ? $input : []);
        $worker->respond(new Payload(json_encode($output, JSON_THROW_ON_ERROR)));
    } catch (\Throwable $e) {
        $worker->error((string) $e);
    }
}
