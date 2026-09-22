# 隐私检测与思考续传数据

思考签名、加密推理和加密续传内容属于上游协议状态。隐私过滤一旦将其替换，即使只替换部分字符，也可能导致下一轮 `invalid_encrypted_content` 或签名校验失败。它们必须在抽取正文时排除，不能依赖隐私模型的置信度或密文前缀。

`core/internal/privacy/continuation.go` 从请求根结构识别这些字段，再由 `document.go` 在所有检测器之前跳过对应字符串。原有 Responses 专项修复已收拢到这一入口；原测试保留。规则、自定义规则、模型及 block/warn/redact 共用同一边界。重写只修改实际抽取的字符串，所以签名原始 JSON 字节也不被改写。

## 已实现的协议范围

| 入站协议 | 结构位置 | 原样保留的字段 |
| --- | --- | --- |
| Responses / Responses compact | `input[]`，type 为 `reasoning`、`compaction` 或现有兼容 `compaction_summary`；无 role 或 assistant role | `encrypted_content` |
| Responses / Responses compact | `input[]` 中 `function_call_output` / `custom_tool_call_output` 的 `output[]`，part type 为 `encrypted_content` | part 的 `encrypted_content` |
| Anthropic Messages | `messages[]` assistant 的 `content[]`，type 为 `thinking` | `signature`，以及非空签名对应的 `thinking` 文本 |
| Anthropic Messages | 同位置，type 为 `redacted_thinking` | `data` |
| Gemini generateContent | `contents[]` model 的 `parts[]` | `thoughtSignature` / SDK 字段 `thought_signature` |
| Chat Completions 兼容 Anthropic | assistant 的 `content[]` 或 LiteLLM `thinking_blocks[]` | 上述 thinking / redacted_thinking 字段 |
| Chat Completions 兼容 OpenRouter | assistant 的 `reasoning_details[]`，type 为 `reasoning.encrypted` | `data` |
| Chat Completions 兼容 OpenRouter | 同位置，type 为 `reasoning.text` | `signature`，以及非空签名对应的 `text` |
| Chat Completions 兼容 Gemini | assistant 的 `tool_calls[]`，type 为 `function` | `extra_content.google.thought_signature`，call 或 function 下 `provider_specific_fields.thought_signature` |

Anthropic 要求回传完整、未修改的 thinking block，修改其文字同样可能报 400。因此，带非空签名的这个 block 中的 thinking 文本与签名一起保留。普通 assistant 回答、无签名推理文本、Responses summary、OpenRouter summary，以及 Gemini part 里的普通 text 仍被检查。

这不是按厂商名称切换的白名单。使用以上相同协议结构的其他厂商自动适用。当前协议注册表没有 Gemini Interactions、Bedrock Converse 等独立入站协议；其其他载体与未来新字段不在已验证范围。新增载体必须先确认类型及结构位置，再增加抽取、伪造字段和回传测试，不能宣称所有未来厂商均已覆盖。

## 不允许通过同名字段绕过

以下内容继续正常检测：

- 用户文字中粘贴的 `{"type":"reasoning","encrypted_content":"..."}`。
- 普通消息、工具参数及工具结果中任意命名为 `signature`、`data`、`thoughtSignature`、`encrypted_content` 的值。
- 将数组伪装成 `{"0": ...}` 的对象，或错误角色、错误层级和错误 type。
- 与签名内容恰好完全相同、但位于普通正文里的字符串。

协议结构只能确定该位置应当携带上游状态；AstrLink 不持有上游签名密钥，不能自行证明一个签名是真的。签名真伪仍由上游校验，不能把排除检测描述为密码学验证。

## 证据与回归

核实日期：2026-09-22。没有读取或上传私人请求记录，所有回归密文和密钥均为测试构造值。

- [OpenAI reasoning 文档](https://developers.openai.com/api/docs/guides/reasoning)：stateless 的 `encrypted_content` 用于后续调用，输出历史需回传。
- [Anthropic preserving thinking blocks](https://platform.claude.com/docs/en/build-with-claude/thinking#preserving-thinking-blocks)：thinking blocks 必须 complete and unmodified；修改可产生 400。该页也说明 `signature`、`redacted_thinking` 和流式 `signature_delta`。
- [Google thinking 文档](https://ai.google.dev/gemini-api/docs/thinking#signatures)：generateContent 签名附着于 part，可以出现在函数调用或最终响应部分。
- [Google Gen AI Python 类型](https://github.com/googleapis/python-genai/blob/main/google/genai/types.py)：`Part.thought_signature` 为用于后续请求的 opaque signature。
- [Google ADK 兼容实现](https://github.com/google/adk-python/blob/main/src/google/adk/models/lite_llm.py)：`_extract_thought_signature_from_tool_call` 及 outbound tool-call 实现定义 `extra_content.google` / `provider_specific_fields`，也保留 Anthropic `thinking_blocks`。tool-call ID 内的 `__thought__` 载体已有协议 ID 排除规则，无需按字符串前缀另增规则。
- [OpenRouter reasoning details](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens)：定义 `reasoning.encrypted.data` 和 `reasoning.text.signature`。

自动回归覆盖抽取、模型输入、相同字符串不同位置、真实敏感正文继续替换、请求原始字节保留、warn/block 不误触发、JSON/SSE 响应恢复、Anthropic signature delta，以及既有 Responses 双轮历史回放。没有连接厂商在线 API，不把离线回归称为在线厂商验收。
