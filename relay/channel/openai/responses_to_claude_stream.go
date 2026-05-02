package openai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesToClaudeStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	model := info.UpstreamModelName

	if info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		}
	}

	var (
		usage            = &dto.Usage{}
		outputText       strings.Builder
		sentMessageStart bool
		streamErr        *types.NewAPIError
	)

	// Track tool calls by item ID
	toolCallItemIDToCallID := make(map[string]string)
	toolCallItemIDToName := make(map[string]string)
	toolCallItemIDToArgs := make(map[string]string)
	toolCallItemIDStarted := make(map[string]bool)
	toolCallItemIDClosed := make(map[string]bool)
	// Track tool call order for correct index assignment
	toolCallOrder := make([]string, 0)
	toolCallItemIDToBlockIndex := make(map[string]int)

	claudeConvert := info.ClaudeConvertInfo

	stopOpenBlocks := func() {
		switch claudeConvert.LastMessagesType {
		case relaycommon.LastMessageTypeText, relaycommon.LastMessageTypeThinking:
			helper.ClaudeData(c, dto.ClaudeResponse{
				Type:  "content_block_stop",
				Index: common.GetPointer[int](claudeConvert.Index),
			})
		case relaycommon.LastMessageTypeTools:
			for _, itemID := range toolCallOrder {
				if toolCallItemIDClosed[itemID] {
					continue
				}
				blockIndex := toolCallItemIDToBlockIndex[itemID]
				helper.ClaudeData(c, dto.ClaudeResponse{
					Type:  "content_block_stop",
					Index: common.GetPointer[int](blockIndex),
				})
				toolCallItemIDClosed[itemID] = true
			}
		}
	}

	stopOpenBlocksAndAdvance := func() {
		if claudeConvert.LastMessagesType == relaycommon.LastMessageTypeNone {
			return
		}
		stopOpenBlocks()
		switch claudeConvert.LastMessagesType {
		case relaycommon.LastMessageTypeTools:
			maxIndex := claudeConvert.ToolCallBaseIndex
			for _, itemID := range toolCallOrder {
				if idx, ok := toolCallItemIDToBlockIndex[itemID]; ok && idx >= maxIndex {
					maxIndex = idx + 1
				}
			}
			claudeConvert.Index = maxIndex
			claudeConvert.ToolCallBaseIndex = 0
			claudeConvert.ToolCallMaxIndexOffset = 0
			toolCallOrder = nil
		default:
			claudeConvert.Index++
		}
		claudeConvert.LastMessagesType = relaycommon.LastMessageTypeNone
	}

	// finalizeStream closes all open blocks and emits message_delta + message_stop
	finalizeStream := func() {
		stopOpenBlocks()

		stopReason := "end_turn"
		hasUnclosedToolCalls := false
		for _, itemID := range toolCallOrder {
			if !toolCallItemIDClosed[itemID] {
				hasUnclosedToolCalls = true
				break
			}
		}
		if claudeConvert.LastMessagesType == relaycommon.LastMessageTypeTools || hasUnclosedToolCalls {
			stopReason = "tool_use"
		}

		claudeUsage := &dto.ClaudeUsage{
			InputTokens:  usage.PromptTokens,
			OutputTokens: usage.CompletionTokens,
		}
		if usage.PromptTokensDetails.CachedTokens > 0 {
			claudeUsage.CacheReadInputTokens = usage.PromptTokensDetails.CachedTokens
		}

		helper.ClaudeData(c, dto.ClaudeResponse{
			Type:  "message_delta",
			Usage: claudeUsage,
			Delta: &dto.ClaudeMediaMessage{
				StopReason: &stopReason,
			},
		})
		helper.ClaudeData(c, dto.ClaudeResponse{
			Type: "message_stop",
		})
		claudeConvert.Done = true
	}

	sendMessageStart := func() {
		if sentMessageStart {
			return
		}
		helper.ClaudeData(c, dto.ClaudeResponse{
			Type: "message_start",
			Message: &dto.ClaudeMediaMessage{
				Id:    responseId,
				Model: model,
				Type:  "message",
				Role:  "assistant",
				Usage: &dto.ClaudeUsage{
					InputTokens:  info.GetEstimatePromptTokens(),
					OutputTokens: 0,
				},
			},
		})
		sentMessageStart = true
	}

	startToolBlock := func(itemID, callID, name string) {
		if claudeConvert.LastMessagesType != relaycommon.LastMessageTypeTools {
			stopOpenBlocksAndAdvance()
			claudeConvert.ToolCallBaseIndex = claudeConvert.Index
			claudeConvert.ToolCallMaxIndexOffset = 0
			toolCallOrder = nil
		}
		claudeConvert.LastMessagesType = relaycommon.LastMessageTypeTools
		blockIndex := claudeConvert.ToolCallBaseIndex + len(toolCallOrder)
		claudeConvert.ToolCallMaxIndexOffset = len(toolCallOrder)
		toolCallOrder = append(toolCallOrder, itemID)
		toolCallItemIDToBlockIndex[itemID] = blockIndex
		toolCallItemIDStarted[itemID] = true

		idx := blockIndex
		helper.ClaudeData(c, dto.ClaudeResponse{
			Index: &idx,
			Type:  "content_block_start",
			ContentBlock: &dto.ClaudeMediaMessage{
				Id:    callID,
				Type:  "tool_use",
				Name:  name,
				Input: map[string]interface{}{},
			},
		})
	}

	emitToolArgsDelta := func(itemID, delta string) {
		blockIndex, ok := toolCallItemIDToBlockIndex[itemID]
		if !ok {
			return
		}
		idx := blockIndex
		helper.ClaudeData(c, dto.ClaudeResponse{
			Index: &idx,
			Type:  "content_block_delta",
			Delta: &dto.ClaudeMediaMessage{
				Type:        "input_json_delta",
				PartialJson: &delta,
			},
		})
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, "failed to unmarshal responses stream event: "+err.Error())
			sr.Error(err)
			return
		}

		switch streamResp.Type {
		case "response.created":
			if streamResp.Response != nil && streamResp.Response.Model != "" {
				model = streamResp.Response.Model
			}

		case "response.reasoning_summary_text.delta":
			sendMessageStart()
			if streamResp.Delta == "" {
				break
			}
			if claudeConvert.LastMessagesType != relaycommon.LastMessageTypeThinking {
				stopOpenBlocksAndAdvance()
				idx := claudeConvert.Index
				helper.ClaudeData(c, dto.ClaudeResponse{
					Index: &idx,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type:     "thinking",
						Thinking: common.GetPointer[string](""),
					},
				})
				claudeConvert.LastMessagesType = relaycommon.LastMessageTypeThinking
			}
			idx := claudeConvert.Index
			helper.ClaudeData(c, dto.ClaudeResponse{
				Index: &idx,
				Type:  "content_block_delta",
				Delta: &dto.ClaudeMediaMessage{
					Type:     "thinking_delta",
					Thinking: &streamResp.Delta,
				},
			})

		case "response.reasoning_summary_text.done":

		case "response.output_text.delta":
			sendMessageStart()
			if streamResp.Delta == "" {
				break
			}
			outputText.WriteString(streamResp.Delta)

			if claudeConvert.LastMessagesType != relaycommon.LastMessageTypeText {
				stopOpenBlocksAndAdvance()
				idx := claudeConvert.Index
				helper.ClaudeData(c, dto.ClaudeResponse{
					Index: &idx,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type: "text",
						Text: common.GetPointer[string](""),
					},
				})
				claudeConvert.LastMessagesType = relaycommon.LastMessageTypeText
			}
			idx := claudeConvert.Index
			helper.ClaudeData(c, dto.ClaudeResponse{
				Index: &idx,
				Type:  "content_block_delta",
				Delta: &dto.ClaudeMediaMessage{
					Type: "text_delta",
					Text: common.GetPointer[string](streamResp.Delta),
				},
			})

		case "response.output_item.added":
			if streamResp.Item == nil || streamResp.Item.Type != "function_call" {
				break
			}
			sendMessageStart()

			itemID := strings.TrimSpace(streamResp.Item.ID)
			callID := strings.TrimSpace(streamResp.Item.CallId)
			if callID == "" {
				callID = itemID
			}
			name := strings.TrimSpace(streamResp.Item.Name)

			if itemID != "" {
				toolCallItemIDToCallID[itemID] = callID
				if name != "" {
					toolCallItemIDToName[itemID] = name
				}
			}

			if !toolCallItemIDStarted[itemID] {
				startToolBlock(itemID, callID, name)
			}

			// Emit any arguments that came with the item
			newArgs := streamResp.Item.ArgumentsString()
			if newArgs != "" {
				prevArgs := toolCallItemIDToArgs[itemID]
				delta := newArgs
				if prevArgs != "" && strings.HasPrefix(newArgs, prevArgs) {
					delta = newArgs[len(prevArgs):]
				}
				if delta != "" {
					toolCallItemIDToArgs[itemID] = newArgs
					emitToolArgsDelta(itemID, delta)
				}
			}

		case "response.output_item.done":
			if streamResp.Item == nil || streamResp.Item.Type != "function_call" {
				break
			}
			itemID := strings.TrimSpace(streamResp.Item.ID)

			// Emit final arguments if any new ones appeared
			newArgs := streamResp.Item.ArgumentsString()
			if newArgs != "" {
				prevArgs := toolCallItemIDToArgs[itemID]
				delta := newArgs
				if prevArgs != "" && strings.HasPrefix(newArgs, prevArgs) {
					delta = newArgs[len(prevArgs):]
				}
				if delta != "" {
					toolCallItemIDToArgs[itemID] = newArgs
					emitToolArgsDelta(itemID, delta)
				}
			}

			// Close this specific tool call block
			if !toolCallItemIDClosed[itemID] {
				blockIndex := toolCallItemIDToBlockIndex[itemID]
				helper.ClaudeData(c, dto.ClaudeResponse{
					Type:  "content_block_stop",
					Index: common.GetPointer[int](blockIndex),
				})
				toolCallItemIDClosed[itemID] = true
			}

		case "response.function_call_arguments.delta":
			itemID := strings.TrimSpace(streamResp.ItemID)
			callID := toolCallItemIDToCallID[itemID]
			if callID == "" {
				callID = itemID
			}
			if callID == "" {
				break
			}

			toolCallItemIDToArgs[itemID] += streamResp.Delta

			if !toolCallItemIDStarted[itemID] {
				name := toolCallItemIDToName[itemID]
				startToolBlock(itemID, callID, name)
			}

			emitToolArgsDelta(itemID, streamResp.Delta)

		case "response.function_call_arguments.done":

		case "response.completed":
			if streamResp.Response != nil {
				if streamResp.Response.Model != "" {
					model = streamResp.Response.Model
				}
				if streamResp.Response.Usage != nil {
					if streamResp.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResp.Response.Usage.InputTokens
						usage.InputTokens = streamResp.Response.Usage.InputTokens
					}
					if streamResp.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResp.Response.Usage.OutputTokens
						usage.OutputTokens = streamResp.Response.Usage.OutputTokens
					}
					if streamResp.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResp.Response.Usage.TotalTokens
					} else {
						usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
					}
					if streamResp.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResp.Response.Usage.InputTokensDetails.CachedTokens
					}
				}
			}

			sendMessageStart()
			finalizeStream()

		case "response.error", "response.failed":
			if streamResp.Response != nil {
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
					sr.Stop(streamErr)
					return
				}
			}
			streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return

		default:
		}
	})

	if streamErr != nil {
		return nil, streamErr
	}

	// Fallback usage estimation
	if usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, outputText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	// Ensure message_start was sent even if no content events arrived
	if !sentMessageStart {
		sendMessageStart()
		finalizeStream()
	}

	return usage, nil
}
