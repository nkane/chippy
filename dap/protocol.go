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
	SupportsDataBreakpoints            bool `json:"supportsDataBreakpoints"`
	SupportsSteppingGranularity        bool `json:"supportsSteppingGranularity"`
	SupportsLogPoints                  bool `json:"supportsLogPoints"`
	SupportsDelayedStackTraceLoading   bool `json:"supportsDelayedStackTraceLoading"`
	SupportsLoadedSourcesRequest       bool `json:"supportsLoadedSourcesRequest"`
	SupportsCompletionsRequest         bool `json:"supportsCompletionsRequest"`
	SupportsBreakpointLocationsRequest bool `json:"supportsBreakpointLocationsRequest"`
	SupportsVariablePaging             bool `json:"supportsVariablePaging"`
}

// LaunchArguments is the chippy-specific subset of a `launch` request body.
// Matches the CLI flags (rom, addr, reset, cpu, dbg, trace) so a launch
// config in the editor looks like the chippy CLI invocation.
type LaunchArguments struct {
	NoDebug bool `json:"noDebug,omitempty"`
	// StopOnEntry: pointer so we can distinguish "absent" (default to
	// pause) from "explicit false" (auto-run after launch).
	StopOnEntry *bool  `json:"stopOnEntry,omitempty"`
	Rom         string `json:"rom,omitempty"`
	LoadAddr    uint16 `json:"loadAddr,omitempty"`
	ResetVec    uint16 `json:"resetVec,omitempty"`
	LinkerCfg   string `json:"linkerCfg,omitempty"`
	DbgPath     string `json:"dbgPath,omitempty"`
	CPUVariant  string `json:"cpuVariant,omitempty"` // "nmos" | "65c02"
	TracePath   string `json:"tracePath,omitempty"`
}

