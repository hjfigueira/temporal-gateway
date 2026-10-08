<?php

declare(strict_types=1);

require __DIR__ . '/vendor/autoload.php';

use App\BusinessRulesWorkflow;
use App\NotificationWorkflow;
use Temporal\WorkerFactory;

$factory = WorkerFactory::create();

$worker = $factory->newWorker(getenv('NOTIFICATIONS_TASK_QUEUE') ?: 'notifications-task-queue');
$worker->registerWorkflowTypes(NotificationWorkflow::class, BusinessRulesWorkflow::class);

$factory->run();
