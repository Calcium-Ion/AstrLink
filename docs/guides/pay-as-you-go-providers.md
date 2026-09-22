# 接入 API 与 Coding Plan

在 **API 服务 → 添加服务** 中选择与你购买的服务相对应的类型，填写密钥，再配置模型列表。模型列表为空时，该服务不会处理推理请求。

## 先区分账号类型

- **厂商开放平台 API**：通常按用量收费，使用开放平台生成的 API Key。
- **Coding Plan**：使用编程订阅专用的密钥和地址，不一定能用开放平台密钥替代。
- **OpenCode Zen / Go**：Zen 是按量付费服务，Go 是月费订阅，添加服务时分别选择。
- **Codex / Claude 账号订阅**：在订阅类型中按提示完成授权，无需按下面的 API Key 方式填写。

## 按量付费 API

在服务类型中选择 **按量付费 API**，再选择厂商。AstrLink 会填入预设地址；使用不同地域或国际站账号时，请改为该账号控制台提供的地址。

| 厂商 | 默认 API 地址 | 模型列表 |
| --- | --- | --- |
| OpenAI | `https://api.openai.com/v1` | 拉取或手动添加 |
| Anthropic | `https://api.anthropic.com` | 拉取或手动添加 |
| Gemini | `https://generativelanguage.googleapis.com` | 拉取或手动添加 |
| DeepSeek | `https://api.deepseek.com/v1` | 拉取或手动添加 |
| 千问（百炼） | `https://dashscope.aliyuncs.com/compatible-mode/v1` | 手动添加 |
| Kimi（Moonshot） | `https://api.moonshot.cn/v1` | 拉取或手动添加 |
| 智谱 GLM | `https://open.bigmodel.cn/api/paas/v4` | 手动添加 |
| MiniMax | `https://api.minimax.cn/v1` | 拉取或手动添加 |
| 豆包（火山方舟） | `https://ark.cn-beijing.volces.com/api/v3` | 手动添加模型 ID 或 `ep-…` 推理接入点 ID |
| xAI（Grok） | `https://api.x.ai/v1` | 拉取或手动添加 |
| OpenCode Zen | `https://opencode.ai/zen/v1` | 拉取或手动添加 |

“拉取”是否成功取决于服务商的模型发现接口和账号权限；没有模型发现接口的服务，需要按控制台中的实际模型 ID 手动添加。

百炼预设使用北京地域，密钥需与地域或业务空间匹配。Moonshot 国际账号通常使用 `https://api.moonshot.ai/v1`，MiniMax 国际账号使用 `https://api.minimax.io/v1`。

## Coding Plan

| 服务 | 默认地址 |
| --- | --- |
| OpenCode Go | `https://opencode.ai/zen/go/v1` |
| Kimi Coding | `https://api.kimi.ai/coding` |
| GLM Coding Plan | `https://open.bigmodel.cn/api/anthropic` |
| MiniMax Coding Plan | `https://api.minimax.cn/anthropic` |

使用订阅控制台提供的凭据，并核对当前套餐支持的模型。服务预设不会改变你的订阅权益或额度。

## New API 和自定义网关

选择 **New API** 或对应兼容类型，填写网关地址和该网关颁发的 API Key。支持模型和协议取决于网关实际配置，不能仅凭预设判断所有接口都可用。

客户端连接 AstrLink 时，仍应使用 AstrLink 的访问令牌；这里填写的上游密钥仅用于 AstrLink 连接服务商。

## 选择客户端协议

保存服务后，在入口协议配置中核对客户端所需的接口。原样转发要求上游支持同一种接口；上游格式不同的情况下，可选择界面中可用的协议转换。

例如，Gemini API 预设允许通过 OpenAI Chat Completions 接口调用，由 AstrLink 转换为 Gemini 请求。协议转换可能无法保留所有厂商专有参数，遇到工具调用或推理参数不兼容时，先尝试该服务的原生接口。

切换服务类型时请检查模型和协议设置。已有服务不会因为预设更新而自动覆盖保存的配置。

## 排查接入失败

- **401 / 403**：检查账号、密钥、地域以及 API / Coding Plan 类型是否对应。
- **模型不可用**：核对模型 ID、账号权限和 AstrLink 模型列表；火山方舟可能需要推理接入点 ID。
- **协议不支持**：检查客户端使用的接口与服务入口配置，必要时启用可用的转换。
- **连接失败**：检查服务地址和系统代理，然后在请求记录中查看实际失败原因。

接入步骤见 [首页](../../README.md)，网关端口与代理设置见 [桌面设置](../../apps/desktop/README.md)。
