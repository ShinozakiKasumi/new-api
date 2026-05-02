package openaicompat

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func ResponsesToClaudeResponse(resp *dto.OpenAIResponsesResponse, id string) (*dto.ClaudeResponse, *dto.Usage, error) {
	if resp == nil {
		return nil, nil, nil
	}

	claudeResp := &dto.ClaudeResponse{
		Id:   id,
		Type: "message",
		Role: "assistant",
	}

	if resp.Model != "" {
		claudeResp.Model = resp.Model
	}

	var contents []dto.ClaudeMediaMessage
	stopReason := "end_turn"

	for _, out := range resp.Output {
		switch out.Type {
		case "message":
			for _, c := range out.Content {
				if c.Type == "output_text" && c.Text != "" {
					contents = append(contents, dto.ClaudeMediaMessage{
						Type: "text",
						Text: &c.Text,
					})
				}
			}

		case "function_call":
			name := strings.TrimSpace(out.Name)
			callID := strings.TrimSpace(out.CallId)
			if callID == "" {
				callID = strings.TrimSpace(out.ID)
			}

			var inputMap any
			argsStr := out.ArgumentsString()
			if argsStr != "" {
				var parsed map[string]any
				if err := common.Unmarshal([]byte(argsStr), &parsed); err == nil {
					inputMap = parsed
				} else {
					inputMap = argsStr
				}
			} else {
				inputMap = map[string]any{}
			}

			contents = append(contents, dto.ClaudeMediaMessage{
				Type:  "tool_use",
				Id:    callID,
				Name:  name,
				Input: inputMap,
			})
			stopReason = "tool_use"
		}
	}

	if len(contents) == 0 {
		text := ExtractOutputTextFromResponses(resp)
		if text != "" {
			contents = append(contents, dto.ClaudeMediaMessage{
				Type: "text",
				Text: &text,
			})
		}
	}

	claudeResp.Content = contents
	claudeResp.StopReason = responsesStatusToClaudeStopReason(resp, stopReason)

	// Usage
	usage := &dto.Usage{}
	claudeUsage := &dto.ClaudeUsage{}
	if resp.Usage != nil {
		claudeUsage.InputTokens = resp.Usage.InputTokens
		claudeUsage.OutputTokens = resp.Usage.OutputTokens
		usage.PromptTokens = resp.Usage.InputTokens
		usage.InputTokens = resp.Usage.InputTokens
		usage.CompletionTokens = resp.Usage.OutputTokens
		usage.OutputTokens = resp.Usage.OutputTokens
		if resp.Usage.TotalTokens != 0 {
			usage.TotalTokens = resp.Usage.TotalTokens
		} else {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		if resp.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = resp.Usage.InputTokensDetails.CachedTokens
			claudeUsage.CacheReadInputTokens = resp.Usage.InputTokensDetails.CachedTokens
		}
	}
	claudeResp.Usage = claudeUsage

	return claudeResp, usage, nil
}

func responsesStatusToClaudeStopReason(resp *dto.OpenAIResponsesResponse, fallback string) string {
	if resp == nil {
		return fallback
	}

	var status string
	if len(resp.Status) > 0 {
		_ = common.Unmarshal(resp.Status, &status)
	}

	switch strings.ToLower(status) {
	case "completed":
		return fallback
	case "incomplete":
		return "max_tokens"
	case "failed", "cancelled":
		return "end_turn"
	}

	return fallback
}
