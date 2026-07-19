package llm

// ClientToolsAnalyzer is this POC's dedicated want wiring for the
// "LLM calls a frontend tool" experiment (server/internal/clienttools +
// web/src/DebugApp.tsx). It is deliberately independent of WantAnalyzer/
// WantPool (want_analyzer.go/want_pool.go), which back the real assistant
// conversation flow (server/internal/llm/assistant_agent.go's Tools
// whitelist: entry_add, entry_query, ...): mixing this POC's
// trip_entry_add/delete/update/list tools into that whitelist — or reusing
// its shared orchestrator/mutex — would let a stray WS message from this
// debug harness disturb real channel conversations, which the task this
// file exists for explicitly calls out to avoid ("避免正式對話流程被這次
// 試做污染").
//
// A second *wantorch.Orchestrator instance (not a second call to
// wantorch.SetupWith/orchestrator.Setup) is what actually keeps these
// worlds apart: want's own LLM client initialization
// (orchestrator.InitializeWithConfig -> internal.Initialize) sets a
// package-level GlobalEngine, so calling SetupWith a second time in the
// same process would be redundant re-initialization against the same
// already-configured provider, not real isolation — the isolation this POC
// actually needs comes from Orchestrator.Role scoping which agent role
// (and therefore which Tools whitelist — see clienttoolsRole below) a given
// orchestrator's dispatch loop resolves per call (see want's
// orchestrator.go Start()'s toolUseContext := internal.LoadToolUseContext
// (agentID, orch.Role, ...) and internal/loader.go's AgentLoader.GetAgent).
// wantorch.NewOrchestrator(role) + orch.Start() does exactly that without
// re-running provider init.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tim72117/tripace/internal/clienttools"
	"github.com/tim72117/tripace/internal/toolschema"

	wantorch "github.com/tim72117/want/orchestrator"
	"github.com/tim72117/want/pkg/agentreg"
	wantui "github.com/tim72117/want/ui"
)

// clienttoolsRole is this POC's want agent role name — distinct from
// "assistant" (assistant_agent.go), so its Tools whitelist only ever
// contains this app's own trip_entry_* tools. Mirrors agent's
// agentRoleFor(appID) (backend/internal/inference/agent_roles.go), simplified
// to a fixed constant since this POC has exactly one app
// (server/tools/clienttools.yaml's appId: clienttools), not a
// registry of many.
const clienttoolsRole = "clienttools"

// clientToolsThought is the fallback system prompt used if
// server/tools/clienttools.yaml has no `thought:` field set. In practice the
// YAML always sets one (see that file) — this only matters if someone
// strips it.
const clientToolsThought = "You are a tool-selection assistant embedded in a web page. " +
	"The user is talking to the page, not to you directly. When their " +
	"message calls for an action the page can perform, call the single " +
	"matching tool with well-formed arguments; the page executes it, " +
	"not you. If nothing needs doing, just reply in plain text. Never " +
	"ask the user to wait or claim you performed an action yourself — " +
	"the tool call itself is the action."

// ClientToolsAnalyzer wraps a dedicated *wantorch.Orchestrator scoped to
// clienttoolsRole. One instance serves this POC's WS session (see
// server/internal/api/clienttools_ws.go) — Prompt below is called once per
// user-typed sentence, serialized by mu exactly like WantAnalyzer.generate
// (only one inference turn in flight at a time; a second call while one is
// running waits its turn rather than interleaving).
type ClientToolsAnalyzer struct {
	orch *wantorch.Orchestrator
	mu   sync.Mutex
}

