# 接入 API 与 Coding Plan

[返回使用指南](README.md)

在 AstrLink 中添加 API 提供商后，客户端就可以通过本机网关使用它的模型。接入前，请准备好账号对应的 API 地址、密钥或订阅授权，并确认账号可用的模型。

**API 提供商的模型列表不能为空。** 保存账号信息后，还需要拉取或手动添加模型，该提供商才能处理推理请求。

## 先区分账号类型

同一家厂商的开放平台 API 和编程订阅可能使用不同的地址、密钥和额度。添加时应按实际购买的产品选择类型。

| 账号类型 | 接入方式 |
| --- | --- |
| 厂商开放平台 API | 通常按用量收费，使用开放平台生成的 API Key |
| Coding Plan | 使用编程订阅专用的密钥和地址，不能假定开放平台密钥也可用 |
| OpenCode Zen / Go | Zen 为按量付费，Go 为月费订阅，分别选择对应类型 |
| Codex / Claude / Grok 账号订阅 | 选择对应订阅类型，按界面提示完成授权 |

Grok 订阅（SuperGrok / Grok Build）通过 xAI Device Code 登录，请求经由 Grok CLI 代理。它与 xAI 开放平台 API Key 相互独立。

## 添加提供商

1. 打开 **API 提供商 → 添加 API 提供商**，选择账号类型和厂商。
2. 核对 API 地址，填写密钥，或完成订阅账号授权。
3. 拉取或手动添加需要使用的模型，核对入口协议，然后保存。
4. 在提供商列表中打开测试窗口，确认模型可以正常回复。操作见[测试 API 提供商与模型](provider-testing.md)。

客户端连接 AstrLink 时，使用的是 **AstrLink 访问令牌**。这里填写的上游 API Key 仅供 AstrLink 连接 API 提供商使用。

## 按量付费 API

选择 **按量付费 API** 后，AstrLink 会按厂商填入预设地址。使用其他地域或国际站账号时，请改为账号控制台提供的地址。

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
| 豆包（火山方舟） | `https://ark.cn-beijing.volces.com/api/v3` | 手动添加模型 ID 或以 `ep-` 开头的推理接入点 ID |
| xAI（Grok） | `https://api.x.ai/v1` | 拉取或手动添加 |
| OpenCode Zen | `https://opencode.ai/zen/v1` | 拉取或手动添加 |

“拉取”是否成功取决于提供商是否支持模型发现接口，以及账号是否有权限。无法拉取时，请按控制台中的实际模型 ID 手动添加。

百炼预设使用北京地域，密钥需与地域或业务空间匹配。Moonshot 国际账号通常使用 `https://api.moonshot.ai/v1`，MiniMax 国际账号使用 `https://api.minimax.io/v1`。

## Coding Plan

| API 提供商 | 默认地址 |
| --- | --- |
| OpenCode Go | `https://opencode.ai/zen/go/v1` |
| Kimi Coding | `https://api.kimi.ai/coding` |
| GLM Coding Plan | `https://open.bigmodel.cn/api/anthropic` |
| MiniMax Coding Plan | `https://api.minimax.cn/anthropic` |

使用订阅控制台提供的凭据，并核对当前套餐支持的模型。API 提供商预设不会改变你的订阅权益或额度。

## New API 和自定义网关

选择 **New API** 或对应兼容类型，填写网关地址和该网关颁发的 API Key。支持模型和协议取决于网关实际配置，不能仅凭预设判断所有接口都可用。

## 选择客户端协议

在入口协议配置中核对客户端所需的接口。原样转发要求上游支持同一种接口；上游格式不同的情况下，可选择界面中可用的协议转换。

例如，Gemini API 预设允许通过 OpenAI Chat Completions 接口调用，由 AstrLink 转换为 Gemini 请求。协议转换可能无法保留所有厂商专有参数；遇到工具调用或推理参数不兼容时，先尝试该提供商的原生接口。

切换提供商类型后，请重新检查模型和协议设置。已有提供商不会因为预设更新而自动覆盖保存的配置。

## 排查接入失败

| 问题 | 检查内容 |
| --- | --- |
| 401 / 403 | 账号、密钥、地域，以及 API / Coding Plan 类型是否对应 |
| 模型不可用 | 模型 ID、账号权限和 AstrLink 中的模型列表；火山方舟可能需要推理接入点 ID |
| 协议不支持 | 客户端接口与提供商入口配置是否匹配，是否需要启用可用的协议转换 |
| 连接失败 | 提供商地址、系统代理，以及请求记录中的具体失败原因 |

客户端连接步骤见[首页](../../README.md#开始使用)，网关端口与代理设置见[桌面设置](../../apps/desktop/README.md)。
