# 使用指南

这里介绍 AstrLink 的日常操作、功能行为和常见问题。第一次使用时，先按照[首页的「开始使用」](../../README.md#开始使用)添加 API 提供商、创建访问令牌并连接客户端。

## 接入与测试

| 指南 | 适合什么时候阅读 |
| --- | --- |
| [接入 API 与 Coding Plan](pay-as-you-go-providers.md) | 选择账号类型、填写上游地址和密钥、配置模型与协议 |
| [测试 API 提供商与模型](provider-testing.md) | 确认连接是否可用，批量测试模型，查看回复和耗时 |
| [通过 WebSocket 使用 Responses](responses-websocket.md) | 为支持 WebSocket 的客户端配置连接，了解续接与并发限制 |

## 路由与费用

| 指南 | 适合什么时候阅读 |
| --- | --- |
| [让同一会话优先使用同一家 API 提供商](provider-stickiness.md) | 设置会话粘性、查看绑定记录或重新选择提供商 |
| [理解未计价调用与费用估算](billing-unpriced.md) | 了解部分调用为何没有金额，以及哪些情况会自动补算 |

## 隐私检测

| 指南 | 适合什么时候阅读 |
| --- | --- |
| [隐私检测如何处理思考与续传数据](privacy-reasoning-continuation.md) | 了解哪些协议数据会原样保留，以及普通正文的检测范围 |

## 其他文档

- [桌面设置](../../apps/desktop/README.md)：网关端口、系统代理、窗口行为、模型安装和诊断。
- [参与开发](../../CONTRIBUTING.md)：开发环境、构建和测试。
- [Core 说明](../../core/README.md)：独立网关进程的启动方式和参数。
- [控制接口定义](../../contracts/control-api.openapi.yaml)：供开发与集成使用的管理接口。
