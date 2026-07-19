package api

// clienttoolsSession implements the WS half of the "LLM calls a frontend
// tool" POC: one Session per connected browser tab (web/src/DebugApp.tsx's
// lightweight bridge), speaking the protocol.Envelope wire format. Modeled
// directly on /Users/caitingyu/Documents/agent's backend/internal/ws/session.go
// — same pendingCalls-map-of-channels blocking mechanism, same read-loop/
// prompt-goroutine split to avoid the deadlock documented below — adapted
// from gorilla/websocket (agent's library) to shuttle's nhooyr.io/websocket
// (Conn.Read(ctx)/Write(ctx, typ, p) instead of ReadMessage/WriteMessage).
//
// This is deliberately a separate type from Session (hub.go/ws.go), not a
// mode grafted onto it: Session's handleWS is scoped to a real channel
// (membership-checked against s.store) and only ever broadcasts one-way
// (entries_updated, recommended_places, ...) via Hub — it has no per-
// connection request/response bridging need. Reusing it here would mean
// either bolting a second, unrelated blocking mechanism onto a type that
// already has a clear job, or making every real channel session pay for
// machinery it never uses. A dedicated type keeps the "does this connection
// need to block on an answer" concern local to the one POC that needs it.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tim72117/tripace/internal/clienttools"
	"github.com/tim72117/tripace/internal/llm"
	"github.com/tim72117/tripace/internal/protocol"
	"github.com/tim72117/tripace/internal/toolschema"

	"nhooyr.io/websocket"
)

// clientToolsWriteTimeout/PingInterval mirror agent's ws/session.go
// constants (writeTimeout/pingInterval) — kept as a POC-local set rather
// than shared with Session/Hub's own timeouts, which serve a
// differently-shaped connection (see this file's package doc comment).
// There's no PongTimeout here (agent's version has one): that constant
// bounded gorilla/websocket's SetReadDeadline, an idiom nhooyr.io/websocket
// doesn't use — see run's and startPingLoop's own comments for how dead-peer
// detection works here instead.
const (
	clientToolsWriteTimeout = 10 * time.Second
	clientToolsPingInterval = 30 * time.Second
)

// clientToolsInteractionTimeout bounds how long AskInteraction waits for the
// browser to answer a tool_call/tool_query before giving up. Deliberately
// shorter than clientToolsTimeout (llm.ClientToolsAnalyzer.Prompt's own 90s
// end-to-end budget — see clienttools_agent.go), so a page that never
// answers fails with a clear "the page didn't answer in time" on the one
// stuck tool call, rather than the whole prompt instead riding out to the
// unrelated 90s ceiling. Same reasoning and same 20s value as agent's own
// ws/session.go interactionTimeout.
const clientToolsInteractionTimeout = 20 * time.Second

// clientToolsSession is one connected browser tab running
// web/src/DebugApp.tsx's client-tools bridge.
type clientToolsSession struct {
	id       string
	conn     *websocket.Conn
	registry *toolschema.Registry
	analyzer *llm.ClientToolsAnalyzer
	writeMu  sync.Mutex

	mu           sync.Mutex
	app          *toolschema.App
	pendingCalls map[string]chan protocol.ToolResultPayload
}

// runClientToolsSession wires a freshly-upgraded connection into a
// clientToolsSession and blocks (in the caller's own goroutine — the HTTP
// handler's) until the connection closes, running the read loop directly on
// that goroutine exactly like agent's ws.NewSession/Session.run. sessions is
// the live-session tracker handleClientToolsTestPrompt uses to find "the
// page" (see clienttools_sessions.go).
func runClientToolsSession(ctx context.Context, conn *websocket.Conn, registry *toolschema.Registry, analyzer *llm.ClientToolsAnalyzer, sessions *clientToolsSessions) {
	s := &clientToolsSession{
		id:           "cts_" + newID(),
		conn:         conn,
		registry:     registry,
		analyzer:     analyzer,
		pendingCalls: make(map[string]chan protocol.ToolResultPayload),
	}
	// Makes s reachable from a trip_entry_* tool's Call, which runs inside
	// want's own dispatch goroutine with no reference to this session — see
	// clienttools.RegisterAsker's doc comment for the full path back here.
	// Must be deregistered on close (below): a lingering entry would let a
	// later tool call reach a closed connection and hang until
	// clientToolsInteractionTimeout gives up.
	clienttools.RegisterAsker(s.id, s)
	defer clienttools.UnregisterAsker(s.id)

	sessions.add(s)
	defer sessions.remove(s)

	s.run(ctx)
}

