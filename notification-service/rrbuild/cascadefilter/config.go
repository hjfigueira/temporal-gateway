package cascadefilter

import "github.com/roadrunner-server/pool/pool"

// Config is the "cascade_filter" section of .rr.yaml - see plugin.go for
// why this plugin exists. Address/Namespace/TaskQueue mirror the stock
// "temporal" plugin's own address/namespace/taskQueue fields; Pool is the
// same *pool.Config shape server.Plugin.NewPool expects everywhere else in
// this repo (worker.php's implicit pool, http-worker.php's http.pool) -
// so cascade_filter.pool.num_workers means exactly what num_workers means
// there too: the PHP OS-process count, not a Go-side concurrency knob.
type Config struct {
	Address   string       `mapstructure:"address"`
	Namespace string       `mapstructure:"namespace"`
	TaskQueue string       `mapstructure:"task_queue"`
	Pool      *pool.Config `mapstructure:"pool"`
}

const (
	defaultAddress   = "127.0.0.1:7233"
	defaultNamespace = "cascade"
	defaultTaskQueue = "cascade-filter-task-queue"
)

var defaultCommand = []string{"php", "cascade-filter-worker.php"}

// InitDefaults fills in anything .rr.yaml left unset, same contract every
// other RR plugin's Config.InitDefaults follows (see e.g. the stock
// "temporal" plugin's Config.InitDefault).
func (c *Config) InitDefaults() {
	if c.Address == "" {
		c.Address = defaultAddress
	}
	if c.Namespace == "" {
		c.Namespace = defaultNamespace
	}
	if c.TaskQueue == "" {
		c.TaskQueue = defaultTaskQueue
	}
	if c.Pool == nil {
		c.Pool = &pool.Config{}
	}
	if len(c.Pool.Command) == 0 {
		c.Pool.Command = defaultCommand
	}
	c.Pool.InitDefaults()
}