// NewClientToolsAnalyzer registers app's tools (via clienttools.RegisterApp)
// and this POC's agent role into want's global registries, then builds and
// starts a dedicated orchestrator for it. Must be called after want's
// provider has already been initialized once (see NewWant/NewWantPool in
// want_analyzer.go/want_pool.go, called first in cmd/server/main.go) — this
// constructor does NOT call wantorch.SetupWith/orchestrator.Setup, so it
// relies on that having already happened; calling it before any WantAnalyzer
// exists would panic inside want's dispatch loop the first time a prompt is
// submitted (GlobalEngine still nil).
func NewClientToolsAnalyzer(app *toolschema.App) *ClientToolsAnalyzer {
	toolNames := clienttools.RegisterApp(app)

	thought := app.Thought
	if thought == "" {
		thought = clientToolsThought
	}

	agentreg.Register(agentreg.DefaultLoader(), clienttoolsRole, &agentreg.AgentDefinition{
		Role:      clienttoolsRole,
		Tools:     toolNames,
		WhenToUse: "Selects and fills arguments for tools that the client-tools debug page has declared; it never executes them itself.",
		Thought:   thought,
		// Same "replace want's default prompt assembly entirely" approach as
		// assistant_agent.go and agent's RegisterAppRole: the final system
		// prompt sent to the LLM is exactly app.Thought (or
		// clientToolsThought), nothing else appended/prepended.
		PromptBuilder: agentreg.PromptBuilderFunc(func(a *agentreg.Agent, c *agentreg.ToolUseContext) string {
			return a.SystemPrompt
		}),
	})

	orch := wantorch.NewOrchestrator(clienttoolsRole)
	orch.OnError(func(err error) {
		fmt.Printf("[clienttools] 🔴 Agent Error: %v\n", err)
	})
	orch.Start()

	return &ClientToolsAnalyzer{orch: orch}
}

// Prompt submits text as this POC's single agent's next user turn, tagging
// the call with sessionID via SetSessionEnvs so clienttools.askPage (called
// from inside a trip_entry_* tool's Call, running on want's own dispatch
// goroutine — see clienttools/tool.go) can find its way back to the right
// WS session's pendingCalls (see clienttools.RegisterAsker /
// server/internal/api/clienttools_ws.go). Blocks until the whole turn (all
// tool calls the LLM made this round, and their round trips to the browser)
// has settled or clientToolsTimeout elapses — same idle-detection pattern
// WantAnalyzer.generate/Assist use (want ui.HandleInferenceMessage +
// StatusViewModel{Status:"idle"}), same 1.5s settle window to let a
// just-finished tool call's trailing text event land before declaring done.
//
// Returns the assistant's plain-text reply for this turn, if any. "" is a
// valid, non-error return — not a sign anything hung or failed — for either
// of two cases found during this POC's own end-to-end testing: a turn
// that's 100% tool calls with no closing remark, or (seen with
// google/gemma-4-12b-it specifically, for some phrasings — e.g. asking it
// to list the current trip entries in Chinese) the model producing a
// completely empty Experience.Content, no tool call and no text at all.
// Neither is a bug in this blocking mechanism: EventBus still delivers
// StatusViewModel{idle} right on schedule either way, so done still closes
// well under clientToolsTimeout — see handlePrompt (clienttools_ws.go),
// which papers over an empty reply with a fallback message rather than
// silently sending nothing back, precisely because "nothing came back" is
// indistinguishable from "still stuck" to whatever's waiting on the other
// end of the WS connection.
func (c *ClientToolsAnalyzer) Prompt(sessionID, text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.orch.SetSessionEnvs(map[string]string{"sessionID": sessionID})

	state := wantui.NewCommonInferenceState()
	var mu sync.Mutex
	var sb strings.Builder
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	unsub := c.orch.EventBus.Subscribe("agent.inference", func(payload interface{}) {
		mu.Lock()
		defer mu.Unlock()

		result, handled := wantui.HandleInferenceMessage(payload, state)
		if !handled || result == nil {
			return
		}
		switch vm := result.(type) {
		case wantui.TextViewModel:
			if vm.Content != "" {
				sb.WriteString(vm.Content)
			}
		case wantui.StatusViewModel:
			// idle 表示這輪推論(含期間所有工具呼叫)已結束;給文字事件一點
			// 到達窗口再結束,同 WantAnalyzer.generate/Assist 的做法。
			if vm.Status == "idle" {
				go func() { time.Sleep(1500 * time.Millisecond); finish() }()
			}
		}
	})
	defer unsub()

	c.orch.Submit(text)

	select {
	case <-done:
	case <-time.After(clientToolsTimeout):
		return "", fmt.Errorf("clienttools 推論逾時(%s)", clientToolsTimeout)
	}

	mu.Lock()
	defer mu.Unlock()
	return strings.TrimSpace(sb.String()), nil
}

// clientToolsTimeout bounds one Prompt() call end-to-end, including every
// tool_call/tool_query round trip to the browser it triggers along the way
// (each individual round trip is itself bounded by
// clienttools_ws.go's interactionTimeout, well under this). Matches
// WantAnalyzer's own completeTimeout (90s, see want_analyzer.go's Assist/
// Answer) — this POC has no reason to be more or less patient than the real
// assistant flow it's modeled on.
const clientToolsTimeout = 90 * time.Second
