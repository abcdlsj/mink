package llm

import (
	"encoding/json"

	"github.com/abcdlsj/mink/msg"
	"github.com/anthropics/anthropic-sdk-go"
)

func (p *anthropicProvider) buildRequest(msgs []msg.Message, tools []Tool) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(p.cfg.MaxTokens),
	}
	for _, m := range msgs {
		if m.Role == "system" {
			params.System = []anthropic.TextBlockParam{{
				Type: "text",
				Text: m.Content,
			}}
			continue
		}
		if blocks := anthropicMessageBlocks(m); len(blocks) > 0 {
			params.Messages = append(params.Messages, anthropicMessage(m.Role, blocks))
		}
	}
	if ts := anthropicTools(tools); len(ts) > 0 {
		params.Tools = ts
	}
	return params
}

func anthropicMessage(role string, blocks []anthropic.ContentBlockParamUnion) anthropic.MessageParam {
	if role == "assistant" {
		return anthropic.NewAssistantMessage(blocks...)
	}
	return anthropic.NewUserMessage(blocks...)
}

func anthropicMessageBlocks(m msg.Message) []anthropic.ContentBlockParamUnion {
	switch m.Role {
	case "user":
		return anthropicUserBlocks(m)
	case "assistant":
		return anthropicAssistantBlocks(m)
	case "tool":
		return anthropicToolResultBlocks(m.ToolResults, false)
	default:
		return nil
	}
}

func anthropicUserBlocks(m msg.Message) []anthropic.ContentBlockParamUnion {
	blocks := anthropicToolResultBlocks(m.ToolResults, true)
	if m.Content != "" {
		blocks = append([]anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)}, blocks...)
	}
	if len(blocks) == 0 {
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("(empty)")}
	}
	return blocks
}

func anthropicAssistantBlocks(m msg.Message) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	if m.ReasoningSignature != "" && m.Reasoning != "" {
		blocks = append(blocks, anthropic.NewThinkingBlock(m.ReasoningSignature, m.Reasoning))
	}
	if m.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	for _, tc := range m.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal(tc.Args, &args)
		blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, tc.Name))
	}
	return blocks
}

func anthropicToolResultBlocks(results []msg.ToolResult, emptyOK bool) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(results))
	for _, tr := range results {
		content := tr.Content
		if content == "" {
			content = "(no output)"
		}
		blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, content, false))
	}
	if !emptyOK && len(blocks) == 0 {
		return nil
	}
	return blocks
}

func anthropicTools(tools []Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schemaBytes, _ := json.Marshal(t.Function.Parameters)
		var inputSchema anthropic.ToolInputSchemaParam
		_ = json.Unmarshal(schemaBytes, &inputSchema)
		toolParam := anthropic.ToolParam{
			Name:        t.Function.Name,
			Description: anthropic.String(t.Function.Description),
			InputSchema: inputSchema,
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &toolParam})
	}
	return out
}

func parseAnthropicResponse(resp *anthropic.Message) *Response {
	var res Response
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.ThinkingBlock:
			res.Reasoning = b.Thinking
			res.ReasoningSignature = b.Signature
		case anthropic.TextBlock:
			res.Content += b.Text
		case anthropic.ToolUseBlock:
			argsJSON, _ := json.Marshal(b.Input)
			if b.Name == "" || len(argsJSON) == 0 {
				continue
			}
			res.ToolCalls = append(res.ToolCalls, msg.ToolCall{
				ID:   b.ID,
				Name: b.Name,
				Args: argsJSON,
			})
		}
	}
	res.Usage = toAnthropicTokenUsage(resp.Usage)
	return &res
}
