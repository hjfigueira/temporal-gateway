package spec

// CascadeTrigger is one x-temporal.triggers entry as passed into the
// CascadeEvent workflow the "nexus" driver starts (see NexusConfig and
// internal/temporal.CascadeEvent): the same TemporalBinding declared in the
// spec, but with WorkflowID already rendered against the incoming HTTP
// request - a workflow has no access to that request, so the substitution
// (internal/gateway's renderTemplate) has to happen before the trigger ever
// leaves the gateway process - plus the name of the Nexus endpoint that
// reaches the binding's target namespace (see
// internal/config.TemporalConnectionConfig.NexusEndpoint), which
// CascadeEvent needs to actually reach it.
type CascadeTrigger struct {
	TemporalBinding
	NexusEndpoint string `json:"nexusEndpoint"`
}

// CascadeInput is the CascadeEvent workflow's input: the original request
// body (so the workflow can reference other fields the way a direct
// dispatch could) plus every trigger the operation declared, already
// rendered (see CascadeTrigger).
type CascadeInput struct {
	Body     any              `json:"body,omitempty"`
	Triggers []CascadeTrigger `json:"triggers"`
}

// CascadeTriggerResult is one trigger's outcome from the Dispatch Nexus
// operation CascadeEvent invoked for it - either Result or Error is set,
// never both. This is carried in CascadeEvent's own return value and
// workflow history; it never reaches the gateway's HTTP response directly,
// since that response reports only the CascadeEvent workflow's own start
// outcome (see internal/gateway.NexusDriver).
type CascadeTriggerResult struct {
	Namespace  string         `json:"namespace"`
	WorkflowID string         `json:"workflowId"`
	Action     TemporalAction `json:"action"`
	Result     any            `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// CascadeResult is the CascadeEvent workflow's return value: every
// trigger's outcome, in the same order as CascadeInput.Triggers.
type CascadeResult struct {
	Results []CascadeTriggerResult `json:"results"`
}

// DispatchInput is the input to the Dispatch Nexus operation (see
// internal/temporal.NewDispatchService): a single trigger's binding, plus
// the original request body, mirroring exactly what the "direct" driver
// passes to Dispatcher.Dispatch in-process.
type DispatchInput struct {
	Binding TemporalBinding `json:"binding"`
	Body    any             `json:"body,omitempty"`
}

// DispatchOutput is the Dispatch Nexus operation's result: the same
// JSON-serializable value Dispatcher.Dispatch would have returned directly.
type DispatchOutput struct {
	Result any `json:"result,omitempty"`
}
