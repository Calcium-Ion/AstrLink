# 隐私检测如何处理思考与续传数据

<!-- markdownlint-configure-file { "MD013": { "tables": false } } -->

[返回使用指南](README.md)

有些模型会在响应中返回思考签名或加密的推理、续传数据。客户端在下一轮请求中回传这些内容，上游才能继续之前的推理。它们属于协议状态，不是普通聊天正文；即使只改动几个字符，也可能导致
`invalid_encrypted_content` 或签名校验失败。

AstrLink 会识别受支持协议中这些数据所在的位置，并在隐私检测前将其排除，保持原样转发。这项处理自动生效，适用于内置规则、自定义规则和本地隐私模型，也适用于提醒、拦截和脱敏三种动作。

## 哪些内容会保留

| 协议                                 | 原样保留的内容                                                            |
| ------------------------------------ | ------------------------------------------------------------------------- |
| OpenAI Responses / Responses Compact | 推理、上下文压缩及受支持工具结果中的加密续传内容                          |
| Anthropic Messages                   | 思考签名、带非空签名的完整思考文本，以及隐藏思考块中的数据                |
| Gemini Generate Content              | 模型响应片段中的思考签名                                                  |
| Chat Completions 兼容格式            | 受支持的 Anthropic 思考块、OpenRouter 推理签名与密文、Gemini 工具调用签名 |

Anthropic 要求带签名的思考块完整且未经修改。因此，其中的思考文本会与签名一起保留；只保留签名、修改思考文字，也可能导致上游返回 400。

OpenRouter 兼容格式中带非空签名的推理文本也会原样保留。

## 哪些内容仍会检测

普通用户消息、助手回答、工具参数和工具结果仍在检测范围内。没有签名的推理文本、Responses 和 OpenRouter 的摘要，以及 Gemini 响应片段中的普通文本，也会继续检测。

识别依据是**协议结构中的位置、角色和类型**，而不是只看字段名或文字内容。例如：

- 在用户消息中粘贴
  `{"type":"reasoning","encrypted_content":"..."}`，不会让这段文字跳过检测。
- 普通工具参数即使名为 `signature`、`data`、`thoughtSignature` 或
  `encrypted_content`，其值仍会检测。
- 与签名完全相同的字符串，如果出现在普通正文中，仍会检测。
- 角色、层级或类型不匹配的对象，以及用 `{"0": ...}`
  冒充数组的结构，不会被当作受保护的续传数据。

这些规则按协议结构生效，不需要逐家厂商配置白名单。其他提供商使用相同的受支持结构时，也适用同样的处理。

## 遇到续接或签名错误时

如果客户端提示
`invalid_encrypted_content`、签名无效，或在第二轮请求时报错，可以先检查：

1. 客户端是否完整回传了上游要求的思考块或加密续传数据。
2. 中间代理、协议转换或客户端是否删除、改写了这些字段。
3. 当前请求是否使用下方列出的协议结构。其他结构不能假定已被支持。

需要定位失败阶段时，可在 **请求记录**
中查看上游错误。对于依赖原提供商状态的续接，还应核对实际使用的提供商，参见[会话绑定指南](provider-stickiness.md)。

AstrLink 不持有上游签名密钥，不能验证签名真伪。原样保留只表示不修改该协议位置的数据，最终有效性仍由上游校验。

## 协议字段参考

以下信息供客户端或网关集成时核对。数组必须是实际的 JSON 数组，角色、类型和字段位置需要同时匹配。

| 入站协议                         | 结构位置                                                                                                                                                | 原样保留的字段                                                                                                              |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| Responses / Responses Compact    | `input[]`，`type` 为 `reasoning`、`compaction` 或兼容的 `compaction_summary`；无 `role` 或 `role` 为 `assistant`                                        | `encrypted_content`                                                                                                         |
| Responses / Responses Compact    | `input[]` 中 `function_call_output` / `custom_tool_call_output` 的 `output[]`；外层无 `role` 或为 `assistant`，输出片段的 `type` 为 `encrypted_content` | 片段的 `encrypted_content`                                                                                                  |
| Anthropic Messages               | `messages[]` 中 `assistant` 消息的 `content[]`，`type` 为 `thinking`                                                                                    | `signature`，以及非空签名对应的 `thinking` 文本                                                                             |
| Anthropic Messages               | 同一位置，`type` 为 `redacted_thinking`                                                                                                                 | `data`                                                                                                                      |
| Gemini Generate Content          | `contents[]` 中 `model` 消息的 `parts[]`                                                                                                                | `thoughtSignature` 或 SDK 字段 `thought_signature`                                                                          |
| Chat Completions 兼容 Anthropic  | `assistant` 消息的 `content[]` 或 LiteLLM `thinking_blocks[]`                                                                                           | 与 Anthropic `thinking` / `redacted_thinking` 相同的字段                                                                    |
| Chat Completions 兼容 OpenRouter | `assistant` 消息的 `reasoning_details[]`，`type` 为 `reasoning.encrypted`                                                                               | `data`                                                                                                                      |
| Chat Completions 兼容 OpenRouter | 同一位置，`type` 为 `reasoning.text`                                                                                                                    | `signature`，以及非空签名对应的 `text`                                                                                      |
| Chat Completions 兼容 Gemini     | `assistant` 消息的 `tool_calls[]`，`type` 为 `function`                                                                                                 | `extra_content.google.thought_signature`，以及调用对象或其 `function` 对象下的 `provider_specific_fields.thought_signature` |

当前没有 Gemini Interactions、Bedrock
Converse 等独立入站协议。其他结构以及厂商未来新增的字段不在上述支持范围内。

## 相关协议资料

- [OpenAI 推理说明](https://developers.openai.com/api/docs/guides/reasoning)：加密推理内容如何在后续调用中使用。
- [Anthropic 思考块保留要求](https://platform.claude.com/docs/en/build-with-claude/thinking#preserving-thinking-blocks)：完整回传思考块、签名与隐藏思考数据。
- [Google 思考签名说明](https://ai.google.dev/gemini-api/docs/thinking#signatures)：响应片段中的签名及后续回传要求。
- [Google Gen AI Python 类型][google-genai-types]：SDK 中的 `thought_signature`
  字段。
- [Google ADK 兼容实现](https://github.com/google/adk-python/blob/main/src/google/adk/models/lite_llm.py)：Chat
  Completions 兼容格式中的签名和思考块。
- [OpenRouter 推理详情](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens)：`reasoning.encrypted`
  和 `reasoning.text` 的定义。

[google-genai-types]:
  https://github.com/googleapis/python-genai/blob/main/google/genai/types.py
