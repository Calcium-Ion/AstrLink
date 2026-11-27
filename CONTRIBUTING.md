# 参与开发

<!-- markdownlint-configure-file { "MD013": { "tables": false } } -->

感谢你参与 AstrLink。问题反馈请附上操作系统、应用版本和复现步骤；日志与请求示例请先脱敏。

## 环境要求

- Go 1.27.3 或更新版本。
- Bun 1.3.14 或更新版本。
- Rust stable，至少 1.88，用于构建两个模型 worker。
- `make`；运行契约校验还需要 Ruby。

首次构建需要联网下载语言依赖、ONNX
Runtime 和平台运行库。模型权重不随构建下载，只有用户在应用中选择后才安装。

## 从源码运行

```sh
git clone https://github.com/Calcium-Ion/AstrLink.git
cd AstrLink
make dev
```

该命令安装前端依赖、构建网关和 worker，然后启动一个隔离的开发实例。它有自己的数据和配置目录，默认端口是 18318，可以和正式版或
`make start`
同时运行。客户端配置和 Agent 工具仍写在当前用户的主目录，与其他实例共用。

在开发实例里写入客户端配置后，客户端就改连开发实例；配置原本连着另一个 AstrLink 时会先确认。两个实例启动时只更新自己写入的配置，不会互相改回。`astrlink`
命令行连接最后启动的实例。

要用原来的数据和端口运行 AstrLink，执行
`make start`。它只构建一次前端，不监听文件，改代码后重新执行才会生效。

Windows 没有 `make` 时，可在安装好上述工具后执行：

```sh
cd apps/desktop
bun install --frozen-lockfile
bun run desktop:dev
```

`bun run desktop:start` 对应 `make start`。

仅调试前端时，在 `apps/desktop` 下运行
`bun run dev`。纯浏览器预览没有桌面原生接口，需要模拟数据；完整功能请通过
`desktop:dev` 使用。

在 macOS 上，`make dev` 运行 `apps/desktop/.mygo/dev` 中的
`AstrLink Dev.app`，系统身份为
`com.astrlink.desktop.dev`，通知权限与正式版分开；`make start` 构建并运行
`apps/desktop/build/<平台>/AstrLink.app`。开发版在 Go 代码改动后重新构建并重启，前端改动由开发服务器重新加载。

## 检查改动

在仓库根目录运行与改动相关的检查：

```sh
make desktop-install
make core-check convo-check contracts-check
make desktop-check
make core-race convo-race contracts-race
```

`make check`
还会构建 sidecar，并执行两个模型 worker 和桌面 Go 宿主（`make desktop-go-check`）的检查，需要完整的原生构建环境。

测试使用模拟上游和小型合成模型，不需要真实 API
Key。不要把访问令牌、模型权重、应用数据库或生成的安装包加入提交。

## 构建安装包

在目标操作系统上执行：

```sh
cd apps/desktop
bun install --frozen-lockfile
bun run desktop:build
```

构建产物位于 `apps/desktop/build/<系统>-<架构>/`，例如
`build/darwin-arm64/`。应用签名、公证和正式发布需要另行配置。

## GitHub Actions 打包

三个平台有独立的打包流程，推送 `main`、在 Actions 页面选择 **Run
workflow**，或推送版本标签即可运行：

| 流程            | 产物                                                                         |
| --------------- | ---------------------------------------------------------------------------- |
| macOS package   | Apple Silicon 和 Intel 的 `.dmg`、保留执行权限的 `.app.tar.gz`、`SHA256SUMS` |
| Linux package   | x64 `.deb`、`install.sh` 及其安装的 `.tar.gz`、`SHA256SUMS`                  |
| Windows package | x64 NSIS `.exe`                                                              |

macOS 和 Linux 的安装包仅在前端检查、Core 测试和包验证通过后上传，下载产物保留 14 天。

macOS 在对应架构的 runner 上构建并挂载 DMG 验证；Linux 在 Ubuntu
22.04 构建，再在 Debian 12 容器内安装并校验依赖和启动。

Unix 包验证检查架构、运行库与许可证、worker 进程启动，以及 Core 健康接口和正常退出；不启动桌面窗口，也不下载或执行生产模型。

失败时上传诊断文件，保留 7 天。

普通推送和手动运行将产物保存在 Actions 中。

推送版本标签时，Release 流程会构建对应标签的代码，验证成功后自动创建 Release，并附加各平台安装包和独立的 SHA-256 校验文件。

只有上传任务具有 `contents: write` 权限，使用 GitHub 自动提供的
`GITHUB_TOKEN`，无需配置个人访问令牌。

### 发布版本

版本号取自标签，例如 `v0.3.10` 发布 `0.3.10`，无需修改
`apps/desktop/package.json`。

1. 确认待发布提交已推送到 `main`，且该提交的 CI 已通过。Release
   流程开始时会再次检查，CI 未通过或仍在运行时会等待，失败则停止。
2. （可选）先在 GitHub **Releases → Draft a new release**
   为该标签保存一份草稿并写好版本说明。草稿不会触发打包；推送标签后，流程会保留草稿的标题和说明。未准备草稿时，流程会自动生成说明。
3. 创建并推送标签：

   ```sh
   git tag v0.3.10 <提交>
   git push github v0.3.10
   ```

   推送任何标签都会触发 Release 和 Intel macOS 打包，不要使用
   `git push --tags`，也不要推送非版本标签。预发布版本使用
   `v0.3.10-beta.1` 这类标签。

4. 在 Actions 查看各平台构建。全部完成后，流程才发布 Release 并上传安装包和更新清单，构建过程中 Release 保持为草稿。

构建失败时可以使用 **Re-run failed jobs**
重试。已经发布并带有 `latest.json` 的 Release 不会被覆盖；需要重发时请使用新的版本号。编辑已发布 Release 的说明不会重新打包。标签必须包含这些工作流，旧版本标签不会自动取得
`main` 上的新流程。

macOS 包仍不使用 Developer
ID 签名或公证，发布 Release 不会改变这一点。正式签名需要另行配置 Apple 凭据。

## 代码目录

| 目录                     | 内容                     |
| ------------------------ | ------------------------ |
| `apps/desktop`           | React 界面与 Go 桌面宿主 |
| `apps/privacy-worker`    | 本地隐私模型推理         |
| `apps/classifier-worker` | 本地路由分类模型推理     |
| `core`                   | Go 网关与诊断 MCP 程序   |
| `convo`                  | 会话与用户轮次识别库     |
| `contracts`              | 管理接口及协议能力定义   |
| `docs/guides`            | 用户指南                 |

桌面界面优先复用 `apps/desktop/src/components`
中的组件。修改持久化配置或接口时，同步检查契约、兼容性与相关测试。提交说明应描述用户可观察到的变化及验证方式。
