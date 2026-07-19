// Package protocol defines the WebSocket message envelope exchanged between
// the browser-side client-tools bridge (web/src/DebugApp.tsx's lightweight
// port of agent's packages/bridge/src/client.ts) and this backend's
// clienttools WS session (server/internal/clienttools). Modeled directly on
// /Users/caitingyu/Documents/agent's backend/internal/protocol/message.go —
// same message names, same envelope shape — trimmed to only what this POC's
// single "clienttools" app needs (no per-app SDK version negotiation, no
// quota error code: see that package's doc comments for the parts
// deliberately not reproduced here).
package protocol

import "encoding/json"

// MessageType identifies the shape of Payload in an Envelope.
type MessageType string

const (
	// Client (browser) -> Server

	// TypeHello is sent once when a session connects, carrying the appId
	// (fixed to "clienttools" for this POC — see server/tools/clienttools.yaml)
	// whose tool set this session should resolve.
	TypeHello MessageType = "hello"

	// TypePrompt sends a user-typed sentence for the inference service to
	// reason about (this POC's dedicated clienttools agent role — see
	// server/internal/llm/clienttools_agent.go — not the regular assistant).
	TypePrompt MessageType = "prompt"

	// TypeToolResult returns the outcome of a tool call the client executed
	// against its in-memory trip entry list (add/delete/update/list).
	TypeToolResult MessageType = "tool_result"

	// Server -> Client

	// TypeAck acknowledges a Hello and returns the resolved tool set for
	// the session (the appId's tool names, from the Registry).
	TypeAck MessageType = "ack"

	// TypeToolCall instructs the client to execute a named tool with
	// arguments produced by the inference service. Fire-and-forget from the
	// inference service's perspective in the sense that want's tool call
	// considers itself "done" once the client confirms success/failure —
	// but the confirmation itself is still awaited synchronously (see
	// clienttools.forwardingTool and toolschema.ToolKindAction): nothing
	// the client computed (e.g. the trip list's new length) flows back into
	// the LLM's reasoning, only whether the call succeeded.
	TypeToolCall MessageType = "tool_call"

	// TypeToolQuery instructs the client to run a named tool's handler and
	// report its return value back via TypeToolResult — the inference
	// service is blocked waiting for that TypeToolResult and feeds the
	// answer back into the LLM's context (e.g. a newly-added entry's id, or
	// the full current trip list). See toolschema.ToolKindQuery and
	// clienttools.queryTool/askPage for the server-side half.
	TypeToolQuery MessageType = "tool_query"

	// TypeAssistantMessage carries a natural-language message meant for
	// display to the end user (no state side effect on the trip list).
	TypeAssistantMessage MessageType = "assistant_message"

	// TypeError reports a protocol- or inference-level error tied to a
	// specific request (by RequestID) or the connection as a whole.
	TypeError MessageType = "error"
)

// Envelope is the single message shape sent over the WebSocket connection in
// both directions. Payload is decoded based on Type.
type Envelope struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// HelloPayload is the Payload of a TypeHello message.
type HelloPayload struct {
	AppID string `json:"appId"`
}

// AckPayload is the Payload of a TypeAck message.
type AckPayload struct {
	SessionID string   `json:"sessionId"`
	ToolNames []string `json:"toolNames"`
}

// PromptPayload is the Payload of a TypePrompt message.
type PromptPayload struct {
	Text string `json:"text"`
}

// ToolCallPayload is the Payload of a TypeToolCall/TypeToolQuery message —
// both share this shape (see MessageType's doc comments for the behavioral
// difference; the wire shape carrying the call itself is identical).
type ToolCallPayload struct {
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
}

// ToolResultPayload is the Payload of a TypeToolResult message.
type ToolResultPayload struct {
	ToolName string          `json:"toolName"`
	OK       bool            `json:"ok"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// AssistantMessagePayload is the Payload of a TypeAssistantMessage message.
type AssistantMessagePayload struct {
	Text string `json:"text"`
}

// ErrorPayload is the Payload of a TypeError message.
type ErrorPayload struct {
	Message string `json:"message"`
}