func (s *clientToolsSession) run(ctx context.Context) {
	defer s.conn.Close(websocket.StatusNormalClosure, "")

	stopPing := s.startPingLoop(ctx)
	defer stopPing()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// No per-call read timeout here — deliberately, after first getting
		// this wrong (see git history/PR description): wrapping every Read in
		// a fresh clientToolsPongTimeout context.WithTimeout kills a
		// perfectly healthy connection the instant no *client-originated*
		// message arrives within that window, which is exactly what happens
		// while a prompt is legitimately in flight (up to
		// llm.clientToolsTimeout = 90s) waiting on a tool_query round trip —
		// the browser has nothing more to send until this session's own
		// AskInteraction pushes it a tool_call/tool_query, so Read blocking
		// for well over 60s with no traffic is the expected healthy case, not
		// a dead-peer signal. ctx here is the HTTP request's own context
		// (canceled when the underlying connection actually closes); dead-peer
		// detection is startPingLoop's job via Ping's own built-in
		// wait-for-pong-and-error behavior, same idiom shuttle's existing
		// handleWS (ws.go) already uses for its channel WS connections.
		_, raw, err := s.conn.Read(ctx)
		if err != nil {
			log.Printf("[clienttools-ws] session %s closed: %v", s.id, err)
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			s.sendError("", "invalid envelope JSON")
			continue
		}

		s.handle(ctx, env)
	}
}

// startPingLoop keeps the connection alive and detects a dead peer:
// nhooyr.io/websocket's Ping blocks until the matching pong arrives (or its
// own ctx is done) and returns an error otherwise — see conn.go's Ping doc
// comment — so, unlike agent's gorilla/websocket version (which needs an
// explicit SetPongHandler resetting a manual read deadline), this loop is
// what ends the session on a dead connection: run's Read call has no
// timeout of its own (see run's comment above for why that was wrong the
// first time), so on a genuinely dead peer nothing would ever unblock it —
// a failed Ping here calls s.conn.CloseNow() to force that Read to return an
// error, rather than just quietly stopping this goroutine and leaving run
// blocked indefinitely.
func (s *clientToolsSession) startPingLoop(ctx context.Context) func() {
	ticker := time.NewTicker(clientToolsPingInterval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(ctx, clientToolsWriteTimeout)
				err := s.conn.Ping(pingCtx)
				cancel()
				if err != nil {
					_ = s.conn.CloseNow()
					return
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

func (s *clientToolsSession) handle(ctx context.Context, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeHello:
		s.handleHello(env)
	case protocol.TypePrompt:
		// Dispatched onto its own goroutine, unlike every other case here —
		// this is the one handler that can legitimately take a long time
		// (analyzer.Prompt blocks for the whole inference turn, up to
		// clientToolsTimeout). run's read loop must stay free to call
		// conn.Read again while a prompt is in flight, specifically so it
		// can read a tool_result answering a tool_query that prompt's own
		// inference call is blocked waiting on (see AskInteraction) —
		// otherwise the read loop can never read the answer to a question
		// the in-flight prompt is itself asking, deadlocking (in practice:
		// stalling) until AskInteraction's own clientToolsInteractionTimeout
		// gives up and unblocks Prompt from the other side. Every other
		// message type here is fast/non-blocking already (map/field writes,
		// or itself just a channel send in handleToolResult's case), so
		// keeping them synchronous in the read loop is fine and preserves
		// their relative ordering — only prompt needed to be pulled out.
		// Exact same reasoning as agent's ws/session.go handle(), which this
		// is a direct port of.
		go s.handlePrompt(ctx, env)
	case protocol.TypeToolResult:
		s.handleToolResult(env)
	default:
		s.sendError(env.RequestID, "unknown message type: "+string(env.Type))
	}
}

func (s *clientToolsSession) handleHello(env protocol.Envelope) {
	var p protocol.HelloPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		s.sendError(env.RequestID, "invalid hello payload")
		return
	}

	app, ok := s.registry.Get(p.AppID)
	if !ok {
		s.sendError(env.RequestID, "unknown appId: "+p.AppID)
		return
	}

	s.mu.Lock()
	s.app = app
	s.mu.Unlock()

	names := make([]string, 0, len(app.Tools))
	for _, t := range app.Tools {
		names = append(names, t.Name)
	}

	s.send(protocol.TypeAck, env.RequestID, protocol.AckPayload{
		SessionID: s.id,
		ToolNames: names,
	})
}

func (s *clientToolsSession) handlePrompt(_ context.Context, env protocol.Envelope) {
	var p protocol.PromptPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		s.sendError(env.RequestID, "invalid prompt payload")
		return
	}
	text, err := s.runPrompt(p.Text)
	if err != nil {
		s.sendError(env.RequestID, "inference error: "+err.Error())
		return
	}
	// Always send something back, even when the turn produced no closing
	// remark (a turn that's 100% tool calls, or — found during this POC's
	// own end-to-end testing — a turn where google/gemma-4-12b-it just
	// answered with nothing at all for some phrasings) — see
	// llm.ClientToolsAnalyzer.Prompt's doc comment: "" is a valid, non-error
	// return, not a sign anything hung. Without this fallback, a browser tab
	// (or a test harness, which is exactly how this gap was originally
	// found) waiting on *some* reply to this requestId has no way to tell
	// "the turn genuinely finished with nothing to say" apart from "still
	// running" — it would just sit there indefinitely with no feedback.
	if text == "" {
		text = "(此輪沒有文字回覆——可能是純工具呼叫,或這次沒有觸發任何動作。)"
	}
	s.send(protocol.TypeAssistantMessage, env.RequestID, protocol.AssistantMessagePayload{Text: text})
}

