package temporal

import (
	"temporal-gateway/internal/config"
)

// Catalog is the set of workflow types the gateway may address, along with
// each one's default task queue. It is declared under temporal.workflows in
// the gateway config and used only to fill in a task queue an x-temporal
// binding doesn't specify directly.
type Catalog struct {
	workflows map[string]config.WorkflowDefinition
}

// NewCatalog builds a Catalog from the gateway config's temporal.workflows.
func NewCatalog(defs []config.WorkflowDefinition) *Catalog {
	m := make(map[string]config.WorkflowDefinition, len(defs))
	for _, d := range defs {
		m[d.Name] = d
	}
	return &Catalog{workflows: m}
}

// TaskQueueFor returns the default task queue declared for a workflow type.
func (c *Catalog) TaskQueueFor(workflowType string) (string, bool) {
	d, ok := c.workflows[workflowType]
	if !ok || d.TaskQueue == "" {
		return "", false
	}
	return d.TaskQueue, true
}
