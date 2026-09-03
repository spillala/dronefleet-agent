// Package agent implements Agent 1's reasoning loop: an LLM with access to
// dronefleet-mcp's read/diagnostic tools, asked to find and explain faults
// in the fleet without attempting to fix them.
package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/spillala/dronefleet-agent/internal/llm"
	"github.com/spillala/dronefleet-agent/internal/mcpclient"
)

const maxIterations = 8

// diagnosticTools is the allow-list of MCP tools exposed to the model.
// recover_drone is deliberately excluded: remediation is Agent 2's job
// (Phase 4), not Agent 1's.
var diagnosticTools = map[string]bool{
	"get_fleet_status": true,
	"get_drone":        true,
	"list_missions":    true,
	"get_mission":      true,
	"simulate_fault":   true, // kept for demoing the loop end-to-end
}

const systemPrompt = `You are Agent 1 for DroneFleet, a simulated drone fleet.
Your job is fault detection and diagnosis only: use the available tools to
find drones in a fault state, gather enough detail to explain the likely
cause and severity, and report findings clearly.

You do not have a tool to recover or remediate a drone, and you must not
claim to have fixed anything. Remediation is a separate agent's job. If you
believe a drone needs recovery, say so in your findings and stop there.

When you are done investigating, reply with plain text (no further tool
calls): a short summary of fleet health, then one line per faulted drone
with your diagnosis.`

// Agent runs the diagnose loop against a connected MCP session and LLM.
type Agent struct {
	mcp   *mcpclient.Client
	llm   *llm.Client
	model string
}

// New builds an Agent from an already-connected MCP client and an Ollama
// (or any Ollama-API-compatible) LLM client.
func New(mcp *mcpclient.Client, llmClient *llm.Client, model string) *Agent {
	return &Agent{mcp: mcp, llm: llmClient, model: model}
}

// Diagnose runs one investigation pass and returns the model's final
// findings as plain text.
func (a *Agent) Diagnose(ctx context.Context, instruction string) (string, error) {
	tools, err := a.tools(ctx)
	if err != nil {
		return "", err
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: instruction},
	}

	for i := 0; i < maxIterations; i++ {
		resp, err := a.llm.Chat(ctx, llm.ChatRequest{
			Model:    a.model,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return "", fmt.Errorf("chat turn %d: %w", i, err)
		}
		messages = append(messages, resp.Message)

		if len(resp.Message.ToolCalls) == 0 {
			return resp.Message.Content, nil
		}

		for _, call := range resp.Message.ToolCalls {
			log.Printf("[agent] calling tool %s(%v)", call.Function.Name, call.Function.Arguments)
			result, callErr := a.mcp.CallTool(ctx, call.Function.Name, call.Function.Arguments)
			if callErr != nil {
				result = fmt.Sprintf("error: %v", callErr)
			}
			messages = append(messages, llm.Message{
				Role:     "tool",
				ToolName: call.Function.Name,
				Content:  result,
			})
		}
	}

	return "", fmt.Errorf("gave up after %d tool-calling turns without a final answer", maxIterations)
}

// tools fetches the live MCP tool list and translates it to the
// Ollama/OpenAI tool-schema shape, restricted to diagnosticTools.
func (a *Agent) tools(ctx context.Context) ([]llm.Tool, error) {
	mcpTools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	var tools []llm.Tool
	for _, t := range mcpTools {
		if !diagnosticTools[t.Name] {
			continue
		}
		tools = append(tools, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return tools, nil
}
