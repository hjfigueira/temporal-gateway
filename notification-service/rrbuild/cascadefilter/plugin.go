// Package cascadefilter is a RoadRunner plugin that answers
// internal/temporal.CascadeFilterActivityName ("IsEventRelevant") -
// CascadeEvent's per-trigger relevance check (see the Go gateway's
// internal/temporal/cascade.go) - by forwarding each task into its own PHP
// worker pool, which runs src/CascadeFilterActivity.php's actual business
// rule (see cascade-filter-worker.php). This plugin owns none of the
// business logic itself: it only speaks the Temporal Go SDK (dialing the
// "cascade" namespace, polling the task queue, decoding the activity
// input) and RR's own worker-pool protocol (encoding that input as a
// payload, handing it to a PHP process, decoding the result) - every
// actual relevance decision is made by the PHP on the other end of the
// pool, same as NotificationWorkflow/BusinessRulesWorkflow's business
// logic lives in PHP behind the stock "temporal" plugin.
//
// Why a second plugin (and a second pool) instead of reusing the stock
// "temporal" plugin's own PHP pool: RoadRunner's own "temporal" plugin
// (temporalio/roadrunner-temporal) dials exactly one
// go.temporal.io/sdk/client.Client, for exactly one namespace, for the
// whole process - see its Config.Namespace/Config.Address. CascadeEvent
// runs in the "cascade" namespace, notification-service's own workflows run
// in "notifications"; a Temporal Activity always executes in its calling
// workflow's namespace, so serving both from ONE stock plugin instance was
// never possible without forking it. Rather than fork a Temporal-maintained
// module, this plugin dials its own second client/worker entirely in Go,
// and its own small PHP pool to go with it (see Config.Pool) - registered
// alongside the stock plugins in this module's own custom RoadRunner build
// (see ../main.go and notification-service/.rr.yaml's cascade_filter
// section), so the whole thing still ships as the one binary/one process
// notification-service runs, same as before this existed.
package cascadefilter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/roadrunner-server/pool/payload"
	"github.com/roadrunner-server/pool/pool"
	"github.com/roadrunner-server/pool/pool/static_pool"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
)

// Name is this plugin's config key (.rr.yaml's "cascade_filter" section)
// and RR-internal identity.
const Name = "cascade_filter"

// cascadeFilterActivityName must match internal/temporal.
// CascadeFilterActivityName exactly - CascadeEvent calls the activity by
// this name, and has no idea (or need to know) whether the stock "temporal"
// plugin or this one answers it.
const cascadeFilterActivityName = "IsEventRelevant"

// Configurer is the subset of RR's core config plugin this Plugin needs -
// declared locally (rather than importing the config plugin's package
// directly) so this plugin only depends on the shape it actually uses; any
// concrete config plugin RR's endure container wires in that satisfies it
// works, structurally. Mirrors the same pattern temporalio/roadrunner-
// temporal's own Plugin.Init uses for its api.Configurer.
type Configurer interface {
	UnmarshalKey(name string, out any) error
	Has(name string) bool
}

// Logger is the subset of RR's core logger plugin this Plugin needs - same
// reasoning as Configurer above.
type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// Server is the subset of RR's core "server" plugin this Plugin needs to
// spawn its own PHP pool - the exact same NewPool method
// temporalio/roadrunner-temporal's own Plugin.initPool and the stock "http"
// plugin both call. Declared locally for the same structural-typing reason
// as Configurer/Logger above.
type Server interface {
	NewPool(ctx context.Context, cfg *pool.Config, env map[string]string, log *zap.Logger) (*static_pool.Pool, error)
}

// Plugin is a standalone RoadRunner plugin: its own Temporal client,
// worker.Worker, and PHP pool, entirely independent of the stock "temporal"
// plugin also registered in this build (see ../main.go) - the two share
// nothing but the OS process and the Temporal server they both happen to
// talk to.
type Plugin struct {
	mu     sync.Mutex
	config *Config
	log    *zap.Logger
	server Server

	client client.Client
	worker worker.Worker
	pool   *static_pool.Pool
}

// Init reads the "cascade_filter" config section. Returning nil here (no
// errors.Disabled sentinel) when the section is absent would be wrong for
// most plugins, but this one defaults cleanly (see Config.InitDefaults) and
// notification-service always wants it running, so an absent section just
// means "use the defaults" rather than "skip this plugin" - unlike, say,
// the stock "temporal" plugin, which has no sensible default namespace for
// an arbitrary deployment and so treats a missing section as disabled.
func (p *Plugin) Init(cfg Configurer, log Logger, server Server) error {
	p.config = &Config{}
	if cfg.Has(Name) {
		if err := cfg.UnmarshalKey(Name, p.config); err != nil {
			return fmt.Errorf("%s: %w", Name, err)
		}
	}
	p.config.InitDefaults()

	p.log = log.NamedLogger(Name)
	p.server = server

	return nil
}

