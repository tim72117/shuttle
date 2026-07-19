// Package clienttools bridges want tool calls to a browser tab's in-memory
// trip entry list over WebSocket, for the "LLM calls a frontend tool" POC
// (see web/src/DebugApp.tsx for the browser side). Modeled directly on
// /Users/caitingyu/Documents/agent's backend/internal/inference package
// (agent_roles.go + interaction.go) — see this package's doc comments for
// what's the same and what's trimmed for this POC's single-app,
// single-session-at-a-time scope.
//
// Unlike the agent project (whose comment on this exact pattern explains it
// deliberately avoids want's own ToolContext.RequestInteraction because that
// platform has no want UI behind it at all), shuttle's want version
// (github.com/tim72117/want v0.0.2) actually ships a working
// RequestInteraction/ResolveInteraction bridge on *orchestrator.Orchestrator
// itself (see orchestrator/interaction.go's interactionRegistry) — but nothing
// in shuttle uses it yet (grep the repo: entry_add/entry_query/ask_user/
// ask_choice are all either synchronous store calls or one-way
// NotifyXxx broadcasts, never a blocking round trip to the browser). This
// package intentionally builds its own parallel pendingCalls-and-block
// mechanism instead of wiring into orch.RequestInteraction, per this POC's
// explicit goal: prove out the agent project's InteractionAsker/pendingCalls
// architecture end-to-end on shuttle, not just discover that want already has
// something adjacent. A production follow-up could consider consolidating
// onto orch.RequestInteraction later; see also clienttools/agent_role.go's
// doc comment on why a second, independent *orchestrator.Orchestrator exists
// for this POC instead of reusing shuttle's shared assistant one.
package clienttools

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tim72117/tripace/internal/toolschema"
)

// InteractionAsker is what a WS session implements to let a tool (either
// ToolKind — see forwardingTool/queryTool in tool.go) reach the actual
// connected browser tab: send it a request, block until the browser answers
// (or the session decides to time out/error), return the answer. kind only
// affects which wire message type the browser receives (tool_call vs
// tool_query — see protocol.MessageType); both are handled identically by
// the browser-side bridge (see web/src/DebugApp.tsx) — the blocking/bridging
// behavior here is otherwise the same for both kinds.
//
// The tool's Call runs inside want's own dispatch goroutine (see
// orchestrator.Orchestrator.dispatch), with no direct reference to whatever
// object owns the actual WS connection. RegisterAsker/lookupAsker is the
// bridge: the WS session registers itself here (keyed by its own session id)
// when it starts, and deregisters when the connection closes. The calling
// tool recovers the session id from ctx.GetSessionEnvs()["sessionID"] — set
// once via orch.SetSessionEnvs before Submit, the same SessionEnvs plumbing
// shuttle's existing wanttools package already uses for channelID (see
// wanttools.ChannelFrom) — rather than ctx.GetAgentID(), because in this
// want version AgentID is fixed per *orchestrator.Orchestrator instance for
// its whole lifetime (see orchestrator.NewOrchestrator: generated once, not
// per-session), unlike the agent project's platform where AgentID is
// prefixed per-WS-session by the host. This POC's dedicated orchestrator
// (see agent_role.go) serves exactly one browser tab's prompts at a time,
// so a single SessionEnvs-carried id is enough to identify "the tab this
// call should ask" without needing want itself to be session-aware.
type InteractionAsker interface {
	AskInteraction(toolName string, args json.RawMessage, kind toolschema.ToolKind) (json.RawMessage, error)
}

var (
	askersMu sync.RWMutex
	askers   = make(map[string]InteractionAsker)
)

// RegisterAsker makes sessionID's asker reachable from queryTool.Call /
// forwardingTool.Call.
func RegisterAsker(sessionID string, asker InteractionAsker) {
	askersMu.Lock()
	askers[sessionID] = asker
	askersMu.Unlock()
}

// UnregisterAsker removes sessionID's asker (call on connection close, or a
// later tool call would otherwise reach a closed/gone session and hang
// until AskInteraction's own timeout).
func UnregisterAsker(sessionID string) {
	askersMu.Lock()
	delete(askers, sessionID)
	askersMu.Unlock()
}

func lookupAsker(sessionID string) (InteractionAsker, bool) {
	askersMu.RLock()
	defer askersMu.RUnlock()
	a, ok := askers[sessionID]
	return a, ok
}

// sessionIDFromEnvs reads the WS session id a tool call should reach, out of
// the SessionEnvs the orchestrator attached to this call's ToolUseContext
// (see this file's InteractionAsker doc comment for why SessionEnvs rather
// than GetAgentID()).
func sessionIDFromEnvs(envs map[string]string) (string, bool) {
	if envs == nil {
		return "", false
	}
	id, ok := envs["sessionID"]
	return id, ok && id != ""
}

// askPage is forwardingTool.Call's and queryTool.Call's shared bridge to the
// browser: resolve the current want call's session from sessionEnvs, find
// its registered asker, and block on it. kind is passed straight through to
// AskInteraction to pick the wire message type.
func askPage(sessionEnvs map[string]string, toolName string, args json.RawMessage, kind toolschema.ToolKind) (json.RawMessage, error) {
	sessionID, ok := sessionIDFromEnvs(sessionEnvs)
	if !ok {
		return nil, fmt.Errorf("tools that wait for the page aren't available in this context (no session id on this call)")
	}
	asker, ok := lookupAsker(sessionID)
	if !ok {
		return nil, fmt.Errorf("no connected page for session %q (it may have disconnected)", sessionID)
	}
	return asker.AskInteraction(toolName, args, kind)
}
