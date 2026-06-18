// anthropic.go is a minimal client for the Anthropic Messages API — just
// enough of the request/response shape to run a tool-use conversation loop.
//
// This is deliberately NOT the official Anthropic Go SDK: the eval harness
// needs exactly one endpoint and a handful of fields, and the project's
// existing posture (see go.mod, which depends on nothing but the MCP SDK) is
// to avoid a dependency where a tight stdlib net/http + encoding/json client
// covers the need. If the harness later needs streaming, retries, or other
// endpoints, that tradeoff is worth revisiting.
package evals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	anthropicAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
	// defaultMaxTokens bounds each model response. The harness's prompts and
	// expected responses are short (tool calls and brief confirmations), so this
	// is generous headroom without inviting runaway-length replies.
	defaultMaxTokens = 1024
)

// anthropicTool is a tool definition in the shape the Messages API expects.
// mcp.Tool's Name/Description/InputSchema map onto this directly — InputSchema
// is already a JSON Schema object, so no schema is hand-duplicated here.
type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// contentBlock is a tagged union covering the three block types this harness
// needs: assistant text, an assistant tool_use request, and a user-supplied
// tool_result. Fields irrelevant to a block's Type are simply omitted.
type contentBlock struct {
	Type string `json:"type"` // "text" | "tool_use" | "tool_result"

	// "text" blocks.
	Text string `json:"text,omitempty"`

	// "tool_use" blocks (assistant -> harness).
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// "tool_result" blocks (harness -> assistant, as a user-role message).
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// anthropicMessage is one turn in the conversation sent to/received from the
// API; Content is always the block-array form (never the bare-string
// shorthand the API also accepts), which keeps encoding uniform throughout.
type anthropicMessage struct {
	Role    string         `json:"role"` // "user" | "assistant"
	Content []contentBlock `json:"content"`
}

type messagesRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type messagesResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

// anthropicError mirrors the Messages API's error envelope so a non-2xx
// response can be reported with the API's own message rather than a bare
// status code.
type anthropicError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicClient sends Messages API requests authenticated with a single API
// key. It is intentionally tiny: one method, no connection pooling beyond
// http.DefaultClient, no retries — a failed eval call is a failed eval case,
// not something to silently paper over by retrying.
type anthropicClient struct {
	apiKey string
}

// sendMessage issues one Messages API call and returns the parsed response.
func (c *anthropicClient) sendMessage(ctx context.Context, req messagesRequest) (*messagesResponse, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = defaultMaxTokens
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("evals: encoding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("evals: building request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	httpReq.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("evals: calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("evals: reading API response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr anthropicError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("evals: Anthropic API returned %d: %s: %s", resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("evals: Anthropic API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var out messagesResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("evals: decoding API response: %w", err)
	}
	return &out, nil
}
