# 接入 API 与 Coding Plan

<!-- markdownlint-configure-file { "MD013": { "tables": false } } -->

[返回使用指南](README.md)

在 AstrLink 中添加 API 提供商后，客户端就可以通过本机网关使用它的模型。接入前，请准备好账号对应的 API 地址、密钥或订阅授权，并确认账号可用的模型。

**API 提供商的模型列表不能为空。**
保存账号信息后，还需要拉取或手动添加模型，该提供商才能处理推理请求。

## 先区分账号类型

同一家厂商的开放平台 API 和编程订阅可能使用不同的地址、密钥和额度。添加时应按实际购买的产品选择类型。

| 账号类型                       | 接入方式                                                 |
| ------------------------------ | -------------------------------------------------------- |
| 厂商开放平台 API               | 通常按用量收费，使用开放平台生成的 API Key               |
| Coding Plan                    | 使用编程订阅专用的密钥和地址，不能假定开放平台密钥也可用 |
| OpenCode Zen / Go              | Zen 为按量付费，Go 为月费订阅，分别选择对应类型          |
| Codex / Claude / Grok 账号订阅 | 选择对应订阅类型，按界面提示完成授权                     |
| GitHub Copilot 订阅            | 确认风险提示后，用 GitHub Device Code 完成授权           |
| Droid 订阅                     | 确认风险提示后，用 Factory Device Code 完成授权          |

Grok 订阅（SuperGrok / Grok Build）通过 xAI Device Code 登录，请求经由 Grok
CLI 代理。它与 xAI 开放平台 API Key 相互独立。

GitHub Copilot 订阅：

- AstrLink 以 OpenCode 的身份登录，GitHub 授权页上请求授权的应用叫 OpenCode。OpenCode 是 GitHub 官方支持的第三方客户端。
- GitHub 仍可能判定经网关的使用违规，请连接你本人的账号。
- Copilot 的模型名用点号（`claude-sonnet-4.6`）。提供商的“模型重定向”里有内置规则，把 Claude
  Code 发的 `claude-sonnet-4-6` 换成
  `claude-sonnet-4.6`，每条都可以关闭或改目标。

Droid 订阅（Factory Pro / Plus / Max）：

- AstrLink 以 Droid
  CLI 的身份登录和转发请求，Factory 登录页上请求授权的应用叫 Droid。
- Factory 只接受系统提示以 Droid 自我介绍开头的请求。AstrLink 会在每个请求的系统提示前加上这一行，你的提示词保持不变、排在它后面。
- Factory 没有模型列表接口。“拉取模型”给出 Factory 文档列出的模型，新模型可以手动添加。
- Factory 仍可能判定经网关的使用违规，请连接你本人的账号。

## 添加提供商

1. 打开 **API 提供商 → 添加 API 提供商**，选择账号类型和厂商。
2. 厂商预设选择账号所在的站点（国内站或国际站），网关类型填写网关地址；然后填写密钥，或完成订阅账号授权。
3. 拉取或手动添加需要使用的模型，核对入口协议，然后保存。
4. 在提供商列表中打开测试窗口，确认模型可以正常回复。操作见[测试 API 提供商与模型](provider-testing.md)。

客户端连接 AstrLink 时，使用的是 **AstrLink 访问令牌**。这里填写的上游 API
Key 仅供 AstrLink 连接 API 提供商使用。

## 按量付费 API

厂商预设不需要填写 API 地址：在 **API 地址**
中选择账号所在的站点即可。只有经反向代理或其他地域接入时，才需要选择
**自定义地址** 并填写。认证方式也由预设决定，只有 **自定义** 类型需要选择。

