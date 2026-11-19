# AstrLink 插件 API（v2）

<!-- markdownlint-configure-file { "MD013": { "tables": false } } -->

AstrLink Core 内嵌
[moejs](https://github.com/MoeForge/moejs)，用来运行插件；插件用到的 Web
API、事件循环和定时器，以及 `node:fs`、`Buffer`
的主要部分，都是 moejs 自己的实现。

插件用 JavaScript 或 TypeScript 编写，负责策略：请求放不放行、走哪个提供商、往请求里加什么、怎么改回复、在请求记录和插件页上展示什么。

AstrLink 负责机制：时间和内存上限、存储、确认，以及把插件的结果放到哪里。

插件和 npm 包一样是受信任的代码，不进沙箱：除了 AstrLink 提供的 `astrlink.*`
函数和标准的 Web
API，还能用 Node 的大部分内置模块，网络可以访问任何地址。它碰不到的是：插件自己目录以外的文件、子进程和线程、网关的端口、网关的环境变量，以及网关替它保存的密钥。插件声明的能力在安装审查里列出，启用插件即全部生效。

## 写一个插件

最简单的插件是一个文件：导出清单 `meta`
和若干钩子，此外不能导出别的名字。完整的例子见
[team-rules/plugin.ts](team-rules/plugin.ts)：它把配置里的团队规则写进每个会话的系统提示词。

插件多了文件或要用 npm 依赖时，写成一个项目：

```text
my-plugin/
├── package.json        # name、version、engines.astrlink，以及 astrlink 字段
├── package-lock.json   # 有依赖时必需；也认 pnpm-lock.yaml、bun.lock
├── src/plugin.ts       # 后端入口（必需）
├── src/client.tsx      # 界面入口（可选）
├── assets/             # 插件运行时读的文件（可选）
└── icon.svg            # 图标（可选）
```

`package.json` 里写
`"astrlink": { "plugin": "src/plugin.ts", "client": "src/client.tsx", "files": ["assets"] }`；不写时按
`src/plugin.ts`、`plugin.ts` 这些惯用的位置找。`files`
列出插件运行时要读的文件或目录，随插件装进安装目录，最多 1000 个、32
MiB。`engines.astrlink`
写兼容的 AstrLink 版本范围，不满足时拒绝安装。项目由 AstrLink 内置的构建器打包成单文件，运行时不再加载别的代码；Node 的内置模块不打进包里，由网关提供（见「Node 的模块」）；React 和界面组件由宿主提供，不要打进包里。

命令行 `astrlink plugin`
和 Core 用同一套构建器，本机构建的结果和用户安装时一致：

| 命令                        | 做什么                                                                                                                 |
| --------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `astrlink plugin new <key>` | 从模板起项目：`basic`、`files`、`gate`、`route`、`forward`、`stream`、`response`、`schedule`、`http`、`provider`、`ui` |
| `astrlink plugin build`     | 构建出 `plugin.js`、`client.js` 和 source map                                                                          |
| `astrlink plugin check`     | 构建并在 moejs 里编译，检查清单、能力和不该用的 API                                                                    |
| `astrlink plugin test`      | 按 `tests/` 里的用例跑钩子，可以伪造存储、网络应答和时钟                                                               |
| `astrlink plugin dev`       | 监听改动、构建，装进本机 AstrLink 的插件目录并实时输出日志                                                             |

`astrlink plugin new` 会把类型定义放进项目：后端是
[astrlink-plugin.d.ts](../../core/plugins/astrlink-plugin.d.ts)，界面是
[astrlink-plugin-client.d.ts](../../core/plugins/astrlink-plugin-client.d.ts)。下文说到的字段以它们为准。

插件装在 `<数据目录>/plugins/<key>/`：`plugin.ts` 或 `plugin.js`（可以带
`plugin.js.map`），有界面时还有
`client.js`。随版本发布的内置插件可以被目录里同 key 的插件替换。

- 同一目录里可以放一个 `icon.svg` 或 `icon.png`（最多 64
  KiB，两个都有时用 SVG），插件页把它当作插件的图标。SVG 不能带脚本；不合要求的图标会被忽略，不影响插件加载。
- 手动改了文件后，在「插件」页点「重新扫描」即可，不用重启。正在处理的请求继续用旧的版本跑完。
- 单个 `.ts` 文件由 moejs 去掉类型后直接运行，不做类型检查；`enum`、带值的
  `namespace` 这类有运行时语义的写法要经过构建。源码最多 8 MiB。
- apiVersion 1 的插件必须同步执行：不支持
  `async`、`await`，加载前就会报错并给出行号。

## 从 GitHub 安装

插件页「安装插件」里粘贴 GitHub 链接即可，作者不用另外打包：

- 链接可以是仓库首页（装最新的版本标签，没有标签就装默认分支）、`tree/<分支或标签>/<目录>`（一个仓库放多个插件）、Release 或提交。暂不支持私有仓库。
- AstrLink 先把链接定到具体的提交，经网关的出网代理下载这个提交的源码。定位提交和下载都走 Git 协议和源码包，不占 GitHub 接口每小时的次数；只有更新说明和提交时间要用接口，次数用完时不显示。
- 有依赖时必须有锁文件。依赖按锁文件从 npm 下载，用锁文件里的哈希逐个校验；默认用 npm 官方源，失败时退到 npmmirror，源可以在安装对话框里改。只下载构建实际用到的包，不运行任何安装脚本；带原生模块的包直接报不支持。
- 构建后在 moejs 里检查，核对
  `engines.astrlink`，然后显示审查结果：来源、版本、作者、申请的能力、联网用途、有没有界面代码、依赖的个数和大小，以及会替换哪个插件（内置的、开发中的、别处装的）。
- 安装要在「插件确认」窗口里确认。装好的插件不会自动启用；目录里的
  `install.json` 记下仓库、提交、子目录、锁文件和构建产物的哈希。
- 「检查更新」看同一个仓库：装的是标签就看同一前缀的新版本标签（显示 Release 说明），装的是分支就看分支的新提交，装的是固定提交就不跟随。更新同样经过审查和确认，审查里写明新增和去掉的能力、界面代码、联网用途和依赖的变化。
- 被替换的版本保留最近 3 个，可以回滚；回滚也要确认。卸载会删掉插件文件和保留的版本，插件的设置、存储的数据和数据目录里的文件保留，再装回来时先停用；替换过内置插件的，卸载后内置插件恢复。

## 清单 meta

| 字段           | 说明                                                                                               |
| -------------- | -------------------------------------------------------------------------------------------------- |
| `apiVersion`   | `2`；`1` 的插件照常运行，但只能用 v1 的能力和钩子，而且必须同步执行                                |
| `key`          | 小写字母开头，只含小写字母、数字和短横线，最多 32 个字符，与目录名相同                             |
| `name`         | 界面上显示的名称，最多 64 个字符                                                                   |
| `version`      | 版本号，最多 32 个字符                                                                             |
| `capabilities` | 申请的能力，见下表                                                                                 |
| `network`      | 申请网络时必填：`{ purpose: "…" }`，安装审查和插件页展示给用户                                     |
| `credentials`  | 凭据位：`name`、`label`、`header`、可选的 `scheme`（如 `Bearer`）。需要网络能力                    |
| `metadata`     | 启用时登记的元信息属性：`key`、`label`、`pattern`（以 `^` 开头、`$` 结尾）、可选的 `marker`        |
| `notice`       | 给用户的提醒，最多 1000 个字符，原样显示在插件页的介绍里                                           |
| `settings`     | 插件页「设置」表单的字段，按顺序排列，见下文                                                       |
| `credits`      | 插件依托或对接的开源项目，显示在「概览」里，见下文                                                 |
| `storage`      | 插件在 `astrlink.store` 里存了什么，显示在「概览」的「数据存储」里，见下文                         |
| `tools`        | 供模型使用的查询和上报，最多 8 个，见「允许 Agent 查询」                                           |
| `limits`       | v2：各扩展点需要的时间上限（毫秒），不能超过网关的上限，见「扩展点」                               |
| `failClosed`   | v2：`onRequest` 出错或超时时拦下请求而不是放行，需要 `request.gate`，用户同意后才生效              |
| `schedules`    | v2：`onSchedule` 的运行间隔：`[{ name, everyMinutes }]`，最多 8 个，1～10080 分钟，需要 `schedule` |
| `providers`    | v2：插件转接的供应商，最多 4 个，需要 `provider`，见「供应商适配器」                               |

清单按严格 JSON 读取：拼错的字段会报错，不会被悄悄忽略。

### 设置项 settings

插件页按 `settings` 生成「设置」表单，最多 64 项。每一项：

- `key`：值在配置里的位置，点号表示下一层，例如
  `quotas.skill`。两项的位置不能重叠。
- `label`：最多 32 个字符。`type`：`server`、`text`、`number`、`switch` 或
  `choice`。
- `group`：所在分组的标题；`row`：同一 `row`
  的相邻字段排在一行，行名就是它；`advanced: true` 收进「更多设置」。
- `help`：放在字段旁的 `?` 里，最多 500 个字符；表单里不放说明小字。
- `default`：配置里没有值时，`astrlink.config.get()`
  返回它。`required`：保存时不能为空。
- `number` 可带 `min`、`max`、`step`、`unit`；`choice` 必须带
  `options: [{ value, label }]`；`placeholder` 显示在空的输入框里。

`server`
是插件要连的服务地址，没有默认值，只能由用户填写。保存时 AstrLink 把它的协议、主机和端口加进允许访问的地址，标明来自这个设置；`credential`
指定的凭据跟着绑到这个地址，地址改了，凭据也跟着改。本机以外的明文 `http://`
仍要用户在表单里勾选同意，换一个地址要重新同意。

以 `$` 开头的键放置 AstrLink 自己的设置，只能带 `group` 和 `advanced`：

| 键            | 设置                                               | 条件                  |
| ------------- | -------------------------------------------------- | --------------------- |
| `$wait`       | 最长等待                                           |                       |
| `$trusted`    | 受信任，跳过隐私检查                               |                       |
| `$origins`    | 其他允许访问的地址                                 | 需要 `network`        |
| `$clients`    | 对哪些客户端生效，默认全部                         |                       |
| `$workspace`  | 按工作区自动绑定，默认打开，见「元信息」           | 需要 `metadata`       |
| `$refresh`    | 自动刷新常驻块，默认关闭，见「常驻块什么时候重算」 | 需要 `context.inject` |
| `$lookup`     | 允许 Agent 查询，默认打开，见「允许 Agent 查询」   | 需要 `tools`          |
| `$limits`     | 各扩展点的时间上限，见「扩展点」                   | v2                    |
| `$failClosed` | 入口出错时拦下请求                                 | 需要 `failClosed`     |

插件没有放置的，表单放在「网关」分组，地址放进「更多设置」。

保存时 AstrLink 按设置项检查配置：类型、范围、选项、必填，出错时指明是哪一项。

设置项没有声明的键原样保留，插件照常读取；「更多设置」里的「以 JSON 编辑」改的是同一份配置。

### 数据存储 storage

`storage` 声明插件自己在 `astrlink.store`
里存的东西，最多 8 项：`label`（最多 100 个字符）、`content`（是否含对话内容，默认否）、`retention`（留多久，最多 64 个字符）。需要
`storage` 能力。

插件页「概览」的「数据存储」把这些和 AstrLink 替插件存的东西按存放的地方（AstrLink 数据库、只在内存里、请求记录）分栏列出：会话绑定、工作区和值的对应、插件在每个会话里的第一轮和暂停状态、请求记录里的插件记录、只在内存里的常驻块。每一项是什么、留多久，以及插件声明的各项，都收在卡片标题旁的问号里。工作区记录和插件存储可以在那里清除。

### 开源声明 credits

`credits` 列出插件依托或对接的开源项目，最多 8 项：`name`、可选的
`owner`、可选的 SPDX 许可证标识 `license`（如
`Apache-2.0`）、`url`（只能是 https，用系统浏览器打开）。插件页「概览」底部写成「使用了开源项目 name」；`url`
是 GitHub 仓库时，旁边显示它的星标数和 GitHub 图标，星标数由 Core 向 GitHub 查询，缓存 6 小时。

## 能力

| 能力             | 给插件的东西                                              |
| ---------------- | --------------------------------------------------------- |
| `context.inject` | `prepareSession`、`prepareTurn` 可以往请求里加内容        |
| `request.read`   | 钩子能看到本轮的用户文本和元信息；`astrlink.request.info` |
| `network`        | `fetch`、`node:http(s)` 和 `astrlink.http.fetch`          |
| `storage`        | `astrlink.store`，插件私有的键值存储                      |
| `ui.views`       | 钩子可以返回请求记录和插件页里的视图                      |
| `model.tools`    | 模型可以运行 `tools` 里的查询和上报，由 `callTool` 作答   |

apiVersion 2 另有这些能力：

| 能力                | 给插件的东西                                                                   |
| ------------------- | ------------------------------------------------------------------------------ |
| `request.write`     | `beforeForward` 可以追加系统提示词、在本轮开头加内容、改生成参数、增删工具     |
| `request.write.raw` | `beforeForward` 可以替换整个请求体，`beforeUpstream` 可以改每次尝试的请求      |
| `request.gate`      | `onRequest` 可以拒绝请求                                                       |
| `routing`           | `route` 可以调整候选提供商的顺序、排除提供商、换模型                           |
| `response.read`     | `onResponse` 在请求结束后看到回复                                              |
| `response.write`    | `rewriteResponse` 改整段回复，`onStreamEvent`、`onStreamEnd` 改流式事件        |
| `events`            | `onEvent` 收到网关里发生的事，不含请求和回复的内容                             |
| `schedule`          | 按 `meta.schedules` 定时运行 `onSchedule`                                      |
| `http.serve`        | 推理端口上的 `/plugins/<key>/…` 由 `onHttp` 作答                               |
| `provider`          | 提供 `meta.providers` 里的供应商，见「供应商适配器」                           |
| `ui.code`           | 高风险：插件的界面代码在主窗口里运行，和 AstrLink 界面同等权限，见「界面代码」 |

插件声明的能力在安装审查里列出，`ui.code`
用红字写明风险；启用插件即全部生效，不再逐项授予。调用没有声明的能力对应的函数会抛出
`TypeError`。

这些操作只在单独的「插件确认」窗口里生效：安装、更新和回滚插件代码，设为受信任（跳过隐私检查），让凭据经明文 HTTP 发往本机以外的服务器，以及出错时拦下请求。窗口自己弹出、不抢焦点，主窗口里运行的代码点不到它。网页控制台没有这个窗口，用它自己的确认框。

## 运行时

apiVersion 2 的钩子可以写成
`async`，在 moejs 的事件循环上运行，直到钩子返回的 Promise 兑现：等网络、存储、文件和定时器的时候不算脚本时间，也不占插件的执行槽位。一直不兑现的 Promise 算失败。Promise 兑现后，这次调用还没结束的请求和宿主操作会被取消，定时器一并清除，不会带到下一次调用。

可以用的全局：

- `fetch`、`Request`、`Response`、`Headers`：可以访问任何地址，规则见「宿主函数」；响应体可以用
  `response.body` 按流读取。
- `ReadableStream`、`WritableStream`、`TransformStream` 和
  `TextDecoderStream`、`TextEncoderStream`，`AbortController`、`AbortSignal`（含
  `timeout`、`any`），`Event`、`EventTarget`、`DOMException`。
- `URL`、`URLSearchParams`（按 WHATWG 标准解析）、`TextEncoder`、`TextDecoder`、`atob`、`btoa`、`structuredClone`、`queueMicrotask`。
- `Intl` 和各类型的 `toLocaleString`，带 11 种语言的数据；默认语言是
  `en-US`，时区是 UTC。
- `crypto.getRandomValues`、`crypto.randomUUID`、`crypto.subtle.digest`，以及 HMAC 的签名和校验。
- `setTimeout`、`setInterval`、`setImmediate` 和对应的清除函数，返回 Node 的
  `Timeout` 对象（`ref`、`unref`、`refresh`）。只在 apiVersion
  2 的钩子运行时触发，受本次调用的时间上限约束；模块顶层设的定时器不会触发。
- `console`：写进插件日志。`performance`。
- Node 的全局：`process`、`Buffer`、`global`、`require`（只能取 Node 的模块）、`__dirname`
  和 `__filename`（指向安装目录）、`CustomEvent`。
- `eval` 和 `new Function`
  可以用。内置对象每个运行时一份，插件可以修改，不影响别的插件。

### Node 的模块

插件可以 `import` 或 `require` Node 的内置模块，写 `node:fs` 或 `fs`
都行。构建器不把它们打进包里，网关加载插件时提供，行为以 Node 22 为准。

| 模块                                                                                                                                                                                  | 说明                                                                                                                                                                                                            |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fs`、`fs/promises`                                                                                                                                                                   | 同步、回调和 Promise 三种写法，`FileHandle`、读写流、目录和 `glob`。只能读写插件自己的目录，见下                                                                                                                |
| `path`、`url`、`util`、`events`、`buffer`、`stream`（含 `promises`、`consumers`）、`string_decoder`、`querystring`、`punycode`、`assert`、`timers`（含 `promises`）、`readline`、`vm` | 和 Node 一致                                                                                                                                                                                                    |
| `crypto`                                                                                                                                                                              | 摘要（MD5、SHA-1、SHA-2、SHA-3）、HMAC、随机数、PBKDF2、scrypt、HKDF、AES（CBC、CTR、GCM、ECB）和 ChaCha20-Poly1305、RSA／ECDSA／Ed25519 的签名和验签、生成密钥对、`KeyObject`；`webcrypto` 就是全局的 `crypto` |
| `zlib`                                                                                                                                                                                | gzip、deflate、brotli 的压缩和解压，同步、回调和流三种写法；流在结束时一次处理，解压结果最多 256 MiB                                                                                                            |
| `http`、`https`                                                                                                                                                                       | 客户端，建在 `fetch` 上，规则和 `fetch` 相同。Agent 不保持连接，`ca`、`cert` 这些 TLS 选项不起作用                                                                                                              |
| `sqlite`                                                                                                                                                                              | `DatabaseSync`、`StatementSync`，数据库文件放在插件自己的目录里；打开的数据库跨调用一直可用                                                                                                                     |
| `os`                                                                                                                                                                                  | 系统的只读信息；`homedir()` 是插件的数据目录，`tmpdir()` 是其中的 `.tmp`                                                                                                                                        |
| `process`                                                                                                                                                                             | `env` 只有 `NODE_ENV`、`HOME`、`TMPDIR`、`TZ`；`cwd()` 是数据目录；`stdout`、`stderr` 写进插件日志；`exit`、`kill`、`chdir` 抛出错误                                                                            |
| `async_hooks`                                                                                                                                                                         | `AsyncLocalStorage`：`run` 给的值在它运行的函数结束前一直有效，跨 `await` 可用；同一次调用里并行的几路看到的是最新的值                                                                                          |
| `perf_hooks`、`diagnostics_channel`、`module`、`console`、`constants`、`domain`、`tty`                                                                                                | 常用部分；`module.createRequire` 只能取 Node 的模块                                                                                                                                                             |

不提供的模块：

- `child_process`、`worker_threads`、`cluster`：起进程或线程能读写任何文件，目录的限制就形同虚设。
- `net`、`tls`、`dgram`、`dns`、`http2`：网关的端口不交给插件；要对外提供接口，用
  `http.serve` 扩展点。
- `inspector`、`repl`、`wasi`，以及 `v8` 的堆快照。

插件自己的代码导入它们时，构建直接报错；依赖导入时只给警告，模块照常加载，调用到的函数抛出
`ERR_ACCESS_DENIED`。`net.isIP` 这类纯计算的函数照常可用。

### 插件自己的目录

| 目录     | 位置                            | 用法                                                                                                             |
| -------- | ------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| 数据目录 | `<数据目录>/plugin-data/<key>/` | 可读写，是 `process.cwd()`，相对路径都落在这里；`os.tmpdir()` 指向其中的 `.tmp/`                                 |
| 安装目录 | `<数据目录>/plugins/<key>/`     | 只读，`import.meta.dirname` 和 `__dirname` 指向这里；`package.json` 的 `astrlink.files` 列出的文件随插件装在这里 |

- 其他路径一律报 `EACCES`，消息里写明「plugins read and write only their own
  directories」；往安装目录写报
  `EROFS`。符号链接只能用相对路径指向数据目录里，指到外面同样被拒。
- 数据目录最多 1 GiB，超出的写入报 `ENOSPC`。
- 一次调用里打开的文件描述符在调用结束时关闭。
- apiVersion
  2 插件在 Linux 和 macOS 上，数据目录里的读写、`stat`、`readdir`、`mkdir`、`rm`、`cp`、`rename`
  和 `glob` 由 moejs 的 `node:fs`
  完成；安装目录和其余操作（文件描述符、流、`chmod`
  等）由网关处理，行为都以 Node 为准。
- 卸载插件时数据目录保留，和设置一样，重新安装接着用；插件页「数据存储」里可以清除。更新和回滚会整体替换安装目录，所以要留下的数据放在数据目录。

`astrlink.textStream({ map?, replace?, maxMatch? })`
是流式改写的帮手：它认得 Chat
Completions、Responses、Messages 和 Gemini 的流式格式，把回复文字逐段交给
`map`，或按 `replace` 的规则替换（跨事件的匹配最多看 `maxMatch`
个字符），返回现成的 `onStreamEvent` 和 `onStreamEnd`。

## 扩展点

apiVersion 2 的扩展点按请求经过的顺序：

| 扩展点                             | 做什么                                               | 能力                                 | 默认上限 / 最大  | 出错或超时时                                     |
| ---------------------------------- | ---------------------------------------------------- | ------------------------------------ | ---------------- | ------------------------------------------------ |
| `onRequest(ctx)`                   | 放行、打标签或拒绝                                   | 拒绝要 `request.gate`                | 1 s / 5 s        | 放行；插件声明 `failClosed` 且用户同意时拦下     |
| `route(ctx)`                       | 调整候选提供商的顺序、排除提供商、换模型             | `routing`                            | 1 s / 5 s        | 保持原来的顺序                                   |
| `beforeForward(ctx)`               | 改请求：系统提示词、本轮开头、参数、工具、整个请求体 | `request.write`、`request.write.raw` | 2.5 s / 5 s      | 原样发出                                         |
| `beforeUpstream(ctx)`              | 改每次尝试发往上游的请求，用提供商的协议             | `request.write.raw`                  | 1 s / 5 s        | 原样发出                                         |
| `rewriteResponse(ctx)`             | 改整段回复                                           | `response.write`                     | 1 s / 5 s        | 原样返回                                         |
| `onStreamEvent(ctx, event, state)` | 改流式回复的每条事件                                 | `response.write`                     | 每条 50 ms / 1 s | 这条事件原样放过；同一请求里接连出错就不再交给它 |
| `onStreamEnd(ctx, state)`          | 流结束时补发最后的事件                               | `response.write`                     | 同上             | 不补发                                           |
| `onResponse(ctx)`                  | 请求结束后看回复，在请求之外运行                     | `response.read`                      | 30 s / 60 s      | 记一条失败，不影响回复                           |
| `observeTurn(turn)`                | 每轮结束后，例如写记忆                               | —                                    | 30 s / 60 s      | 同上                                             |
| `onEvent(event)`                   | 网关里发生的事，不含内容                             | `events`                             | 30 s / 60 s      | 同上                                             |
| `onSchedule(run)`                  | 按 `meta.schedules` 定时运行                         | `schedule`                           | 60 s / 300 s     | 记进插件日志                                     |
| `onHttp(request)`                  | 回答推理端口上的 `/plugins/<key>/…`                  | `http.serve`                         | 10 s / 30 s      | 回 502                                           |
| `onUiCall(call)`                   | 回答插件界面代码的调用                               | `ui.code`                            | 10 s / 30 s      | 调用失败                                         |

- 上限是墙钟时间。插件可以在 `meta.limits`
  里写它需要多久，不能超过最大值；用户在插件设置里看得到、能调。插件页「运行状态」按扩展点显示耗时分位。
- 授了
  `network`，每个扩展点都能联网，网络等待也受上限约束。入口和路由用到的外部数据，最好放进存储做缓存，由定时任务刷新。
- 多个插件服务同一个扩展点时，按插件页「执行顺序」依次运行，每个插件拿到前一个的结果。
- 每次执行都写进请求记录：做了什么（打标签、拒绝、调整路由、改请求、改回复）、耗时和失败原因。
- 看得到请求体的扩展点，拿到的是隐私检查之后的内容：脱敏时是占位符，网关在发往上游前还原；插件被用户标为受信任时才看到原文。

`ctx.request`
在各扩展点都一样：请求记录的 id、协议、模型、客户端、是否流式、会话和轮次、元信息、已有的标签、界面语言和请求体大小。各钩子的入参和返回见类型定义里的
`RequestContext`、`RouteContext`、`ForwardEdits`、`UpstreamContext`、`ResponseContext`、`StreamResult`、`ObserveContext`、`GatewayEvent`、`ScheduleRun`、`HttpServeRequest`
和 `UiCall`。

## 会话钩子

这些钩子 v1 就有，v2 里照样可用，也可以写成 `async`。

| 钩子                      | 什么时候调用                 | 返回                                                             |
| ------------------------- | ---------------------------- | ---------------------------------------------------------------- |
| `prepareSession(session)` | 会话需要常驻块时，见下文     | `{ system?, record? }`：追加到系统提示词                         |
| `prepareTurn(turn)`       | 每一轮的第一个调用           | `{ context?, skip?, choices?, record? }`：放在本轮用户消息的开头 |
| `observeTurn(turn)`       | 每轮一次，在请求之外异步执行 | `{ record? }`                                                    |
| `onAction(action)`        | 用户点了插件记录上的按钮     | `{ refresh?, message? }`                                         |
| `callTool(view)`          | 模型运行了插件的查询或上报   | `{ text, hits?, record? }`，见「允许 Agent 查询」                |
| `describeStatus(page)`    | 打开插件页的「概览」、点刷新 | 视图块数组：插件自己的状态                                       |
| `testConnection(page)`    | 点「测试连接」               | `{ status: "ok" \| "error", summary, view? }`                    |

每个钩子都是可选的。

插件页的「概览」由 AstrLink 排版：服务器地址和最近一次「测试连接」的结果、会话绑定到插件登记的属性的各个值（会话数和最近使用时间）、`credits`
里的开源项目。`describeStatus` 只需返回插件自己的状态，例如最近一次写入。

- `prepareSession`
  的结果按会话保存在内存里，之后的请求原样复用，见「常驻块什么时候重算」。
- 请求从进入网关起最多等插件 2.5 秒，隐私检查也算在里面；插件页的「最长等待」可以在 1～5 秒之间调整。`prepareTurn`
  发出的网络请求在这个时间到之前一点结束，插件可以拿已经回来的结果作答。超时的这一轮不注入，请求照常发出；晚到的
  `context` 不留给本轮后面的调用。`prepareSession`
  超时后会跑完，结果留给会话后面的请求。
- `observeTurn` 返回的 `record`
  会补进这个请求的记录；钩子出错或超时时，记录里写明原因。会话还缺插件登记的属性值时，`observeTurn`
  等这一轮的回复：回复里的标记绑定了会话，就带着新值调用；没有绑定，这一轮先留着，等会话有了值再带着它调用，`turn.late`
  为真，记录补进那时的请求；会话不纳入（值为
  `none`）时丢掉。留着的轮次只在内存里，每个会话最多前 4 轮、合计 64
  KiB，最多留 64 个会话的，网关重启后没有。
- 同一轮后面的调用（例如 agent 循环里的工具调用）不再调用
  `prepareTurn`，原样复用本轮的块。之前各轮的块按原来的位置原样补回，提示缓存不受影响；带
  `previous_response_id` 的 Responses 请求由上游保存历史，只注入本轮。
- 块放在本轮第一条用户消息里、用户自己的文字前面，排在工具结果（Anthropic 的
  `tool_result`、Gemini 的 `functionResponse`）之后。
- `turn.user.text` 是这条消息的原文，最多 200,000 个字符；`typed`
  去掉了客户端的包装，例如 `<system-reminder>`；`previous` 是上一轮的
  `typed`，最多 4 KiB。没有 `request.read`
  能力的插件读到的文本和元信息都是空的。
- 启用了隐私策略时，文本交给插件之前先按不受信任的去处检查：脱敏时插件拿到脱敏后的文本；拦截或检查不可用时，这一轮不调用
  `prepareTurn` 和 `observeTurn`。插件页可以把插件标为受信任，跳过这项检查。
- 连接测试、AstrLink 自己发起的模型调用、Claude
  Code 的 Haiku 后台请求，以及 embeddings 这类没有对话轮次的请求，都不调用这三个钩子。
- `describeStatus` 和 `testConnection` 收到
  `{ language }`，插件按界面语言写它的视图。

钩子看到的会话还有这些字段：

- `language`：`en`、`zh-CN` 或
  `ja`，会话开始时取 AstrLink 的界面语言，之后整个会话不变。插件注入的内容和记录都按它来写；桌面端切换语言，从新会话起生效。
- `session.first`：插件第一次参与这个会话的那一轮为真，也就是会话的第一轮，或插件启用后看到的第一轮。网关重启后不变。
- `workspace`：客户端请求里写着的工作目录的最后一级名字，只有 `request.read`
  才有，不给完整路径。
- `reason`：只在 `prepareSession` 里，说明这次为什么算，见下文。
- `redacted`：只在 `prepareSession`
  里，隐私策略会把找到的内容换成占位符，插件存过的数据里也会有占位符。
- `late`：只在 `observeTurn` 里，这一轮来的时候会话还没有值，现在补写，`meta`
  里是会话后来绑定的值。

`prepareTurn` 可以返回 `choices: { <属性键>: [值, …] }`：

- 每个属性最多 50 个，是本轮的块里提供给用户挑的值。
- 之后哪一轮用户的整条消息正好是其中一个（不分大小写），AstrLink当场把会话绑定到它，这一轮就带上常驻块。
- 说编号或别的说法时，仍交给模型输出标记。

### 常驻块什么时候重算

常驻块会话内不变，只在这些时候重算，请求记录里写明原因：

| `reason`  | 什么时候                                                                                            |
| --------- | --------------------------------------------------------------------------------------------------- |
| `start`   | 网关启动以来这个会话的第一个请求                                                                    |
| `bound`   | 会话绑定的值变了                                                                                    |
| `config`  | 插件版本、配置或会话语言变了                                                                        |
| `compact` | 同一个系统提示词下，历史开头的第一条消息变了，或 Codex 留下了新的 compaction 条目；最多 10 分钟一次 |
| `manual`  | 用户点了「刷新」或插件按钮让会话刷新                                                                |
| `auto`    | 插件设置打开了自动刷新时，每轮第一个调用最多每 10 分钟查一次                                        |

- `compact`
  只在本次的等待时间内算完才换上；没赶上就照旧用原来的块，结果也不留给之后的请求。
- `auto` 不让请求等，算完后内容变了才在后面的请求换上。
- 其他原因算完时请求已经不等了，结果留给后面的请求，由第一个带上它的请求在记录里写明。
- 子代理和主对话共用会话、但系统提示词不同，所以切换子代理不算 `compact`。

### 用户的控制

- 插件页顶部可以一键暂停所有插件：暂停期间请求原样发出，每一轮第一个请求的记录写明原因。
- 每个插件可以关掉一部分客户端（`$clients`）。识别不出客户端的请求算「其他」。
- 会话详情可以暂停某个插件在这个会话里的工作：不装载、不附查询方法、不写入，记录写明原因。
- 插件页可以关掉插件的查询（`$lookup`），也可以在「数据存储」里作废这个插件的全部查询凭证；绑了开关（`setting`）的工具可以单独关掉，例如上报。
- 所有插件注入的内容合计不超过注入总预算（默认 16
  KiB），超出时按插件 key 的顺序丢掉后面插件的块，记录写明「超出注入总预算」。
- 会话详情里可以看到当前常驻块的原文。它只在内存里，网关重启后要等下一个请求算出。
- 插件页概览的「效果」和运行状态页的按天明细，汇总本机请求记录里这个插件的记录：装载次数和原因、注入的字节数和等待时间、查询次数和有结果的比例（看
  `callTool` 返回的
  `hits`）、会话怎么绑定的、写入成败（含模型的上报）、失败和超时等原因，以及带过插件内容的会话和其他会话的缓存命中率。只读次数、大小、耗时和原因，不读对话内容，也不上报。

`system` 和 `context` 可以是一段文字，也可以是
`{ open?, items, close? }`。条目按顺序装入，超出 8
KiB 的条目会被丢掉，其余照常注入。同一会话里，`system`
的内容应保持不变，否则提示缓存要整段重写。

### 允许 Agent 查询

插件可以在 `meta.tools`
里登记只读查询，让模型需要细节时自己去查；也可以登记上报，让模型把值得记住的东西交给插件。插件只登记名字、说明和参数；地址和凭证由 AstrLink 生成，插件碰不到。

```ts
tools: [
  {
    name: "search",
    description: {
      "zh-CN": "检索本项目的记忆。",
      en: "Search this project's memory.",
    },
    params: [
      {
        name: "query",
        type: "string",
        required: true,
        maxLength: 200,
        example: { "zh-CN": "要查的内容", en: "what to look up" },
      },
      { name: "limit", type: "number", min: 1, max: 10 },
    ],
  },
  {
    name: "projects",
    description: "List the projects.",
    unbound: true,
    params: [],
  },
  {
    name: "note",
    description: "Note what is worth keeping about this project.",
    writes: true,
    setting: "report",
    params: [
      {
        name: "text",
        type: "string",
        required: true,
        maxLength: 1000,
        example: "what to note",
      },
    ],
  },
],
```

- `description` 和 `example`
  可以是一段文字，也可以按语言分别写（`en`、`zh-CN`、`ja`），按会话语言取，没有时取英文。
- 参数只能是字符串或数字。字符串去掉首尾空白，默认最多 500 个字符，`maxLength`
  最多 2000；数字可以写 `min`、`max`。必填参数要写
  `example`，最多 100 个字符。没有登记的参数一律拒绝。
- 查询默认要等会话有了插件登记的每个属性的值，查的是会话绑定的那个值，由 AstrLink 按会话确定，不由模型传。`unbound: true`
  的查询在会话还没有值时也能用，例如列出全部项目。
- `writes: true`
  是上报：模型把要长期记住的东西交给插件，而不是查。上报要等会话有值，不能同时
  `unbound`；命令用 `POST`（`curl` 不带
  `-G`），用法里不标「只读」，并提醒模型不要写密钥、密码等敏感信息；回答不加「查询结果」那一行。
- `setting` 写插件自己 `settings` 里一个 `switch`
  的键：开关打开时（没设过就取它的
  `default`）才附上和接受这个工具，用户可以单独关掉它。

**什么时候告诉模型。**
同时满足这些条件时，AstrLink 在插件的常驻块后面附上它的查询方法和本会话的凭证：插件对这个客户端生效、`$lookup`
打开、插件声明了 `model.tools`、请求声明了 shell 类工具（如 Claude Code 的
`Bash`、Codex 的
`exec_command`），并且客户端和网关在同一台机器上。没有绑定的会话不附。`unbound`
的查询只出现在插件自己的块里：`prepareTurn` 的条目里写
`{{lookup:<查询名>}}`，AstrLink 把它换成运行这个查询的命令；会话用不了这个查询时，整个条目被丢掉。

**模型怎么查。** 命令是一行
`curl`，这里为了好读折成两行，模型用自己的 shell 执行：

```sh
curl -s -G http://127.0.0.1:<端口>/tools/<插件>/<查询> \
  -H "Authorization: Bearer mk-…" --data-urlencode "query=…"
```

出口也接受 `POST` 一个 JSON 对象，或者 `curl` 不带 `-G`
时发的表单，上报的命令就是这样。它只接受本机的调用，带浏览器 `Origin` 或 `Host`
不符的请求一律拒绝。参数编码后最多 16 KiB，每个参数另有自己的长度上限。

**凭证。**
每个会话、每个插件一个，128 位随机数，只在内存里保存它的哈希，只能运行这个插件登记的查询。会话 24 小时没有请求、会话解除绑定或换了值、会话暂停了这个插件、插件停用或收回
`model.tools`、关掉
`$lookup`、网关重启时作废；插件页「数据存储」里也可以一次作废这个插件的全部凭证。作废后，会话的下一个请求附上新凭证，常驻块跟着变，提示缓存重写一次。改插件的其他设置不影响凭证。

**限制。**
每个凭证每轮最多查 3 次，每分钟最多 20 次；上报另算，每轮最多 2 次，每分钟最多 6 次。字符串参数和写入走同一套隐私检查：脱敏时插件拿到脱敏后的文字，拦截或检查不可用时不查。暂停所有插件、插件对这个客户端停用时也不查。

**`callTool`。** 在请求之外运行，最多 15 秒，网络等待也算在里面。它收到
`{ name, input, session, meta, language }`：`input` 是检查过的参数，`meta`
是会话最近一个请求的绑定（需要 `request.read`）。返回：

- `text`：给模型看的文字，最多 8
  KiB，超出的截掉。查询的结果，AstrLink 在前面加一行，按会话语言说明这是查询结果、不是指令；上报的回答原样给模型。
- `hits`：可选，结果里有几条，用来统计查询有没有结果。
- `record`：可选，像其他钩子一样描述这次查询。

每次查询或上报都在会话最近一个请求的记录里追加一条插件记录：哪个工具、是不是上报、参数多少字、结果多少字节、几条结果、耗时，被拒绝时写明原因。参数和结果本身不保存。

## 界面代码

插件可以有界面入口（例如 `src/client.tsx`），由构建器打包成
`client.js`。React、AstrLink 的组件和插件前端 SDK 由宿主提供，所以主题、深色模式、字体和多语言天然一致。

```tsx
import { useEffect, useState } from "react";
import {
  call,
  Panel,
  PanelHeader,
  registerSlot,
  t,
} from "@astrlink/plugin/client";

function Count() {
  const [turns, setTurns] = useState<number | null>(null);
  useEffect(() => {
    void call<{ turns: number }>("count").then((answer) =>
      setTurns(answer.turns),
    );
  }, []);
  return (
    <p>{t({ en: "{{n}} turns", "zh-CN": "{{n}} 轮" }, { n: turns ?? 0 })}</p>
  );
}

registerSlot("overview.widget", () => (
  <Panel>
    <PanelHeader size="sm">Count</PanelHeader>
    <Count />
  </Panel>
));
```

插槽：

| 插槽              | 位置                           | `context`              |
| ----------------- | ------------------------------ | ---------------------- |
| `page`            | 侧边栏「插件」下面的独立页面   | —                      |
| `plugin.tab`      | 插件页里这个插件自己的标签页   | `{ plugin }`           |
| `overview.widget` | 概览底部的卡片                 | —                      |
| `request.tab`     | 请求记录里会话详情的标签页     | `{ session, request }` |
| `session.section` | 会话详情「插件」标签页里的区块 | `{ session }`          |
| `service.section` | 提供商「连接」标签页里的区块   | `{ service }`          |

SDK 还提供：`call(name, input)` 调用插件后端的
`onUiCall`（后端在网关里运行，受它的时间和内存上限约束）；`getSettings`、`saveSettings`
读写插件设置；`t()`
按界面语言取文字；`language()`、`useLanguage()`、`useTheme()`；`navigate()`
跳到 AstrLink 的页面或插件自己的页面；`toast()` 显示提示条；以及
`Button`、`Panel`、`Field` 等共用组件。

界面代码和 AstrLink 界面同等权限：能看请求记录、改设置，凡是界面能做的都能做。所以：

- 需要 `ui.code`，安装审查里用红字写明有界面代码和它的风险。
- 主窗口的 CSP 不放开，界面代码不能直接访问外网；要联网只能经过插件后端的
  `onUiCall`。
- 设为受信任、装新插件或换代码，只在「插件确认」窗口里生效，主窗口里的代码点不到它。
- 每个插件组件外面都有错误边界，出错只影响它自己；一分钟内出错 3 次，就自动关掉这个插件的界面。
- 每个插件的界面可以在插件页单独关掉；插件页顶部的「插件界面」关掉就是安全模式，所有插件界面都不加载。上次加载插件界面后窗口没能正常运行时，下次启动自动进入安全模式。
- 代码固定在安装时的提交上，更新要用户看过审查后确认。
- 网页控制台默认不加载插件界面，要在插件页打开并确认。

## 供应商适配器

插件可以把一种上游 API 接成提供商，社区不改 Core 就能接入新的模型 API。用户添加提供商时多出「插件提供商」一类，之后它和普通提供商一样参与路由、故障切换、统计和隐私检查。

```ts
export const meta = {
  apiVersion: 2,
  key: "acme",
  name: "Acme",
  version: "1.0.0",
  capabilities: ["provider"],
  providers: [
    {
      id: "acme",
      name: "Acme AI",
      baseUrl: "https://api.acme.example/v1",
      auth: "bearer",
      protocols: { "openai.chat": { stream: true } },
      convert: true,
      models: ["acme-1"],
    },
  ],
};
```

- `protocols` 写插件接哪些协议（Chat
  Completions、Responses、Messages、Gemini），每种是否支持整段回复（`sync`，默认支持）和流式（`stream`）。客户端要的模式插件不支持时，这个提供商不进候选。
- `convert: true`
  让网关先用自己的转换器把其他协议转成插件接的协议，回来时再转回去；不写就不接这类请求。
- `auth` 写网关怎么发密钥：`bearer`、`x-api-key`、`google`、`none` 或
  `{ header: "X-Acme-Key" }`。密钥由用户填在提供商上，插件看不到。

钩子：

| 钩子                                  | 做什么                                                                                             |
| ------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `buildRequest(ctx)`                   | 返回上游请求：`method`、`url`（基础地址后的路径，或同一主机上的地址）、`headers`、`body`、`stream` |
| `parseResponse(ctx, res)`             | 整段回复：返回 `{ json }`，即 `ctx.protocol` 的回复，用量放在协议本来的位置                        |
| `parseStreamEvent(ctx, event, state)` | 流式：每条上游事件调用一次，返回 `{ events, state, done }`                                         |
| `finishStream(ctx, state)`            | 可选，流结束时补发收尾事件，例如 `[DONE]`                                                          |
| `parseError(ctx, res)`                | 可选，把上游错误转成 `{ status, code, message, retryable }`；`retryable` 决定能不能故障切换        |

- `ctx.body` 是隐私检查和转发前修改之后、本来要发往上游的请求体，用
  `ctx.protocol` 的格式；`ctx.settings` 是插件的设置。
- 网关负责 HTTP、出网代理、超时、故障切换和两边的 SSE 分帧：上游的 SSE 或每行一个 JSON 逐条交给
  `parseStreamEvent`；插件返回的事件写成 SSE，`data`
  是字符串时原样写，其余写成 JSON。
- 请求只能发往提供商地址所在的主机；插件设的凭据头和 `X-AstrLink-*`
  头一律去掉，上游认不出 AstrLink。
- 隐私还原、计价（插件只按协议报用量）和请求记录照常由网关完成。
- 获取模型时，`buildRequest` 收到的 `ctx.purpose` 是
  `models`：返回列表请求，`parseResponse` 返回 `{ models }`；返回 `null`
  表示不支持，用户手动填模型。
- 声明了流式就必须导出
  `parseStreamEvent`；导出了用不到的适配钩子，插件拒绝加载。
- 时间上限：构造和解析一次 1 秒（最多 5 秒），流式每条事件 50 毫秒（最多 1 秒）；超时或出错按上游失败处理，可以故障切换。

## new-api 的任务插件

AstrLink 也能运行 [new-api](https://github.com/QuantumNous/new-api)
的任务插件（v1），接入视频、图片和音乐生成。new-api 官方插件市场里的插件不用改就能装。

- 一个任务插件就是一个 `plugin.js`：导出 `meta` 和一组同步钩子，不能
  `import`。目录里只有它、没有 `package.json`，并且导出 `buildSubmitRequest`
  时，AstrLink 把它当作任务插件：原样安装，不经过构建，文件的 sha256 和市场列出的一致。
- 任务插件运行在单独的配置里：没有 `astrlink.*` 和 Node 的模块，有 new-api 的
  `utils`（JSON、Base64、HMAC、JWT、火山引擎签名和只读的 `utils.fetch`）和
  `console`。时间、内存和输入大小照样有上限。
- 插件只做数据转换：选提供商、发请求、存任务、查询进度和下载结果都由网关完成。插件构造的请求只能发往提供商的地址和插件声明的主机，密钥由网关加上。
- 插件页把任务插件和供应商适配器一起放在「供应商」分组里。

### 安装任务插件

- 插件页「插件市场」读官方市场的
  `index.json`，也可以换成其他市场的地址。读取走 raw 地址，不占 GitHub 接口次数。
- 安装前按市场列出的 sha256 校验文件，再按 v1 的规则检查清单和导出。
- 市场的索引写明了插件有哪几部分（`task`、`provider`、`transform`）。三部分都能安装；只有 AstrLink 还不运行的部分才标为「不兼容」，不能安装。
- 「安装插件」里也可以粘贴 GitHub 上单个 `plugin.js`
  的链接；官方插件仓库里的链接认作市场安装，同样校验。
- 插件的图标（`iconFile`、旁边的 `icon.svg` 或
  `icon.png`，都没有时用清单里写的 LobeHub 图标）和更新说明（`CHANGELOG.md`
  及各语言的翻译）一起装上。更新、回滚和卸载和其他插件一样。

### 任务插件的提供商

启用插件后，在提供商页添加提供商，在「插件」一组里选它：

- 地址默认用插件声明的 `baseUrl`；模型取插件声明的模型，可以增删。
- 密钥填上游的 API Key。用服务账号的插件（如 Vertex
  AI）粘贴服务账号 JSON，网关拿它换访问令牌，不交给插件。
- 插件支持时可以打开「上游是另一个 New
  API 网关」：地址填那个装了同一插件的网关，密钥填它的令牌。
- 提供商接哪些接口由插件决定，网关自动设置，编辑器里没有「入口协议」。

### 客户端怎么调用

和其他接口一样带 AstrLink 的访问令牌：

| 接口                                                                                                                            | 做什么                                                     |
| ------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `POST /v1/videos`、`GET /v1/videos/{id}`、`GET /v1/videos/{id}/content`、`POST /v1/videos/{id}/remix`                           | OpenAI 的视频接口：创建、查询、下载和再创作                |
| `POST /v1/images/generations`、`POST /v1/images/edits`                                                                          | OpenAI 的图片接口，任务完成后一次返回，最多等 10 分钟      |
| `POST /v1/responses`、`GET /v1/responses/{id}`                                                                                  | Responses 接口的流式、同步和后台（`background: true`）用法 |
| 插件声明的原生接口                                                                                                              | 厂商自己的请求格式和路径，同样的接口也在 `/{插件 key}` 下  |
| `POST /v1/tasks/{插件 key}`、`GET /v1/tasks/{id}`、`GET /v1/tasks/{id}/artifacts`、`GET /v1/tasks/{id}/artifacts/{key}/content` | 通用的任务接口                                             |

- 请求里的模型决定由哪个插件处理；同一模型有多个提供商时按提供商路由挑选，提交时上游出错可以故障切换。
- 原生接口和 new-api 一样，用厂商自己的路径，例如豆包的
  `POST /api/v3/contents/generations/tasks`，同时在 `/doubao/api/v3/...`
  下提供；路径的第一段已经是插件 key 或它的前缀（阿里百炼的
  `/ali/...`）时不另加。网关自己的接口优先：和 `/v1/chat/completions`
  这类路径相同的原生接口只在 `/{插件 key}` 下提供；`/v1/videos/{id}`
  这类带任务 ID 的路径只认网关的任务 ID，所以 `/v1/videos/text2video/{id}`
  仍归插件。`/v1/tasks`、`/plugins`、`/tools`、`/control`、`/console`、`/assets`、`/astrlink`
  不能声明，插件 key 也不能是 `api`、`v1`、`v1beta` 或这些路径的第一段。
- 查询接口的任务 ID 在路径参数里，路径没有时读同名的查询参数（如 MiniMax 的
  `?task_id=`）。原生接口提交的任务，`GET /v1/videos/{id}`
  由插件自己匹配的查询接口作答。
- 原生接口的解析钩子收到
  `requestHeaders`（不含带凭据的头）；查询接口的展示钩子收到
  `ctx.artifacts`，抛出 `artifact_not_found` 时回 404；展示钩子可以返回
  `{ __status, __headers, __body }` 按厂商自己的状态码作答，头只能设 `Location`
  和 `Retry-After`。
- `buildContentRequest` 可以返回
  `contentPath`：产物是长任务应答 JSON 里的 base64，网关取出后按 `mimeType`
  下载；任务没完成回 409，没有内容回 410。
- 任务存进数据库，网关在后台按插件的方式逐个或批量查询进度。结果文件经网关下载：返回给客户端的下载地址带
  `access` 参数，不带令牌也能下载。
- 插件页「任务」页签列出最近的任务，可以清除已完成的任务，网关保存的结果文件一起删除。
- 请求记录只记提交那一次，查询和下载不记。任务请求不经过隐私检查。

### 用例

`astrlink plugin test` 认 new-api 的用例格式
`{name?, hook, member?, args, expected?, expectedError?}`，同一份用例两边都能跑：

```sh
astrlink plugin test plugin.js --fixture golden.json
```

`member` 是 `native` 里的函数，或 `protocols` 里某个协议的函数，如
`openai_responses.decodeRequest`；`expected` 要求结果相等，`expectedError`
要求失败的消息包含这段文字。不写 `--fixture` 时跑
`tests/*.json`。`astrlink plugin check plugin.js` 按网关加载的方式检查。

## 宿主函数

这些函数只在 AstrLink 调用钩子的时候能用；在模块顶层调用会抛出
`TypeError`。apiVersion 2 里，`astrlink.http.fetch`、`astrlink.store.*` 和
`astrlink.meta.values` 返回 Promise，要 `await`；其余函数照旧同步返回。

### `astrlink.http.fetch(request)` 或 `astrlink.http.fetch([request, …])`

- 可以访问任何地址，经网关的出网代理发出。
- 请求带上 `credential: "<凭据位>"`
  时，AstrLink 只在请求发往该凭据绑定的服务器时把它写进请求头，插件读不到凭据的值；本机以外的明文
  `http://` 服务器需要用户对这个地址单独同意。
- 插件可以自己设置 `Authorization`、`Cookie`、`User-Agent`
  等请求头；`Host`、`Content-Length`、`Connection` 这些由传输决定的头，以及
  `Proxy-*`、`X-AstrLink-*`，由网关去掉。插件没设 `User-Agent`
  时不发送；不跟随重定向，3xx 原样返回。
- 请求体用 `json`（作为 `application/json` 发送）或 `body`，不能同时有，`GET`
  不带请求体，最多 1 MiB。
- 传数组时并行执行，一次最多 8 个；v1 的每次钩子调用最多 16 个，v2 的最多 32 个，可以用
  `Promise.all`
  同时等。单个请求默认 10 秒超时，最多 30 秒，钩子的时间上限会更早截止。等网络的时间不计入脚本时间。
- v2 还提供标准的 `fetch(url, init)` 和 `node:http(s)`，按同样的规则访问网络，和
  `astrlink.http.fetch` 共用请求数和网络时间的预算；`fetch` 返回标准的
  `Response`，响应体按流到达，`init.credential` 指定凭据位，`init.timeoutMs`
  是单个请求的超时。请求没有得到响应时，`fetch` 拒绝为
  `TypeError`，网关拒绝或按它的规则失败的（见下面的 `error`）在
  `error.cause.code` 里给出原因。
- 响应：`status`（没收到响应时为 0）、`headers`（每个头的第一个值，键是小写）、`json`
  或 `text`（最多 1 MiB）、`took_ms`，以及出错时的
  `error`：`denied`（凭据不能发往这个地址）、`timeout`、`network`、`too_large`、`budget`。HTTP 错误码照常返回，不算
  `error`。

### 其他函数

| 函数                                          | 说明                                                                                                                                                     |
| --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `astrlink.store.get/set/delete/keys(prefix?)` | 插件私有的键值存储，保存在 AstrLink 的设置里；值是 JSON，单个最多 64 KiB，总共最多 1 MiB                                                                 |
| `astrlink.config.get()`                       | 插件配置，一段 JSON 对象，最多 64 KiB；每次返回新的副本，插件改不了它                                                                                    |
| `astrlink.request.info()`                     | 本次请求的协议、模型、客户端和元信息，不含正文和凭据；不在请求里时为 `null`                                                                              |
| `astrlink.meta.values(key, limit?)`           | 插件自己登记的属性各个值的使用情况：`value`、`sessions`、`lastUsedAt`（毫秒，没有为 0），最近用过的在前；`limit` 为 1～200，默认 50。需要 `request.read` |
| `astrlink.util.sha256(text)`                  | 文本的 SHA-256，小写十六进制                                                                                                                             |
| `astrlink.log(level, message)`                | `debug`、`info` 或 `warn`；每次调用最多 20 条，每条最多 1 KiB                                                                                            |
| `astrlink.clock.now()`                        | 当前时间，Unix 毫秒                                                                                                                                      |
| `astrlink.clock.offset(at?)`                  | 网关所在时区在某一时刻（Unix 毫秒，默认现在）比 UTC 快多少分钟。插件里的 `Date` 按 UTC 计算，写日期时用它换成用户的日历                                  |

## 运行限制

| 项       | 限制                                                               |
| -------- | ------------------------------------------------------------------ |
| 脚本时间 | 每次调用 200 ms，等网络、存储和定时器的时间不计；供应商适配器 1 秒 |
| 内存     | 运行时保留的内存 32 MiB，随用随丢的不算；供应商适配器 256 MiB      |
| 返回值   | 最多 64 KiB、4096 个值；供应商适配器 64 MiB                        |
| 定时器   | v2 每次调用同时最多 64 个，调用结束时一并取消                      |
| 时间上限 | 每个扩展点各自的上限，见「扩展点」                                 |
| 文件     | 数据目录最多 1 GiB；同时打开的文件最多 256 个                      |
| 动态代码 | `eval` 和 `new Function` 一次最多编译 1 MiB 的源码                 |
| 内置对象 | 每个运行时一份，插件可以修改，不影响别的插件                       |

超出限制时，这次调用的结果被丢弃，请求照常进行。出错的运行时不会再被使用，下次调用用新的。

## 视图

钩子返回的视图由 AstrLink 用自己的组件渲染，所有文字都按纯文本显示，插件不能带 HTML、脚本和链接。

| 块       | 写法                                                                              |
| -------- | --------------------------------------------------------------------------------- |
| `kv`     | `{ type: "kv", items: [{ label, value, mono? }] }`                                |
| `table`  | `{ type: "table", columns: [...], rows: [[...], ...] }`，单元格是文字或数字       |
| `badges` | `{ type: "badges", items: [{ text, tone? }] }`，`tone` 为 `ok`、`warn` 或 `error` |
| `text`   | `{ type: "text", text }`，可以换行                                                |

一个视图最多 32 块、16
KiB；表格最多 200 行，单个文字最多 1024 个字符。超出的部分会被截掉，并标为已截断；不认识的块会被丢掉。

`record` 是钩子对本次请求的描述，显示在请求轨迹的网关泳道里：

- `title`：事件名，例如「记忆」，最多 32 个字符
- `status`：`ok`、`skipped` 或 `failed`
- `summary`：一行摘要，最多 256 个字符
- `view`：可选的视图
- `actions`：可选的按钮，最多 3 个：`id`（小写字母开头，最多 32 个字符）、`label`（最多 16 个字符）、可选的
  `confirm`（点下去之前先确认的话，最多 300 个字符）

用户点了按钮，AstrLink 调用
`onAction({ action, session, meta, language })`，`meta` 是这个会话的绑定（需要
`request.read`）。返回 `refresh: true` 时，会话的下一个请求重算常驻块；`message`
显示给用户，最多 256 个字符。

## 元信息

元信息是给请求打上的自定义属性，例如项目或环境。属性可以在「设置 → 元信息」里自己定义，也可以由插件在
`meta.metadata`
里登记。插件登记的属性，用户只能设值，不能修改或删除；插件被移除以后才可以删除。

每个请求按以下顺序取值，先找到的为准：

1. 请求头 `X-AstrLink-Meta-<键>`，键里的下划线写成短横线。
2. 会话绑定：模型回复或用户发出单独一行的
   `<标记>值</标记>`、用户的回答正好是插件提供的某个值、会话详情里手动设置。
3. 工作区：会话还没有值时，按客户端请求里的工作目录取这个目录上次绑定的值，存为会话绑定。
4. 访问令牌的默认值，在「令牌」详情里设置。

例如项目对应的请求头是
`X-AstrLink-Meta-Project`。AstrLink 在转发前会去掉这些请求头。但不要把它们写进提交到共享仓库的客户端配置：别人的请求如果不经过网关，它们会原样发给上游。

不符合属性校验规则的值视为没有，并在请求记录里留下提示。`none`
是保留值，表示这个请求或会话不纳入。插件在钩子里通过 `turn.meta.<键>`
读到值和来源。

会话绑定的规则：

- 标记行要单独占一行，去掉首尾空白后整行就是
  `<标记>值</标记>`；代码块里的、不符合校验规则的不算，请求记录里会写明被忽略的原因。一条回复里只认第一行。
- 模型的标记只在会话还没有值时生效，从下一个请求起使用，这一轮的 `observeTurn`
  在回复结束后带着新值调用；会话已经有值（包括
  `none`）时，模型再输出也只记为已忽略。用户单独发一行标记，可以随时改绑，立即生效，这条消息本身不交给
  `prepareTurn` 和 `observeTurn`。
- 会话没有存值时（例如客户端换了会话），AstrLink 会在客户端重放的历史里按同样的规则找回绑定。
- 标记里的值和已有的值（其他会话绑定过的、插件提供给这个会话的）只差大小写、短横线或下划线时，绑定到已有的值，记录里写明原来的写法。
- 谁都没用过的值记为「新建」。不符合校验规则、但改成小写就符合的值按小写绑定。
- 工作区：
  - AstrLink 从系统提示词和前两条用户消息里认出工作目录：Claude
    Code、OpenCode 的 `Working directory:`，Codex 的 `<cwd>`，Gemini
    CLI，Cline 一类的 `Current Working Directory`。认不出时照常询问。
  - 只存目录路径用设备密钥算出的指纹和最后一级目录名，不存路径。
  - 会话第一次由标记或回答绑定时，记下「这个目录 → 这个值」；会话详情里改值时，可以选择让目录也跟着改。
  - 插件可以用 `$workspace` 关掉。
- 「请求」页的会话详情可以查看、修改和解除绑定。解除会存为
  `none`，所以历史里的旧标记不会把绑定找回来。

## 出错时

插件编译、加载或校验失败时，只有这个插件停用，插件页显示原因和行号，其他插件和请求不受影响。钩子抛错、超时或超出内存时，这一次的结果被丢弃，按「扩展点」表里的方式处理，请求记录里写明原因。

同一个扩展点一分钟内失败 5 次，会熔断一分钟：这段时间里请求不再交给它，插件页「开发」页签写明到什么时候。

## 开发和调试

- 插件页的「开发」页签有插件的实时日志：`console`、`astrlink.log`、失败的调用和调用栈。
- 同一页签列出每个钩子最近 256 次调用的耗时分位和 JS 时间。
- 「试运行」选一个钩子、填参数跑一次，不访问网络，也不写请求记录。
- 多文件项目构建时生成 source map，出错时调用栈对回 TS 源码的行号。
- `astrlink plugin dev`
  构建出的开发版在插件列表里标为「开发版」，源码改了自动重新加载。

### 测试用例

`astrlink plugin test` 跑 `tests/*.json`
里的用例。一个文件可以是一个用例、用例数组，或者带 `cases`
的对象：对象里的其他字段是这些用例共用的设置，用例自己写了的字段以自己的为准。

| 字段                   | 说明                                                                                                                                     |
| ---------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `name`、`hook`、`args` | 用例名、要调的钩子和参数                                                                                                                 |
| `config`               | 插件的设置。插件申请的能力全部生效，旧用例里的 `capabilities` 读取后不起作用                                                             |
| `files`                | 调用前数据目录里的文件：路径到文本                                                                                                       |
| `request`              | `astrlink.request.info` 的返回值                                                                                                         |
| `store`                | 调用前存储里的值                                                                                                                         |
| `now`、`timeZone`      | 时钟的时间（RFC 3339）和时区（如 `America/New_York`），不写时用真实时间和本机时区                                                        |
| `metaValues`           | `astrlink.meta.values` 按属性返回的值                                                                                                    |
| `fetch`                | 伪造的应答，见下；用例自己的先匹配，再匹配文件共用的                                                                                     |
| `expect`               | `result` 要求结果相等，`match` 要求包含；`error` 要求失败；`store` 是调用后存储里的值；`files` 是调用后数据目录里的文件，`null` 表示没有 |

- `fetch` 的每一项按 `method` 和 `url`（以 `*` 结尾时按前缀）匹配请求；写了
  `body` 时，请求的 JSON 正文还要包含这些字段。
- 应答写 `status`、`headers`、`json` 或 `text`；`error`
  按宿主的方式让请求失败；`delayMs` 推迟应答，也是请求的耗时。
- 写了 `times`
  的一项正好应答这么多次：之后的请求交给下一个匹配的项，次数不够时用例失败。同一地址先失败后成功、只许请求一次，都这样写。
- 没有任何一项应答的请求让用例失败。`expect.store` 里写 `null` 表示没有这个键。
