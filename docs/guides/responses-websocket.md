# 通过 WebSocket 使用 Responses

[返回使用指南](README.md)

支持 Responses
WebSocket 的客户端可以与 AstrLink 建立持续连接，在同一连接上逐轮发送请求、接收响应。每轮请求都会经过模型选择、上游凭据处理、隐私检测与还原、用量统计和请求记录；响应中的模型名也会按配置还原为客户端使用的别名。

**一条连接同一时间只能生成一个响应，并且固定使用同一家 API 提供商和同一个模型。**
更换提供商、模型或凭据时，需要新建连接。

## 在提供商中启用

打开 API 提供商的编辑页面，在连接设置中调整 Responses
WebSocket 开关，并保存表单。提供商列表中的 WebSocket 图标只用于显示能力，不能直接切换设置。

| 提供商类型 | 未单独设置时的默认值                     |
| ---------- | ---------------------------------------- |
| Codex 订阅 | 开启，包括已有但尚未保存此项设置的提供商 |
| 其他提供商 | 关闭                                     |

已经明确保存的关闭设置会保留。WebSocket 开关与普通 HTTP 支持相互独立。

提供商还必须同时满足以下条件：已启用，支持流式
`openai.responses`，且该协议没有配置本地转换。仅开启 WebSocket 开关并不足以让不支持该协议的上游处理请求。

## 连接本机网关

连接地址为：

```text
ws://127.0.0.1:<推理端口>/v1/responses
```

请将端口替换为 AstrLink 界面显示的实际推理端口。客户端应在握手请求中通过
`Authorization` 请求头携带本地访问令牌：

```http
Authorization: Bearer <AstrLink 访问令牌>
```

此入口不接受带浏览器来源（`Origin`）的连接，也不接受 URL 查询参数中的令牌。因此，需要使用能够设置请求头的客户端；网页中的原生 WebSocket 接口不适用于这种连接方式。

AstrLink 使用提供商自己的凭据连接上游，并遵循网关的出站代理设置。本地访问令牌和客户端 Cookie 不会转发给上游。

## 发送请求与继续会话

连接成功后，发送一个 JSON 文本消息：

```json
{
  "type": "response.create",
  "model": "your-model",
  "input": "Hello",
  "store": false
}
```

响应事件同样以 JSON 文本消息返回。等待本轮完成后，可以在同一连接中发送下一条
`response.create`，带上新的输入，并通过 `previous_response_id` 引用上一轮响应。

客户端还可以发送 `response.cancel` 取消生成。`generate: false`
预热请求和顺序使用的 `stream_id` 也会转发给上游。

### 并发与连接限制

- 上一轮尚未结束时再发送生成请求，会收到 409 错误事件。
- 当前不支持一条连接中的并行响应，也不支持在生成过程中插入指令改变本轮输出。
- 每一轮都会重新检查本地认证和提供商是否仍然可用。撤销访问令牌或关闭提供商的 WebSocket 开关，会阻止下一轮请求。
- 成功完成一轮后，上游连接会保留，等待后续请求。

## 断线、失败与请求记录

建立连接时的握手失败会遵循已有的重试和故障转移策略。一旦生成请求已经发给上游，AstrLink 不会自动重发该请求。

连接断开时，会取消上游请求。中断或失败的生成会在请求记录中记为失败。排查问题时，可以在
**请求记录** 中查看实际提供商、模型和失败原因。

输入消息遵循网关的请求体大小设置；即使该设置为不限，WebSocket 消息仍有 128
MiB 的安全上限。单个上游响应事件的上限也是 128 MiB。

## 客户端集成参考

WebSocket 使用 `GET /v1/responses`
完成协议升级。服务配置中的开关字段为可选布尔值
`responses_websocket_enabled`，支持创建和更新；显式传入 `false`
可覆盖 Codex 订阅的默认开启行为。

兼容旧版客户端使用的嵌套消息格式，即将请求参数放在 `response` 对象中：

```json
{
  "type": "response.create",
  "response": {
    "model": "your-model",
    "input": "Hello",
    "store": false
  }
}
```

仅适用于 HTTP 的 `stream`、`stream_options` 和 `background` 字段不会转发给上游。

协议细节见
[OpenAI WebSocket 模式说明](https://developers.openai.com/api/docs/guides/websocket-mode)，提供商配置字段见[控制接口定义](../../contracts/control-api.openapi.yaml)。