// runPrompt is handlePrompt's actual work, factored out so
// handleTestPrompt (clienttools_testtrigger.go) can drive the exact same
// path from an HTTP request instead of a WS "prompt" envelope — no synthetic
// envelope construction needed, no protocol-layer indirection for what is
// purely a test convenience. Requires hello to have already run (s.app set)
// — the same precondition a WS-originated prompt has.
func (s *clientToolsSession) runPrompt(text string) (string, error) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()

	if app == nil {
		return "", fmt.Errorf("no hello received yet on this session; the page must connect before /internal/clienttools/test-prompt can be used")
	}

	return s.analyzer.Prompt(s.id, text)
}

func (s *clientToolsSession) handleToolResult(env protocol.Envelope) {
	var p protocol.ToolResultPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		s.sendError(env.RequestID, "invalid tool_result payload")
		return
	}

	s.mu.Lock()
	ch, ok := s.pendingCalls[env.RequestID]
	if ok {
		delete(s.pendingCalls, env.RequestID)
	}
	s.mu.Unlock()

	if ok {
		ch <- p
		close(ch)
		return
	}

	log.Printf("[clienttools-ws] tool_result with no pending caller: session=%s tool=%s ok=%v", s.id, p.ToolName, p.OK)
}

// AskInteraction implements clienttools.InteractionAsker: sends
// toolName/args to the browser as a tool_call or tool_query and blocks until
// it answers with a matching tool_result (handleToolResult delivers it onto
// the channel this registers in s.pendingCalls — the same map/mechanism a
// regular tool_call's eventual tool_result would use; the two are
// distinguished only by which message type the client originally received,
// not by anything server-side), the request times out, or the connection is
// otherwise gone.
//
// Runs on whatever goroutine want's dispatch called the tool's Call from —
// never this session's own read loop — so it must not touch s.pendingCalls
// or s.app without s.mu, same as every other clientToolsSession method
// reachable from outside run's single-goroutine loop. Direct port of
// agent's ws/session.go Session.AskInteraction.
func (s *clientToolsSession) AskInteraction(toolName string, args json.RawMessage, kind toolschema.ToolKind) (json.RawMessage, error) {
	requestID := "req_" + newID()
	ch := make(chan protocol.ToolResultPayload, 1)

	s.mu.Lock()
	s.pendingCalls[requestID] = ch
	s.mu.Unlock()

	msgType := protocol.TypeToolQuery
	if kind == toolschema.ToolKindAction {
		msgType = protocol.TypeToolCall
	}
	s.send(msgType, requestID, protocol.ToolCallPayload{
		ToolName: toolName,
		Args:     args,
	})

	select {
	case result := <-ch:
		if !result.OK {
			if result.Error != "" {
				return nil, fmt.Errorf("page reported an error answering %q: %s", toolName, result.Error)
			}
			return nil, fmt.Errorf("page reported failure answering %q", toolName)
		}
		return result.Result, nil
	case <-time.After(clientToolsInteractionTimeout):
		s.mu.Lock()
		delete(s.pendingCalls, requestID)
		s.mu.Unlock()
		return nil, fmt.Errorf("page didn't answer %q within %s", toolName, clientToolsInteractionTimeout)
	}
}

func (s *clientToolsSession) send(typ protocol.MessageType, requestID string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[clienttools-ws] marshal outgoing payload: %v", err)
		return
	}
	env := protocol.Envelope{Type: typ, RequestID: requestID, Payload: data}
	out, err := json.Marshal(env)
	if err != nil {
		log.Printf("[clienttools-ws] marshal envelope: %v", err)
		return
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	writeCtx, cancel := context.WithTimeout(context.Background(), clientToolsWriteTimeout)
	defer cancel()
	if err := s.conn.Write(writeCtx, websocket.MessageText, out); err != nil {
		log.Printf("[clienttools-ws] write failed: session=%s err=%v", s.id, err)
	}
}

func (s *clientToolsSession) sendError(requestID, message string) {
	s.send(protocol.TypeError, requestID, protocol.ErrorPayload{Message: message})
}