// Serve spawns the PHP pool, dials the Temporal client, and starts the
// worker - all on its own goroutine ("async thread") rather than inline,
// so it never blocks endure's startup sequence for every other plugin in
// the container - the same non-blocking contract every RR plugin's Serve()
// must follow (RR calls Serve() on all plugins and only proceeds once each
// has *returned* its channel, not once each has finished starting). Any
// failure along the way is sent on errCh, which RR treats as a fatal
// startup error for the whole container - the same severity a stock
// plugin's Serve failing would have.
func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	go func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		// Spawned first: the Temporal worker started below must never poll
		// a task queue it can't yet answer - cascade-filter-worker.php
		// (php pool) is what isEventRelevant actually forwards every task
		// to.
		phpPool, err := p.server.NewPool(context.Background(), p.config.Pool, map[string]string{"RR_MODE": Name}, p.log)
		if err != nil {
			errCh <- fmt.Errorf("%s: spawn php pool: %w", Name, err)
			return
		}
		p.pool = phpPool

		c, err := client.Dial(client.Options{
			HostPort:  p.config.Address,
			Namespace: p.config.Namespace,
		})
		if err != nil {
			errCh <- fmt.Errorf("%s: dial temporal at %q: %w", Name, p.config.Address, err)
			return
		}
		p.client = c

		w := worker.New(c, p.config.TaskQueue, worker.Options{
			MaxConcurrentActivityExecutionSize: int(p.config.Pool.NumWorkers),
		})
		w.RegisterActivityWithOptions(p.isEventRelevant, activity.RegisterOptions{
			Name: cascadeFilterActivityName,
		})

		if err := w.Start(); err != nil {
			c.Close()
			errCh <- fmt.Errorf("%s: start worker: %w", Name, err)
			return
		}
		p.worker = w

		p.log.Info("started",
			zap.String("address", p.config.Address),
			zap.String("namespace", p.config.Namespace),
			zap.String("task_queue", p.config.TaskQueue),
			zap.Uint64("num_workers", p.config.Pool.NumWorkers),
		)
	}()

	return errCh
}

// Stop tears down the worker, client, and PHP pool - guarded by the same
// mutex Serve's goroutine holds while starting up, so Stop can't run
// concurrently with a still-in-progress Serve (e.g. a slow/failing
// Temporal dial) and tear down something that hasn't been assigned yet.
func (p *Plugin) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.worker != nil {
		p.worker.Stop()
	}
	if p.client != nil {
		p.client.Close()
	}
	if p.pool != nil {
		p.pool.Destroy(ctx)
	}
	return nil
}

// Name identifies this plugin to RR's endure container and logs.
func (p *Plugin) Name() string {
	return Name
}

// cascadeFilterInput mirrors internal/temporal.CascadeFilterInput - kept as
// a separate, hand-written declaration rather than importing that package
// (this is a different Go module - see go.mod) because the two only ever
// need to agree on JSON shape, and this struct's JSON encoding is also
// exactly what cascade-filter-worker.php/CascadeFilterActivity.php decode
// on the PHP side of this plugin's own pool - so it has two independent
// readers to stay compatible with, same as every other cross-language
// wire contract in this repo. Keep in sync with internal/temporal/
// cascade.go by hand.
type cascadeFilterInput struct {
	Namespace string `json:"namespace"`
	Body      any    `json:"body,omitempty"`
}

// cascadeFilterOutput mirrors internal/temporal.CascadeFilterOutput and
// cascade-filter-worker.php's response - see cascadeFilterInput's doc
// comment.
type cascadeFilterOutput struct {
	Reason string `json:"reason,omitempty"`
}

// isEventRelevant is the activity function the Temporal Go SDK invokes -
// it holds no business logic of its own: it encodes input as JSON, hands
// it to the PHP pool (cascade-filter-worker.php, which runs
// CascadeFilterActivity::isEventRelevant), and decodes whatever PHP
// answers. See internal/temporal.CascadeEvent for how a rejection here (a
// non-empty Reason) stops that trigger's Nexus dispatch from ever being
// attempted.
func (p *Plugin) isEventRelevant(ctx context.Context, input cascadeFilterInput) (cascadeFilterOutput, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return cascadeFilterOutput{}, fmt.Errorf("%s: encode activity input: %w", Name, err)
	}

	respCh, err := p.pool.Exec(ctx, &payload.Payload{Body: body}, make(chan struct{}))
	if err != nil {
		return cascadeFilterOutput{}, fmt.Errorf("%s: exec php pool: %w", Name, err)
	}

	resp := <-respCh
	if resp.Error() != nil {
		return cascadeFilterOutput{}, fmt.Errorf("%s: php pool: %w", Name, resp.Error())
	}

	var output cascadeFilterOutput
	if err := json.Unmarshal(resp.Body(), &output); err != nil {
		return cascadeFilterOutput{}, fmt.Errorf("%s: decode php pool response: %w", Name, err)
	}

	return output, nil
}
