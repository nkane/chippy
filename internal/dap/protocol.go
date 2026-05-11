// Package dap implements a Debug Adapter Protocol server for chippy. The
// DAP wire format is JSON-RPC-flavored: each message is a JSON object framed
// by an HTTP-style `Content-Length` header. Servers consume requests + emit
// responses or events; events are unprompted notifications.
//
// This package handles the wire layer plus a request-dispatch loop. Real
// debugger functionality lives in the request handlers (launch, breakpoints,
// step controls, etc.) and is added one issue at a time per the #46 epic.
//
// Spec: https://microsoft.github.io/debug-adapter-protocol/
package dap

import "encoding/json"

// ProtocolMessage is the base envelope for every DAP message.
type ProtocolMessage struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"` // "request" | "response" | "event"
}

// Request is a client-initiated call expecting a Response back.
type Request struct {
	ProtocolMessage
	Command   string          `json:"command"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Response carries the result of a Request. Success=false + Message set when
// the server can't fulfill the request.
type Response struct {
	ProtocolMessage
	RequestSeq int         `json:"request_seq"`
	Success    bool        `json:"success"`
	Command    string      `json:"command"`
	Message    string      `json:"message,omitempty"`
	Body       interface{} `json:"body,omitempty"`
}

// Event is an unprompted server-to-client notification.
type Event struct {
	ProtocolMessage
	Event string      `json:"event"`
	Body  interface{} `json:"body,omitempty"`
}

// InitializeArguments is the subset of `initialize` request args we use.
// The full DAP spec has many more capability negotiation fields; clients
// degrade gracefully on missing values.
type InitializeArguments struct {
	ClientID                     string `json:"clientID,omitempty"`
	ClientName                   string `json:"clientName,omitempty"`
	AdapterID                    string `json:"adapterID,omitempty"`
	Locale                       string `json:"locale,omitempty"`
	LinesStartAt1                bool   `json:"linesStartAt1,omitempty"`
	ColumnsStartAt1              bool   `json:"columnsStartAt1,omitempty"`
	PathFormat                   string `json:"pathFormat,omitempty"`
	SupportsVariableType         bool   `json:"supportsVariableType,omitempty"`
	SupportsRunInTerminalRequest bool   `json:"supportsRunInTerminalRequest,omitempty"`
}

// Capabilities is the server's declaration of what requests it honors. The
// shape mirrors `Capabilities` in the DAP spec; only the fields we actually
// advertise are populated for now — future issues add to this set as new
// requests are wired up.
type Capabilities struct {
	SupportsConfigurationDoneRequest   bool `json:"supportsConfigurationDoneRequest"`
	SupportsFunctionBreakpoints        bool `json:"supportsFunctionBreakpoints"`
	SupportsConditionalBreakpoints     bool `json:"supportsConditionalBreakpoints"`
	SupportsHitConditionalBreakpoints  bool `json:"supportsHitConditionalBreakpoints"`
	SupportsEvaluateForHovers          bool `json:"supportsEvaluateForHovers"`
	SupportsStepBack                   bool `json:"supportsStepBack"`
	SupportsSetVariable                bool `json:"supportsSetVariable"`
	SupportsRestartFrame               bool `json:"supportsRestartFrame"`
	SupportsRestartRequest             bool `json:"supportsRestartRequest"`
	SupportsExceptionInfoRequest       bool `json:"supportsExceptionInfoRequest"`
	SupportsTerminateRequest           bool `json:"supportsTerminateRequest"`
	SupportsDisassembleRequest         bool `json:"supportsDisassembleRequest"`
	SupportsReadMemoryRequest          bool `json:"supportsReadMemoryRequest"`
	SupportsWriteMemoryRequest         bool `json:"supportsWriteMemoryRequest"`
	SupportsInstructionBreakpoints     bool `json:"supportsInstructionBreakpoints"`
	SupportsSteppingGranularity        bool `json:"supportsSteppingGranularity"`
	SupportsLogPoints                  bool `json:"supportsLogPoints"`
	SupportsDelayedStackTraceLoading   bool `json:"supportsDelayedStackTraceLoading"`
	SupportsLoadedSourcesRequest       bool `json:"supportsLoadedSourcesRequest"`
	SupportsCompletionsRequest         bool `json:"supportsCompletionsRequest"`
	SupportsBreakpointLocationsRequest bool `json:"supportsBreakpointLocationsRequest"`
}

// LaunchArguments is the chippy-specific subset of a `launch` request body.
// Matches the CLI flags (rom, addr, reset, cpu, dbg, trace) so a launch
// config in the editor looks like the chippy CLI invocation.
type LaunchArguments struct {
	NoDebug     bool   `json:"noDebug,omitempty"`
	StopOnEntry bool   `json:"stopOnEntry,omitempty"`
	Rom         string `json:"rom,omitempty"`
	LoadAddr    uint16 `json:"loadAddr,omitempty"`
	ResetVec    uint16 `json:"resetVec,omitempty"`
	LinkerCfg   string `json:"linkerCfg,omitempty"`
	DbgPath     string `json:"dbgPath,omitempty"`
	CPUVariant  string `json:"cpuVariant,omitempty"` // "nmos" | "65c02"
	TracePath   string `json:"tracePath,omitempty"`
}

// StoppedEventBody is the body of a `stopped` event. Reason values include
// "entry", "step", "breakpoint", "pause", "exception".
type StoppedEventBody struct {
	Reason            string `json:"reason"`
	Description       string `json:"description,omitempty"`
	ThreadID          int    `json:"threadId,omitempty"`
	PreserveFocusHint bool   `json:"preserveFocusHint,omitempty"`
	Text              string `json:"text,omitempty"`
	AllThreadsStopped bool   `json:"allThreadsStopped,omitempty"`
}

// TerminatedEventBody is the body of a `terminated` event.
type TerminatedEventBody struct {
	Restart bool `json:"restart,omitempty"`
}

// Source identifies a source file in stack-frame / breakpoint requests.
type Source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// StackFrame is one entry in a `stackTrace` response. ID must be unique
// per stopped point so the client can correlate frames with subsequent
// `scopes` and `variables` requests.
type StackFrame struct {
	ID                          int     `json:"id"`
	Name                        string  `json:"name"`
	Source                      *Source `json:"source,omitempty"`
	Line                        int     `json:"line,omitempty"`
	Column                      int     `json:"column,omitempty"`
	InstructionPointerReference string  `json:"instructionPointerReference,omitempty"`
}

// Scope groups variables under a named heading in the editor's Variables
// pane. VariablesReference is the handle the client passes to a follow-up
// `variables` request.
type Scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}

// Variable is one row in the Variables pane. VariablesReference > 0 means
// the entry has children; the client expands it via `variables` again.
type Variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference,omitempty"`
}

// StackTraceArguments is the request body for `stackTrace`.
type StackTraceArguments struct {
	ThreadID   int `json:"threadId"`
	StartFrame int `json:"startFrame,omitempty"`
	Levels     int `json:"levels,omitempty"`
}

// ScopesArguments is the request body for `scopes`.
type ScopesArguments struct {
	FrameID int `json:"frameId"`
}

// VariablesArguments is the request body for `variables`.
type VariablesArguments struct {
	VariablesReference int `json:"variablesReference"`
}

// SetVariableArguments is the request body for `setVariable`.
type SetVariableArguments struct {
	VariablesReference int    `json:"variablesReference"`
	Name               string `json:"name"`
	Value              string `json:"value"`
}
