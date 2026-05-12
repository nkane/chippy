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

// SourceBreakpoint is one entry in a `setBreakpoints` request body.
type SourceBreakpoint struct {
	Line         int    `json:"line"`
	Column       int    `json:"column,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	LogMessage   string `json:"logMessage,omitempty"`
}

// SetBreakpointsArguments — replace ALL source-line breakpoints for the
// given source. Each call is destructive against prior bps for the same
// path; bps in other sources are unaffected.
type SetBreakpointsArguments struct {
	Source      Source             `json:"source"`
	Breakpoints []SourceBreakpoint `json:"breakpoints"`
}

// InstructionBreakpoint is one entry in a `setInstructionBreakpoints`
// request body. InstructionReference is typically the hex address string
// `stackTrace` returned for a frame's IP.
type InstructionBreakpoint struct {
	InstructionReference string `json:"instructionReference"`
	Offset               int    `json:"offset,omitempty"`
	Condition            string `json:"condition,omitempty"`
	HitCondition         string `json:"hitCondition,omitempty"`
}

// SetInstructionBreakpointsArguments — replace ALL address breakpoints.
type SetInstructionBreakpointsArguments struct {
	Breakpoints []InstructionBreakpoint `json:"breakpoints"`
}

// FunctionBreakpoint identifies a breakpoint by symbol name. Resolution
// happens at set time via the loaded .dbg symbol table.
type FunctionBreakpoint struct {
	Name         string `json:"name"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
}

// SetFunctionBreakpointsArguments — replace ALL symbol-name breakpoints.
type SetFunctionBreakpointsArguments struct {
	Breakpoints []FunctionBreakpoint `json:"breakpoints"`
}

// Breakpoint is what `setBreakpoints` / `setInstructionBreakpoints`
// return: per-entry resolution status plus the resolved location.
type Breakpoint struct {
	ID                          int     `json:"id,omitempty"`
	Verified                    bool    `json:"verified"`
	Message                     string  `json:"message,omitempty"`
	Source                      *Source `json:"source,omitempty"`
	Line                        int     `json:"line,omitempty"`
	InstructionPointerReference string  `json:"instructionPointerReference,omitempty"`
}

// DisassembleArguments is the request body for `disassemble`.
type DisassembleArguments struct {
	MemoryReference   string `json:"memoryReference"`
	Offset            int    `json:"offset,omitempty"`
	InstructionOffset int    `json:"instructionOffset,omitempty"`
	InstructionCount  int    `json:"instructionCount"`
	ResolveSymbols    bool   `json:"resolveSymbols,omitempty"`
}

// DisassembledInstruction is one row in a `disassemble` response.
type DisassembledInstruction struct {
	Address          string  `json:"address"`
	InstructionBytes string  `json:"instructionBytes,omitempty"`
	Instruction      string  `json:"instruction"`
	Symbol           string  `json:"symbol,omitempty"`
	Location         *Source `json:"location,omitempty"`
	Line             int     `json:"line,omitempty"`
}

// ReadMemoryArguments is the request body for `readMemory`.
type ReadMemoryArguments struct {
	MemoryReference string `json:"memoryReference"`
	Offset          int    `json:"offset,omitempty"`
	Count           int    `json:"count"`
}

// WriteMemoryArguments is the request body for `writeMemory`. `Data` is
// base64-encoded raw bytes (DAP spec).
type WriteMemoryArguments struct {
	MemoryReference string `json:"memoryReference"`
	Offset          int    `json:"offset,omitempty"`
	AllowPartial    bool   `json:"allowPartial,omitempty"`
	Data            string `json:"data"`
}

// EvaluateArguments is the request body for `evaluate`. Context indicates
// where the request came from: "watch", "repl", "hover", "clipboard".
type EvaluateArguments struct {
	Expression string `json:"expression"`
	FrameID    int    `json:"frameId,omitempty"`
	Context    string `json:"context,omitempty"`
	Format     struct {
		Hex bool `json:"hex,omitempty"`
	} `json:"format,omitempty"`
}

// SourceArguments is the request body for `source` — return file contents
// for a previously-listed Source.
type SourceArguments struct {
	Source          Source `json:"source"`
	SourceReference int    `json:"sourceReference,omitempty"`
}

// CompletionsArguments is the request body for `completions`. Text is the
// in-progress expression; Column is the 1-based cursor position.
type CompletionsArguments struct {
	FrameID int    `json:"frameId,omitempty"`
	Text    string `json:"text"`
	Column  int    `json:"column"`
	Line    int    `json:"line,omitempty"`
}

// CompletionItem is one row in a `completions` response.
type CompletionItem struct {
	Label  string `json:"label"`
	Text   string `json:"text,omitempty"`
	Type   string `json:"type,omitempty"`
	Start  int    `json:"start,omitempty"`
	Length int    `json:"length,omitempty"`
}