// AttachArguments is the editor-side request body for `attach`. Empty
// `processId` means "attach to whatever debuggee the server is already
// running"; non-empty is reserved for a future cross-process flow.
type AttachArguments struct {
	ProcessID string `json:"processId,omitempty"`
	// StopOnEntry: pointer so absent / explicit-false / explicit-true
	// each map to a distinct behavior (default pause / no stopped event
	// / explicit pause).
	StopOnEntry *bool `json:"stopOnEntry,omitempty"`
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

// ChippyStateEvent is the name of chippy's custom live-state event.
const ChippyStateEvent = "chippy-state"

// ChippyStateBody is the body of the custom `chippy-state` event — a
// server→client push of live CPU state during free-run, so a client (the
// chippy TUI, vscode-chippy) updates its panels without per-frame `variables`
// polling. It is chippy-specific; standard DAP clients ignore unknown events.
// Throttled server-side to ≤60 Hz. Values are raw (not "$XX" strings) since
// both ends are chippy.
type ChippyStateBody struct {
	A      byte   `json:"a"`
	X      byte   `json:"x"`
	Y      byte   `json:"y"`
	SP     byte   `json:"sp"`
	P      byte   `json:"p"`
	PC     uint16 `json:"pc"`
	Cycles uint64 `json:"cycles"`
	Halted bool   `json:"halted"`
	// DirtyRanges carries the memory written since the previous chippy-state
	// event, coalesced into spans (issue #440). Each span ships its current
	// bytes inline so the client applies the delta to its mirror without a
	// follow-up readMemory. Empty when no memory changed in the window.
	DirtyRanges []MemRange `json:"dirtyRanges,omitempty"`
}

// MemRange is a half-open [Start, End) byte range. When carried on a
// chippy-state event its Data holds the End-Start bytes at Start (base64 on
// the wire); consumers should treat Start+len(Data) as authoritative since a
// span ending at $FFFF makes End wrap.
type MemRange struct {
	Start uint16 `json:"start"`
	End   uint16 `json:"end"`
	Data  []byte `json:"data,omitempty"`
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

	// ChippyStackAddr (chippy extension; standard DAP clients ignore unknown
	// fields) is the "$01XX" address in the 6502 stack page where this frame's
	// pushed return-address pair begins. Empty for frame 0 (the live PC, which
	// is not a stack-page entry). Lets the chippy TUI render its stack-page
	// panel layout directly from the stackTrace response (issue #449).
	ChippyStackAddr string `json:"chippyStackAddr,omitempty"`
	// ChippyCallee (chippy extension) is the symbol at this frame's JSR call
	// target — the routine that was invoked — distinct from Name, which names
	// the routine that resumes at the return address. Empty when no symbol
	// covers the target (issue #449).
	ChippyCallee string `json:"chippyCallee,omitempty"`
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
	// IndexedVariables hints the element count of an expandable indexed
	// collection (array), letting the client page large arrays via the
	// `start`/`count` fields of a follow-up `variables` request.
	IndexedVariables int `json:"indexedVariables,omitempty"`
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

// VariablesArguments is the request body for `variables`. Start/Count are the
// optional paging window a client uses to fetch a slice of a large indexed
// collection (array children); Count == 0 means "all from Start".
type VariablesArguments struct {
	VariablesReference int `json:"variablesReference"`
	Start              int `json:"start,omitempty"`
	Count              int `json:"count,omitempty"`
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

// DataBreakpointAccessType is the access that triggers a data breakpoint
// (watchpoint): a read, a write, or either. Mirrors the TUI's MemBP kinds.
type DataBreakpointAccessType string

const (
	DataAccessRead      DataBreakpointAccessType = "read"
	DataAccessWrite     DataBreakpointAccessType = "write"
	DataAccessReadWrite DataBreakpointAccessType = "readWrite"
)

// DataBreakpointInfoArguments resolves a memory reference / symbol to a
// data-breakpoint id the client then passes to setDataBreakpoints. chippy
// accepts a hex address ("$XXXX" / "0xXX"), a decimal address, or a loaded
// symbol name in Name; VariablesReference is ignored (memory is global).
type DataBreakpointInfoArguments struct {
	VariablesReference int    `json:"variablesReference,omitempty"`
	Name               string `json:"name"`
	FrameID            int    `json:"frameId,omitempty"`
}

// DataBreakpoint is one watchpoint in a setDataBreakpoints request. DataID is
// the id returned by dataBreakpointInfo ("$XXXX" for chippy). AccessType
// defaults to write when empty.
type DataBreakpoint struct {
	DataID       string                   `json:"dataId"`
	AccessType   DataBreakpointAccessType `json:"accessType,omitempty"`
	Condition    string                   `json:"condition,omitempty"`
	HitCondition string                   `json:"hitCondition,omitempty"`
}

// SetDataBreakpointsArguments — replace ALL data breakpoints (watchpoints).
type SetDataBreakpointsArguments struct {
	Breakpoints []DataBreakpoint `json:"breakpoints"`
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

// BreakpointLocationsArguments — DAP request body for the editor's
// gutter feature ("here's a line, what positions on that line could
// actually take a breakpoint?"). We answer at line granularity.
type BreakpointLocationsArguments struct {
	Source    Source `json:"source"`
	Line      int    `json:"line"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
}

// BreakpointLocation — one position the gutter could mark as bp-able.
type BreakpointLocation struct {
	Line      int `json:"line"`
	Column    int `json:"column,omitempty"`
	EndLine   int `json:"endLine,omitempty"`
	EndColumn int `json:"endColumn,omitempty"`
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

// ExceptionBreakpointsFilter is one selectable filter the client offers
// in its Breakpoints pane. The user toggles each filter on/off; the
// resulting set arrives via setExceptionBreakpoints.
type ExceptionBreakpointsFilter struct {
	Filter            string `json:"filter"`
	Label             string `json:"label"`
	Description       string `json:"description,omitempty"`
	Default           bool   `json:"default,omitempty"`
	SupportsCondition bool   `json:"supportsCondition,omitempty"`
}

// SetExceptionBreakpointsArguments is the request body for
// `setExceptionBreakpoints` — the list of filter IDs currently enabled.
type SetExceptionBreakpointsArguments struct {
	Filters []string `json:"filters"`
}

// ExceptionInfoResponseBody is what `exceptionInfo` returns when the
// editor asks for details on the active exception.
type ExceptionInfoResponseBody struct {
	ExceptionID string `json:"exceptionId"`
	Description string `json:"description,omitempty"`
	BreakMode   string `json:"breakMode"`
}
