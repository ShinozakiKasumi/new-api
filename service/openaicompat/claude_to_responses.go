package openaicompat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func ClaudeToResponsesRequest(req *dto.ClaudeRequest) (*dto.OpenAIResponsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("claude request is nil")
	}

	// Instructions from system prompt
	var instructionsRaw json.RawMessage
	if req.System != nil {
		if s, ok := req.System.(string); ok {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				instructionsRaw, _ = common.Marshal(trimmed)
			}
		} else {
			systemParts := req.ParseSystem()
			var parts []string
			for _, p := range systemParts {
				if p.Type == "text" && p.Text != nil && strings.TrimSpace(*p.Text) != "" {
					parts = append(parts, *p.Text)
				}
			}
			if len(parts) > 0 {
				instructionsRaw, _ = common.Marshal(strings.Join(parts, "\n\n"))
			}
		}
	}

	// Convert messages to Responses input items
	inputItems := claudeMessagesToResponsesInput(req.Messages)
	inputRaw, err := common.Marshal(inputItems)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input items: %w", err)
	}

	// Convert tools
	toolsRaw := claudeToolsToResponsesTools(req.Tools)

	// Convert tool_choice
	toolChoiceRaw := claudeToolChoiceToResponsesToolChoice(req.ToolChoice)

	// Convert thinking to reasoning
	var reasoning *dto.Reasoning
	if req.Thinking != nil {
		reasoning = claudeThinkingToResponsesReasoning(req.Thinking)
	}

	out := &dto.OpenAIResponsesRequest{
		Model:        req.Model,
		Input:        inputRaw,
		Instructions: instructionsRaw,
		Stream:       req.Stream,
		Temperature:  req.Temperature,
		TopP:         req.TopP,
		Tools:        toolsRaw,
		ToolChoice:   toolChoiceRaw,
		Reasoning:    reasoning,
		Metadata:     req.Metadata,
	}

	if req.MaxTokens != nil {
		out.MaxOutputTokens = req.MaxTokens
	}

	return out, nil
}

func claudeMessagesToResponsesInput(messages []dto.ClaudeMessage) []map[string]any {
	items := make([]map[string]any, 0, len(messages))

	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			continue
		}

		if msg.IsStringContent() {
			s := msg.GetStringContent()
			if s == "" {
				continue
			}
			item := map[string]any{"role": role, "content": s}
			items = append(items, item)
			continue
		}

		parts, err := msg.ParseContent()
		if err != nil || len(parts) == 0 {
			continue
		}

		var contentParts []map[string]any
		for _, part := range parts {
			switch part.Type {
			case "text":
				text := ""
				if part.Text != nil {
					text = *part.Text
				}
				if text == "" {
					continue
				}
				if role == "assistant" {
					contentParts = append(contentParts, map[string]any{
						"type": "output_text",
						"text": text,
					})
				} else {
					contentParts = append(contentParts, map[string]any{
						"type": "input_text",
						"text": text,
					})
				}

			case "image":
				if part.Source == nil {
					continue
				}
				imageURL := part.Source.Url
				if imageURL == "" {
					data := common.Interface2String(part.Source.Data)
					if data != "" && part.Source.MediaType != "" {
						imageURL = fmt.Sprintf("data:%s;base64,%s", part.Source.MediaType, data)
					}
				}
				if imageURL != "" {
					contentParts = append(contentParts, map[string]any{
						"type":      "input_image",
						"image_url": imageURL,
					})
				}

			case "tool_use":
				// Assistant's tool call — emit as a separate function_call input item
				arguments := "{}"
				if part.Input != nil {
					if b, err := common.Marshal(part.Input); err == nil {
						arguments = string(b)
					}
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   part.Id,
					"name":      part.Name,
					"arguments": arguments,
				})
				continue

			case "tool_result":
				// User's tool result — emit as function_call_output
				output := ""
				if part.Content != nil {
					if s, ok := part.Content.(string); ok {
						output = s
					} else if b, err := common.Marshal(part.Content); err == nil {
						output = string(b)
					}
				}
				callID := strings.TrimSpace(part.ToolUseId)
				if callID != "" {
					items = append(items, map[string]any{
						"type":    "function_call_output",
						"call_id": callID,
						"output":  output,
					})
				}
				continue

			case "document":
				// Best effort: convert document to input_file
				if part.Source != nil {
					data := common.Interface2String(part.Source.Data)
					if data != "" {
						fileData := map[string]any{
							"filename": "document",
						}
						if part.Source.MediaType != "" {
							fileData["mime_type"] = part.Source.MediaType
						}
						fileData["data"] = data
						contentParts = append(contentParts, map[string]any{
							"type": "input_file",
							"file": fileData,
						})
					}
				}
				continue
			}
		}

		if len(contentParts) > 0 {
			items = append(items, map[string]any{
				"role":    role,
				"content": contentParts,
			})
		}
	}

	return items
}

func claudeToolsToResponsesTools(tools any) json.RawMessage {
	if tools == nil {
		return nil
	}

	toolList, ok := tools.([]any)
	if !ok {
		return nil
	}

	var result []map[string]any
	for _, tool := range toolList {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			continue
		}

		name, _ := toolMap["name"].(string)
		if name == "" {
			continue
		}

		item := map[string]any{
			"type": "function",
			"name": name,
		}
		if desc, ok := toolMap["description"].(string); ok && desc != "" {
			item["description"] = desc
		}
		if schema, ok := toolMap["input_schema"]; ok {
			item["parameters"] = schema
		}
		result = append(result, item)
	}

	if len(result) == 0 {
		return nil
	}
	b, _ := common.Marshal(result)
	return b
}

func claudeToolChoiceToResponsesToolChoice(toolChoice any) json.RawMessage {
	if toolChoice == nil {
		return nil
	}

	claudeToResponsesMap := map[string]string{
		"auto": "auto",
		"any":  "required",
		"none": "none",
	}

	resolveType := func(t string) (string, bool) {
		if mapped, ok := claudeToResponsesMap[t]; ok {
			return mapped, true
		}
		return "", false
	}

	switch tc := toolChoice.(type) {
	case string:
		if mapped, ok := resolveType(tc); ok {
			b, _ := common.Marshal(mapped)
			return b
		}
		b, _ := common.Marshal("auto")
		return b
	case map[string]any:
		tcType, _ := tc["type"].(string)
		if mapped, ok := resolveType(tcType); ok {
			b, _ := common.Marshal(mapped)
			return b
		}
		if tcType == "tool" {
			if name, _ := tc["name"].(string); name != "" {
				b, _ := common.Marshal(map[string]any{"type": "function", "name": name})
				return b
			}
		}
	}

	b, _ := common.Marshal(toolChoice)
	return b
}

func claudeThinkingToResponsesReasoning(thinking *dto.Thinking) *dto.Reasoning {
	if thinking == nil {
		return nil
	}

	switch thinking.Type {
	case "enabled":
		effort := "medium"
		if thinking.BudgetTokens != nil {
			switch {
			case *thinking.BudgetTokens <= 1280:
				effort = "low"
			case *thinking.BudgetTokens <= 2048:
				effort = "medium"
			default:
				effort = "high"
			}
		}
		return &dto.Reasoning{
			Effort:  effort,
			Summary: "detailed",
		}
	case "adaptive":
		return &dto.Reasoning{
			Effort:  "high",
			Summary: "detailed",
		}
	}

	return nil
}
