# 参与开发

感谢你参与 AstrLink。问题反馈请附上操作系统、应用版本和复现步骤；日志与请求示例请先脱敏。

## 环境要求

- Go 1.25.1 或更新版本。
- Bun 1.3.14 或更新版本。
- Rust stable，至少 1.88。
- [Tauri 各平台开发依赖](https://v2.tauri.app/start/prerequisites/)。
- `make`；运行契约校验还需要 Ruby。

首次构建需要联网下载语言依赖、ONNX Runtime 和平台运行库。模型权重不随构建下载，只有用户在应用中选择后才安装。

## 从源码运行

```sh
git clone https://github.com/Calcium-Ion/AstrLink.git
cd AstrLink
make dev
```

该命令安装前端依赖、构建网关和 worker，然后启动桌面应用。Windows 没有 `make` 时，可在安装好上述工具后执行：

```sh
cd apps/desktop
bun install --frozen-lockfile
bun run desktop:dev
```

仅调试前端时，在 `apps/desktop` 下运行 `bun run dev`。纯浏览器预览没有桌面原生接口，需要模拟数据；完整功能请通过 `desktop:dev` 使用。

## 检查改动

在仓库根目录运行与改动相关的检查：

```sh
make desktop-install
make core-check convo-check contracts-check
make desktop-check
make core-race convo-race contracts-race
```

`make check` 还会构建 sidecar，并执行两个模型 worker 和桌面 Rust 宿主的检查，需要完整的原生构建环境。

测试使用模拟上游和小型合成模型，不需要真实 API Key。不要把访问令牌、模型权重、应用数据库或生成的安装包加入提交。

## 构建安装包

在目标操作系统上执行：

```sh
cd apps/desktop
bun install --frozen-lockfile
bun run desktop:build
```

构建产物位于 `apps/desktop/src-tauri/target/release/bundle/`；指定 Rust target 时位于相应 target 子目录中。应用签名、公证和正式发布需要另行配置。

## GitHub Actions 打包

三个平台有独立的打包流程，推送 `main` 或在 Actions 页面选择 **Run workflow** 即可运行：

| 流程 | 产物 |
| --- | --- |
| macOS package | Apple Silicon 和 Intel 的 `.dmg`、保留执行权限的 `.app.tar.gz`、`SHA256SUMS` |
| Linux package | x64 `.deb`、`SHA256SUMS` |
| Windows package | x64 NSIS `.exe` |

macOS 和 Linux 的安装包仅在前端检查、Core 测试和包验证通过后上传，下载产物保留 14 天。macOS 在对应架构的 runner 上构建并挂载 DMG 验证；Linux 在 Ubuntu 22.04 构建，再在 Debian 12 容器内安装并校验依赖和启动。

Unix 包验证检查架构、运行库与许可证、worker 进程启动，以及 Core 健康接口和正常退出；不启动桌面窗口，也不下载或执行生产模型。失败时上传诊断文件，保留 7 天。

这些流程生成开发安装包，不会创建 GitHub Release。macOS 包不使用 Developer ID 签名或公证，运行 CI 无需配置 Apple 凭据。

## 代码目录

| 目录 | 内容 |
| --- | --- |
| `apps/desktop` | React 界面与 Tauri 桌面宿主 |
| `apps/privacy-worker` | 本地隐私模型推理 |
| `apps/classifier-worker` | 本地路由分类模型推理 |
| `core` | Go 网关与诊断 MCP 程序 |
| `convo` | 会话与用户轮次识别库 |
| `contracts` | 管理接口及协议能力定义 |
| `docs/guides` | 用户指南 |

桌面界面优先复用 `apps/desktop/src/components` 中的组件。修改持久化配置或接口时，同步检查契约、兼容性与相关测试。提交说明应描述用户可观察到的变化及验证方式。
