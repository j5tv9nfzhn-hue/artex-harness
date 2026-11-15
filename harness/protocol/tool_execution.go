// Package protocol — tool-execution classification (Sprint 1.1).
//
// The streaming executor in norma/harness/streaming.go already runs safe tools in
// parallel and gives exclusive access to non-safe ones. This file makes that
// decision visible in the event stream: an operator should be able to answer
// "which tools may overlap, and which ones serialise the turn?" without reading
// the SDK source.
//
// Design reference: mature harnesses expose the scheduling class of each tool
// as a first-class property. Here the classification is computed from the same
// CoreTool methods the executor itself uses, so the report cannot drift from the
// behaviour it describes.
package protocol

// ToolExecutionClass is the scheduling class of one tool call.
type ToolExecutionClass string

const (
	// ToolParallel may run alongside other parallel calls.
	ToolParallel ToolExecutionClass = "parallel"
	// ToolExclusive requires exclusive access and blocks the calls behind it.
	ToolExclusive ToolExecutionClass = "exclusive"
)

// ClassifyToolExecution mirrors streamExec.add: it asks the tool whether the
// specific input is concurrency-safe. A tool that reports no verdict is
// exclusive (the fail-closed default in norma/tool.Build).
//
// readOnly is carried alongside the verdict so callers can render both facts:
// "read-only" describes what the tool can do to the host, "parallel" describes
// how the executor may schedule it. They are deliberately separate.
func ClassifyToolExecution(concurrencySafe, readOnly bool) (class ToolExecutionClass, isReadOnly bool) {
	if concurrencySafe {
		return ToolParallel, readOnly
	}
	return ToolExclusive, readOnly
}
