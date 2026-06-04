package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// --- OpenAI Responses API types ---

type ResponsesRequest struct {
	Model        string           `json:"model"`
	Instructions *string          `json:"instructions,omitempty"`
	Input        ResponsesInput   `json:"input"`
	Tools        []ResponsesTool  `json:"tools,omitempty"`
	Temperature  *float64         `json:"temperature,omitempty"`
	MaxOutputTokens *int          `json:"max_output_tokens,omitempty"`
	TopP         *float64         `json:"top_p,omitempty"`
	Stream       bool             `json:"stream,omitempty"`
}

type ResponsesInput json.RawMessage

func (ri *ResponsesInput) UnmarshalJSON(data []byte) error {
	*ri = ResponsesInput(data)
	return nil
}

type ResponsesTool struct {
	Type     string          `json:"type"`
	Name     string          `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type ResponsesResponse struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Model     string            `json:"model"`
	Output    []ResponsesOutput `json:"output"`
	Usage     *ResponsesUsage   `json:"usage,omitempty"`
}

type ResponsesOutput struct {
	ID      string              `json:"id"`
	Type    string              `json:"type"`
	Status  string              `json:"status"`
	Role    string              `json:"role"`
	Content []ResponsesContentPart `json:"content"`
}

type ResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// --- Conversion: Responses Request → Gemini Request ---

func responsesToGemini(req *ResponsesRequest) (*GeminiRequest, string, error) {
	gemReq := &GeminiRequest{}

	if req.Instructions != nil && *req.Instructions != "" {
		gemReq.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: *req.Instructions}},
		}
	}

	contents, err := responsesInputToContents(req.Input)
	if err != nil {
		return nil, "", err
	}
	gemReq.Contents = contents

	if req.Temperature != nil || req.MaxOutputTokens != nil || req.TopP != nil {
		gemReq.GenerationConfig = &GenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxOutputTokens,
			TopP:            req.TopP,
		}
	}

	for _, t := range req.Tools {
		if t.Type == "function" {
			gemReq.Tools = append(gemReq.Tools, GeminiTool{
				FunctionDeclarations: []FunctionDeclaration{{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				}},
			})
		}
	}

	model := req.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}

	return gemReq, model, nil
}

func responsesInputToContents(input ResponsesInput) ([]GeminiContent, error) {
	var s string
	if err := json.Unmarshal(input, &s); err == nil {
		return []GeminiContent{{Role: "user", Parts: []GeminiPart{{Text: s}}}}, nil
	}

	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(input, &msgs); err != nil {
		return nil, fmt.Errorf("invalid input format: %w", err)
	}

	var contents []GeminiContent
	for _, msg := range msgs {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			continue
		}

		chatContent := ChatContent(msg.Content)
		parts, err := contentToParts(&chatContent)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, GeminiContent{Role: role, Parts: parts})
	}
	return contents, nil
}

// --- Conversion: Gemini Response → Responses Response ---

func geminiToResponses(gemResp *GeminiResponse, model string) *ResponsesResponse {
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	var output []ResponsesOutput
	for _, cand := range gemResp.Candidates {
		text := extractText(cand.Content)
		output = append(output, ResponsesOutput{
			ID:     msgID,
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []ResponsesContentPart{
				{Type: "output_text", Text: text},
			},
		})
	}

	resp := &ResponsesResponse{
		ID:        fmt.Sprintf("resp_%d", time.Now().UnixNano()),
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Model:     model,
		Output:    output,
	}

	if gemResp.UsageMetadata != nil {
		resp.Usage = &ResponsesUsage{
			InputTokens:  gemResp.UsageMetadata.PromptTokenCount,
			OutputTokens: gemResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  gemResp.UsageMetadata.TotalTokenCount,
		}
	}

	return resp
}
