# AstrLink Core

Core 是 AstrLink 的 Go 网关进程，负责连接上游服务、转发请求、执行路由与隐私策略，以及记录用量。桌面应用会自动管理它的启动、配置和退出。

日常使用请阅读 [AstrLink 使用说明](../README.zh-CN.md)。下面的命令供开发和集成使用。

## 构建与测试

需要 Go 1.25.1 或更新版本。从本目录执行：

```sh
go build -trimpath -o bin/astrlink-core ./cmd/astrlink-core
go build -trimpath -o bin/astrlink-mcp ./cmd/astrlink-mcp
ASTRLINK_CI_NO_REMOTE_MODELS=1 go test ./...
```

也可以在仓库根目录使用 `make core-check` 和 `make core-race`。完整开发环境见 [参与开发](../CONTRIBUTING.md)。

## 启动方式

```sh
go run ./cmd/astrlink-core --help
```

不带参数启动时，仅提供基础健康检查，不会自动加载桌面中的服务配置，也不能直接用于上游推理。实际使用推荐运行桌面应用，由它管理持久化数据、凭据和所需 worker。

自定义宿主需要同时传入 `--data-dir` 和 `--control-token-stdin`，并通过标准输入提供本次启动的控制令牌。控制令牌用于管理接口，与客户端使用的访问令牌不同；不要把它放到命令行参数中。

常用参数：

| 参数 | 用途 |
| --- | --- |
| `--inference-listen` | 本机推理监听地址，默认 `127.0.0.1:8317` |
| `--inference-port-fallback` | 指定端口被占用时改用空闲端口 |
| `--max-request-body-mib` | 请求体大小上限，`0` 表示不设本地上限 |
| `--outbound-proxy` | 选择 `environment`、`system` 或 `direct` 代理模式 |
| `--privacy-worker` | 指定本地隐私检测 worker |
| `--classifier-worker` | 指定本地路由分类 worker |

启动成功后，标准输出会给出一行 `ready` JSON，其中包含实际的推理地址和控制地址。运行诊断写入标准错误。

## API 与客户端

推理接口包括 OpenAI Responses、Chat Completions、Anthropic Messages 和 Gemini Generate Content。服务必须启用对应协议或配置支持的转换路径；模型必须在服务模型列表或路由中可用。

访问推理接口使用 AstrLink 访问令牌。管理接口定义见 [Control API](../contracts/control-api.openapi.yaml)，协议能力结构见 [协议能力定义](../contracts/protocol-capabilities.schema.json)。

`astrlink-mcp` 为支持 MCP 的本地工具提供请求诊断能力；配套使用说明见 [调试技能](../agent-bundle/astrlink-debug/SKILL.md)。

## 数据与隐私

持久化模式在数据目录内保存 SQLite 数据库、服务配置和请求记录。API 密钥保存在专用凭据表中；订阅授权使用系统凭据存储。不要把应用数据目录作为普通日志上传或分享。

隐私检测在请求发送到上游之前运行，可使用本地规则或已安装的模型。模型检测失败时请求会被拒绝。正文捕获需要单独启用；访问令牌、上游密钥和请求正文应分别管理。

## 许可证

Core 的自有源码采用 [Apache-2.0](../LICENSE)。依赖 RelayKit 使用 AGPL-3.0，分发组合构建和提供相应网络服务时还需遵守该依赖的条款。
