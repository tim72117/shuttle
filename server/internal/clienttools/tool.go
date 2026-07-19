package clienttools

import (
	"encoding/json"
	"fmt"

	"github.com/tim72117/tripace/internal/toolschema"
	"github.com/tim72117/want/types"
)

// RegisterApp registers every tool in app into want's global tool registry
// (types.RegisterTool), choosing forwardingTool or queryTool per
// toolschema.ToolKind — mirrors agent's registerForwardingTool
// (backend/internal/inference/agent_roles.go), minus the per-app agent-role
// bookkeeping that lives in agent_role.go here instead (this POC has exactly
// one app, so there's no RegisterPlatformTools-style loop over many apps).
func RegisterApp(app *toolschema.App) []string {
	names := make([]string, 0, len(app.Tools))
	for _, t := range app.Tools {
		names = append(names, t.Name)
		registerOne(t)
	}
	return names
}

func registerOne(t toolschema.Tool) {
	decl := types.ToolDeclaration{
		Name:        t.Name,
		Description: t.Description,
		Type:        "sync",
		Parameters:  parameterSchemaToWant(t.Parameters),
	}
	if t.Kind == toolschema.ToolKindQuery {
		types.RegisterTool(decl, func() types.ToolInterface {
			return &queryTool{name: t.Name}
		})
		return
	}
	types.RegisterTool(decl, func() types.ToolInterface {
		return &forwardingTool{name: t.Name}
	})
}

// forwardingTool is the toolschema.ToolKindAction behavior: blocks until the
// page actually executes the call and reports back (via askPage), but only
// the success/failure of that report ever reaches the LLM — never the
// page's actual returned data. Mirrors agent's forwardingTool
// (backend/internal/inference/agent_roles.go) exactly, with sessionEnvs
// (ctx.GetSessionEnvs()) replacing ctx.GetAgentID() as askPage's session key
// — see interaction.go's InteractionAsker doc comment for why.
type forwardingTool struct {
	types.BaseToolConfig
	name string
}

func (f *forwardingTool) ValidateInput(types.ToolArguments, types.ToolContext) error { return nil }

func (f *forwardingTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal args for %s: %w", f.name, err)
	}

	if _, err := askPage(ctx.GetSessionEnvs(), f.name, raw, toolschema.ToolKindAction); err != nil {
		return nil, fmt.Errorf("execute %s: %w", f.name, err)
	}

	msg := fmt.Sprintf("%q executed successfully.", f.name)
	ctx.EmitToolResult(map[string]interface{}{"message": msg})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (f *forwardingTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Calling %s", f.name)
}

func (f *forwardingTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to call %s: %v", f.name, err)
}

func (f *forwardingTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Executed on the page"
}

// queryTool is the toolschema.ToolKindQuery counterpart to forwardingTool:
// both block until the page answers (via askPage), but where forwardingTool
// only surfaces success/failure to the LLM, queryTool feeds the page's
// actual answer data back into its reasoning (e.g. a newly-added trip
// entry's id, or the full current list) — see toolschema.Tool.Kind's doc
// comment. Mirrors agent's queryTool (backend/internal/inference/agent_roles.go).
type queryTool struct {
	types.BaseToolConfig
	name string
}

func (q *queryTool) ValidateInput(types.ToolArguments, types.ToolContext) error { return nil }

func (q *queryTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal args for %s: %w", q.name, err)
	}

	answerJSON, err := askPage(ctx.GetSessionEnvs(), q.name, raw, toolschema.ToolKindQuery)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", q.name, err)
	}

	var answer interface{}
	if err := json.Unmarshal(answerJSON, &answer); err != nil {
		answer = string(answerJSON) // not JSON — surface it as-is rather than failing the whole call
	}
	ctx.EmitToolResult(map[string]interface{}{"answer": answer})
	return []types.ResultContentBlock{types.TextBlock(string(answerJSON))}, nil
}

func (q *queryTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Asking the page: %s", q.name)
}

func (q *queryTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to query %s: %v", q.name, err)
}

func (q *queryTool) RenderToolResult(data map[string]interface{}) string {
	return fmt.Sprintf("Page answered %s", q.name)
}

// parameterSchemaToWant converts our JSON-Schema subset into the
// map[string]interface{} shape want's ToolDeclaration.Parameters expects.
// Directly ported from agent's parameterSchemaToWant
// (backend/internal/inference/agent_roles.go) — including the vLLM
// workaround documented below, since shuttle's LLM provider is the same
// self-hosted google/gemma-4-12b-it via vLLM (see server/.env's
// AI_PROVIDER/AI_MODEL) that originally surfaced this bug.
//
// Lower-case JSON-Schema type strings ("object", "string", ...), same as
// toolschema.ParameterSchema.Type and same as agent's own version — even
// though shuttle's other hand-written want tools (e.g.
// server/internal/wanttools/recommend_nearby.go) happen to use upper-case
// ("OBJECT", "STRING") by their author's own convention. Checked want's
// vLLM provider (internal/provider/vllm.go): declaration.Parameters is
// passed straight through into the OpenAI-compatible tools payload with no
// case transformation either way, so nothing downstream requires matching
// shuttle's existing convention — lower-case is the actual JSON-Schema
// spec, and matches the exact schema shape already proven against this
// model in the agent project.
func parameterSchemaToWant(p toolschema.ParameterSchema) map[string]interface{} {
	out := map[string]interface{}{
		"type": p.Type,
	}
	if p.Description != "" {
		out["description"] = p.Description
	}
	// Always emit "properties" for an object type, even when there are none
	// (e.g. trip_entry_list, a ToolKindQuery tool with zero parameters) —
	// omitting the key entirely for an empty map produces
	// {"type":"OBJECT"} with no properties, which is valid JSON Schema but
	// confused google/gemma-4-12b-it (via vLLM) into returning an
	// unparseable null response instead of calling the tool with {}. An
	// explicit {} is unambiguous either way. See agent's identical comment
	// on parameterSchemaToWant for the original discovery of this.
	if p.Type == "object" {
		props := make(map[string]interface{}, len(p.Properties))
		for name, sub := range p.Properties {
			if sub == nil {
				continue
			}
			props[name] = parameterSchemaToWant(*sub)
		}
		out["properties"] = props
	}
	if p.Items != nil {
		out["items"] = parameterSchemaToWant(*p.Items)
	}
	if len(p.Required) > 0 {
		out["required"] = p.Required
	}
	if len(p.Enum) > 0 {
		out["enum"] = p.Enum
	}
	return out
}
