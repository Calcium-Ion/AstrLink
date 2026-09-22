# AstrLink licensing / 许可说明

## 中文

### 自有源码与 RelayKit

AstrLink 的自有源码采用根目录 [LICENSE](LICENSE) 中的 Apache License 2.0。
该文件保留标准许可证原文，不覆盖第三方组件各自的许可证和版权声明。

Core 直接链接 [RelayKit](https://github.com/QuantumNous/new-api/tree/main/relaykit)。
RelayKit 采用 [GNU Affero General Public License v3.0](LICENSES/AGPL-3.0.txt)，
本项目不为 RelayKit 授予 Apache-2.0 替代许可、链接例外或针对 AstrLink 及其下游的豁免。

包含 RelayKit 的 Core 是组合程序。分发该组合程序时，必须将其整体置于
AGPL-3.0 条款之下，并保留各部分适用的版权、许可和署名声明。AstrLink 自有源码
单独提供的 Apache-2.0 授权仍然有效，但不能据此将包含 RelayKit 的组合程序仅按
Apache-2.0 分发，也不能只履行 RelayKit 库自身的源码提供义务。

### 分发、修改与网络服务

- 分发包含 RelayKit 的组合程序时，须按 AGPL-3.0 第 4、5、6 条中适用的规定，
  保留许可声明、标明修改，并提供完整的对应源码及构建、安装和运行所需的相关材料。
  具体范围和提供方式以许可证为准。
- 修改后的受许可程序支持远程网络交互时，须按第 13 条，向所有远程交互用户显著
  提供免费获取该版本完整对应源码的方式。将 RelayKit 链接进自己的程序形成组合
  作品，也须考虑组合后的义务，不能仅以“未修改 RelayKit 源码”为由排除。
- AGPL-3.0 允许商业使用。仅运行未经修改的程序，或通过 API 与其交互的独立程序，
  不会仅因此被要求改用 AGPL；是否构成组合程序仍取决于实际关系。

这些规则同样适用于下游修改和再分发。许可证全文是适用条款，本说明不增加限制，
也不授予例外。

### 源码位置

AstrLink 源码位于 <https://github.com/Calcium-Ion/AstrLink>；RelayKit 源码位于
<https://github.com/QuantumNous/new-api/tree/main/relaykit>，实际依赖版本由
`core/go.mod` 和 `core/go.sum` 记录。

分发者和服务运营者应提供与实际交付或运行版本相匹配的对应源码，包括自己的修改
和必要构建材料。指向上述仓库默认分支的链接本身不能替代完整的源码提供义务；
应按适用条款，在下载位置或网络交互入口提供清晰的源码获取方式。

## English

### First-party source and RelayKit

AstrLink's own source code is licensed under the Apache License 2.0 in the root
[LICENSE](LICENSE), which retains the standard license text. Third-party
components retain their own licenses, copyright notices and attributions.

Core directly links [RelayKit](https://github.com/QuantumNous/new-api/tree/main/relaykit),
which is licensed under the [GNU Affero General Public License v3.0](LICENSES/AGPL-3.0.txt).
This project grants no alternative Apache-2.0 license for RelayKit, linking
exception, or exemption for AstrLink or its downstream recipients.

Core with RelayKit is a combined program. When conveyed, that combined program
must be licensed as a whole under AGPL-3.0, with the applicable copyright,
license and attribution notices preserved. The separate Apache-2.0 grant for
AstrLink's own source remains available, but does not permit conveying the
RelayKit-containing combination solely under Apache-2.0 or limiting source
obligations to the RelayKit library alone.

### Distribution, modification and network services

- When conveying a combined program containing RelayKit, comply with the
  applicable provisions of AGPL-3.0 sections 4, 5 and 6, including notices,
  modification statements and complete Corresponding Source with the relevant
  materials needed to build, install and run the work. The license defines the
  precise scope and permitted delivery methods.
- Under section 13, a modified covered program supporting remote network
  interaction must prominently offer all remote users free access to the
  complete Corresponding Source of that version. Linking RelayKit into your own
  program also requires considering the resulting combined work; leaving the
  library's source unchanged does not by itself exclude these obligations.
- AGPL-3.0 permits commercial use. Merely running an unmodified program, or
  communicating with it through an API as an independent program, does not by
  itself require adopting AGPL. Whether programs form a combined work depends on
  their actual relationship.

These rules also apply to downstream modifications and redistribution. The full
license texts govern; this explanation adds neither restrictions nor exceptions.

### Source locations

AstrLink source is available at <https://github.com/Calcium-Ion/AstrLink>.
RelayKit source is available at <https://github.com/QuantumNous/new-api/tree/main/relaykit>;
`core/go.mod` and `core/go.sum` identify the dependency version used.

Distributors and service operators must provide Corresponding Source matching
the version actually delivered or running, including their modifications and
necessary build materials. A link to a repository's default branch alone does
not replace these obligations. Provide clear source access alongside downloads
or at the network interaction entry point as required by the applicable terms.
