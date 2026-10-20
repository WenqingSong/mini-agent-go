package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
)

// 协议层使用的契约字面量。
//
// core.Role、core.ContentType 与 llm.FinishReason 的枚举常量都没有导出，
// 实现包只能以类型转换使用这些取值，不得在实现中新增公共符号（见 task Out of Scope）。
const (
	roleSystem    = core.Role("system")
	roleUser      = core.Role("user")
	roleAssistant = core.Role("assistant")
	roleTool      = core.Role("tool")

	contentTypeText = core.ContentType("text")

	finishStop      = llm.FinishReason("stop")
	finishToolCalls = llm.FinishReason("tool_calls")
	finishLength    = llm.FinishReason("length")
)

// Chat Completions 协议字面量。
const (
	// typeFunction 是 tools[] 与 tool_calls[] 唯一的函数类型。
	typeFunction = "function"
	// toolErrorPrefix 标记可恢复的工具失败，让模型能区分"结果"与"错误描述"。
	toolErrorPrefix = "工具执行失败: "
	// emptyArguments 是"无参数工具"的协议表示；调用方未提供原始 JSON 时回填。
	emptyArguments = "{}"
)

// chatRequest 是 Chat Completions 请求体（只包含本任务映射的字段）。
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Tools       []chatTool    `json:"tools,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
}

// chatMessage 是一条 Chat Completions 消息。
//
// Content 恒定输出为字符串：assistant 带工具调用时为 ""，tool 消息为结果文本。
type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatToolCall 是 assistant 消息回传的工具调用。
type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

// chatFunction 是工具调用的函数名与参数原始 JSON 文本。
type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatTool 是 tools[] 中的一项。
type chatTool struct {
	Type     string      `json:"type"`
	Function chatToolDef `json:"function"`
}

// chatToolDef 是工具定义，Parameters 为 JSON Schema 原文，不做改写。
type chatToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// chatResponse 是 Chat Completions 成功响应体。
type chatResponse struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
}

// chatChoice 保留 message 的原始字节，便于回填 Message.Raw。
type chatChoice struct {
	Message      json.RawMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

// chatRespMessage 是 choice.message 的解析形态。
type chatRespMessage struct {
	Role      string             `json:"role"`
	Content   *string            `json:"content"`
	ToolCalls []chatRespToolCall `json:"tool_calls"`
}

// chatRespToolCall 是响应中的工具调用；Arguments 保持原始 JSON 文本。
type chatRespToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatRespFunction `json:"function"`
}

// chatRespFunction 是响应中的函数名与原始参数 JSON 文本。
type chatRespFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatUsage 是 token 用量；OpenAI 兼容服务可能缺省整体 usage。
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// opBuildRequest 是请求构造阶段错误的固定 Op。
const opBuildRequest = "openai.buildChatRequest"

// opParseResponse 是响应解析阶段错误的固定 Op。
const opParseResponse = "openai.parseChatResponse"

// buildChatRequest 把契约请求映射为 Chat Completions 请求体。
//
// 映射只做结构转写：文本内容按片段顺序拼接，工具参数与 JSON Schema 原样透传，
// 不改写语义。映射失败（缺 ID、缺函数名、缺 Schema、未知角色）属于调用方契约错误，
// 由 Complete 以 invalid_argument 返回且不发出请求。
func buildChatRequest(req llm.Request) (chatRequest, error) {
	out := chatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	messages := make([]chatMessage, 0, len(req.Messages)+1)
	// System 是纯文本 system prompt，映射为独立的 system 消息并位于最前。
	if req.System != "" {
		messages = append(messages, chatMessage{Role: string(roleSystem), Content: req.System})
	}
	for i, msg := range req.Messages {
		converted, err := buildChatMessage(msg)
		if err != nil {
			return chatRequest{}, fmt.Errorf("消息[%d]: %w", i, err)
		}
		messages = append(messages, converted)
	}
	out.Messages = messages

	if len(req.Tools) > 0 {
		tools := make([]chatTool, 0, len(req.Tools))
		for i, spec := range req.Tools {
			converted, err := buildChatTool(spec)
			if err != nil {
				return chatRequest{}, fmt.Errorf("工具[%d]: %w", i, err)
			}
			tools = append(tools, converted)
		}
		out.Tools = tools
	}

	return out, nil
}

// buildChatMessage 映射单条消息。
func buildChatMessage(msg core.Message) (chatMessage, error) {
	switch msg.Role {
	case roleSystem, roleUser:
		return chatMessage{Role: string(msg.Role), Content: contentText(msg.Content)}, nil
	case roleAssistant:
		out := chatMessage{Role: string(roleAssistant), Content: contentText(msg.Content)}
		if len(msg.ToolCalls) == 0 {
			return out, nil
		}
		calls := make([]chatToolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if call.ID == "" {
				return chatMessage{}, core.NewError(core.KindInvalidArgument, opBuildRequest, "assistant 消息中的工具调用缺少 ID，无法回传给模型")
			}
			if call.Name == "" {
				return chatMessage{}, core.NewError(core.KindInvalidArgument, opBuildRequest, fmt.Sprintf("工具调用 %q 缺少函数名", call.ID))
			}
			arguments := emptyArguments
			if len(call.Arguments) > 0 {
				// arguments 以原始 JSON 文本透传，不做重新编排。
				arguments = string(call.Arguments)
			}
			calls = append(calls, chatToolCall{
				ID:       call.ID,
				Type:     typeFunction,
				Function: chatFunction{Name: call.Name, Arguments: arguments},
			})
		}
		out.ToolCalls = calls
		return out, nil
	case roleTool:
		if msg.ToolResult == nil {
			return chatMessage{}, core.NewError(core.KindInvalidArgument, opBuildRequest, "tool 消息必须携带 ToolResult")
		}
		if msg.ToolResult.CallID == "" {
			return chatMessage{}, core.NewError(core.KindInvalidArgument, opBuildRequest, "tool 消息缺少 CallID，无法定位对应工具调用")
		}
		content := msg.ToolResult.Content
		if msg.ToolResult.IsError {
			content = toolErrorPrefix + content
		}
		return chatMessage{
			Role:       string(roleTool),
			Content:    content,
			ToolCallID: msg.ToolResult.CallID,
		}, nil
	default:
		return chatMessage{}, core.NewError(core.KindInvalidArgument, opBuildRequest, fmt.Sprintf("未知消息角色 %q", msg.Role))
	}
}

// buildChatTool 映射一个工具定义；parameters 为必填 JSON Schema 原文。
func buildChatTool(spec core.ToolSpec) (chatTool, error) {
	if spec.Name == "" {
		return chatTool{}, core.NewError(core.KindInvalidArgument, opBuildRequest, "工具缺少名称")
	}
	if len(spec.Parameters) == 0 {
		return chatTool{}, core.NewError(core.KindInvalidArgument, opBuildRequest, fmt.Sprintf("工具 %q 缺少 parameters JSON Schema", spec.Name))
	}
	return chatTool{
		Type: typeFunction,
		Function: chatToolDef{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Parameters,
		},
	}, nil
}

// contentText 按片段顺序拼接纯文本内容；v1 只有 text 片段（core.Message.Validate 已约束）。
func contentText(parts []core.ContentPart) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(part.Text)
	}
	return b.String()
}

// parseChatResponse 把 Chat Completions 响应体解析为契约响应。
//
// 只要有一个字段无法无歧义地映射为契约语义（缺 choices、非法 JSON、未知终止原因、
// 工具调用缺 ID/函数名、arguments 非法 JSON），就整体失败为 provider_failure，
// 不返回半成品 Response；成功返回前强制走 Response.Validate。
func parseChatResponse(body []byte) (llm.Response, error) {
	var payload chatResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return llm.Response{}, protocolError("响应体不是合法 JSON", err)
	}
	if len(payload.Choices) == 0 {
		return llm.Response{}, protocolError("响应缺少 choices", nil)
	}

	choice := payload.Choices[0]
	if len(choice.Message) == 0 {
		return llm.Response{}, protocolError("响应 choices[0] 缺少 message", nil)
	}
	var message chatRespMessage
	if err := json.Unmarshal(choice.Message, &message); err != nil {
		return llm.Response{}, protocolError("响应 choices[0].message 不是合法 JSON 对象", err)
	}

	finishReason, err := mapFinishReason(choice.FinishReason)
	if err != nil {
		return llm.Response{}, err
	}
	// 契约要求返回消息恒为 assistant；provider 明确给出其他角色时视为协议错误。
	if message.Role != "" && core.Role(message.Role) != roleAssistant {
		return llm.Response{}, protocolError(fmt.Sprintf("响应 message.role 必须是 assistant，实际为 %q", message.Role), nil)
	}

	out := llm.Response{
		Message: core.Message{
			ID:   payload.ID,
			Role: roleAssistant,
			Raw:  append(json.RawMessage(nil), choice.Message...),
		},
		FinishReason: finishReason,
		Raw:          append(json.RawMessage(nil), body...),
	}

	if message.Content != nil {
		out.Message.Content = []core.ContentPart{{Type: contentTypeText, Text: *message.Content}}
	}

	if len(message.ToolCalls) > 0 {
		calls := make([]core.ToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if call.ID == "" {
				return llm.Response{}, protocolError("响应中的工具调用缺少 ID", nil)
			}
			if call.Function.Name == "" {
				return llm.Response{}, protocolError(fmt.Sprintf("响应中的工具调用 %q 缺少函数名", call.ID), nil)
			}
			if !json.Valid([]byte(call.Function.Arguments)) {
				// 空字符串同样不构成合法 JSON，一并视为协议错误。
				return llm.Response{}, protocolError(fmt.Sprintf("响应中的工具调用 %q 的 arguments 不是合法 JSON", call.ID), nil)
			}
			calls = append(calls, core.ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: json.RawMessage(call.Function.Arguments),
			})
		}
		out.Message.ToolCalls = calls
	}

	if payload.Usage != nil {
		if payload.Usage.PromptTokens < 0 || payload.Usage.CompletionTokens < 0 {
			return llm.Response{}, protocolError(fmt.Sprintf("响应用量不能为负数：prompt=%d completion=%d", payload.Usage.PromptTokens, payload.Usage.CompletionTokens), nil)
		}
		out.Usage = llm.Usage{
			InputTokens:  payload.Usage.PromptTokens,
			OutputTokens: payload.Usage.CompletionTokens,
		}
	}

	if err := out.Validate(); err != nil {
		return llm.Response{}, protocolError("响应不满足 llm.Response 契约校验", err)
	}

	return out, nil
}

// mapFinishReason 把 finish_reason 映射为契约取值。
//
// 未识别取值与缺失取值都按协议错误处理：无法判断模型是正常结束还是被截断，
// 静默映射成 unknown 会把"被截断的响应"当成可用结果继续往下走。
func mapFinishReason(reason string) (llm.FinishReason, error) {
	switch llm.FinishReason(reason) {
	case finishStop, finishToolCalls, finishLength:
		return llm.FinishReason(reason), nil
	default:
		return "", protocolError(fmt.Sprintf("未知 finish_reason %q", reason), nil)
	}
}

// protocolError 构造协议错误：统一为 provider_failure，并保留原始错误链。
func protocolError(msg string, cause error) *core.RunError {
	return &core.RunError{
		Kind:    core.KindProviderFailure,
		Op:      opParseResponse,
		Message: msg,
		Cause:   cause,
	}
}