| 厂商             | 国内站 / 官方地址                                   | 国际站                                                   | 模型列表                                       |
| ---------------- | --------------------------------------------------- | -------------------------------------------------------- | ---------------------------------------------- |
| OpenAI           | `https://api.openai.com/v1`                         | —                                                        | 拉取或手动添加                                 |
| Anthropic        | `https://api.anthropic.com`                         | —                                                        | 拉取或手动添加                                 |
| Gemini           | `https://generativelanguage.googleapis.com`         | —                                                        | 拉取或手动添加                                 |
| DeepSeek         | `https://api.deepseek.com/v1`                       | —                                                        | 拉取或手动添加                                 |
| 千问（百炼）     | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` | 手动添加                                       |
| Kimi（Moonshot） | `https://api.moonshot.cn/v1`                        | `https://api.moonshot.ai/v1`                             | 拉取或手动添加                                 |
| 智谱 GLM         | `https://open.bigmodel.cn/api/paas/v4`              | `https://api.z.ai/api/paas/v4`                           | 手动添加                                       |
| MiniMax          | `https://api.minimax.cn/v1`                         | `https://api.minimax.io/v1`                              | 拉取或手动添加                                 |
| 豆包（火山方舟） | `https://ark.cn-beijing.volces.com/api/v3`          | —                                                        | 手动添加模型 ID 或以 `ep-` 开头的推理接入点 ID |
| xAI（Grok）      | `https://api.x.ai/v1`                               | —                                                        | 拉取或手动添加                                 |
| OpenCode Zen     | `https://opencode.ai/zen/v1`                        | —                                                        | 拉取或手动添加                                 |

“拉取”是否成功取决于提供商是否支持模型发现接口，以及账号是否有权限。无法拉取时，请按控制台中的实际模型 ID 手动添加。

百炼的国内站为北京地域，国际站为新加坡地域，密钥需与地域或业务空间匹配。

## Coding Plan

| API 提供商          | 国内站 / 官方地址                             | 国际站                                | 原生入口协议                                     |
| ------------------- | --------------------------------------------- | ------------------------------------- | ------------------------------------------------ |
| OpenCode Go         | `https://opencode.ai/zen/go/v1`               | —                                     | 按模型选择 Responses、Anthropic Messages 或 Chat |
| Kimi Coding         | `https://api.kimi.ai/coding`                  | —                                     | Responses、Anthropic Messages、Chat、Models      |
| GLM Coding Plan     | `https://open.bigmodel.cn/api/coding/paas/v4` | `https://api.z.ai/api/coding/paas/v4` | Responses、Anthropic Messages、Chat              |
| MiniMax Coding Plan | `https://api.minimax.cn/v1`                   | `https://api.minimax.io/v1`           | Responses、Anthropic Messages、Chat、Models      |

选择站点后即可使用该订阅的全部原生协议，AstrLink 会按请求协议切换到厂商对应的路径。已保存的厂商 Claude
Code 地址（如 `…/anthropic`）同样有效。GLM Coding Plan 的 Chat 请求固定发往
`/api/coding/paas/v4`，以免消耗开放平台余额；Responses 请求发往智谱统一的
`/api/v1`。

使用订阅控制台提供的凭据，并核对当前套餐支持的模型。API 提供商预设不会改变你的订阅权益或额度。

## New API 和自定义网关

选择 **New API** 或对应兼容类型，填写网关地址和该网关颁发的 API
Key。支持模型和协议取决于网关实际配置，不能仅凭预设判断所有接口都可用。

选择 **New API**
类型时，列表的「订阅额度」列会显示该密钥的已用和剩余额度，按网关公布的额度单位折算为美元。New
API 限制了额度查询频率，因此数据最多缓存 5 分钟。

## 选择客户端协议

在入口协议配置中核对客户端所需的接口。原样转发要求上游支持同一种接口；上游格式不同的情况下，可选择界面中可用的协议转换。

例如，Gemini API 预设允许通过 OpenAI Chat
Completions 接口调用，由 AstrLink 转换为 Gemini 请求。协议转换可能无法保留所有厂商专有参数；遇到工具调用或推理参数不兼容时，先尝试该提供商的原生接口。

切换提供商类型后，请重新检查模型和协议设置。已有提供商不会因为预设更新而自动覆盖保存的配置。

## 排查接入失败

| 问题       | 检查内容                                                                 |
| ---------- | ------------------------------------------------------------------------ |
| 401 / 403  | 账号、密钥、站点或地域，以及 API / Coding Plan 类型是否对应              |
| 模型不可用 | 模型 ID、账号权限和 AstrLink 中的模型列表；火山方舟可能需要推理接入点 ID |
| 协议不支持 | 客户端接口与提供商入口配置是否匹配，是否需要启用可用的协议转换           |
| 连接失败   | 提供商地址、系统代理，以及请求记录中的具体失败原因                       |

客户端连接步骤见[首页](../../README.zh-CN.md#开始使用)，网关端口与代理设置见[桌面设置](../../apps/desktop/README.md)。
