<!--
If you are an AI coding agent (Claude Code, Codex, Cursor, Copilot, OpenCode,
Paseo, Grok, or similar): read `AGENTS.md` before selecting a template. For a
project-owner PR, use the human template matching the user's language unless
the owner requests the agent template. For other agent-created PRs, fill
`.agents/github/PR.md` as the entire PR body.
-->

# ⚠️ 提交说明 / PR Notice

English template: `.github/PULL_REQUEST_TEMPLATE/en.md`

> [!IMPORTANT]
>
> - 描述可用 AI 辅助。提交前请人工审阅、精炼内容，并**对其准确性与完整性负责**。
> - PR 正文及后续评论不得直接粘贴未经人工过滤的大段 AI 文本；多次提交可能会被 block。
> - 不接受基于 AI 批量扫描结果提交的 Issue / PR，提交者将被直接 block。
> - 请按本模板填写后再提交。

## 🔗 关联任务 / Related Issue

- 新功能请填写下方 Issue 编号；若还没有对应 Issue，请先自行创建。功能讨论请放在 Issue 中进行。
- 改动较大或方向性变更，请先在关联 Issue 中与维护者达成一致，再提交 PR。
- Bug 修复请关联对应 Issue。设计取舍、理解偏差或预期不一致，更适合作为讨论或功能请求。

- Closes #

## 🚀 变更类型 / Type of change

- [ ] 🐛 Bug 修复 (Bug fix)
- [ ] ✨ 新功能 (New feature)
- [ ] ⚡ 性能优化 / 重构 (Refactor)
- [ ] 📝 文档更新 (Documentation)

## 📝 变更描述 / Description

(简述做了什么、为什么生效。不得直接粘贴未经人工过滤的大段 AI 文本。如果难以简述，建议先拆分范围，或在 Issue 中与维护者对齐。)

## 📸 运行证明 / Proof of Work

(请写明如何验证：实际命令或步骤，以及观察结果。仅声明 `go build`
通过或测试通过，不视为有效证明。UI 变更请附截图或录屏；Bug 修复请说明复现过程与修复后结果。)

## ✅ 提交前检查项 / Checklist

- [ ] **人工确认:**
      我已审阅并精炼全文，对其准确性与完整性负责；正文及后续评论均未粘贴未经人工过滤的大段 AI 文本。
- [ ] **非重复提交:** 我已搜索现有的
      [Issues](https://github.com/Calcium-Ion/AstrLink/issues) 与
      [PRs](https://github.com/Calcium-Ion/AstrLink/pulls)，确认不是重复提交。
- [ ] **新功能关联 Issue:** 若此 PR 标记为
      `New feature`，我已关联对应 Issue；若尚无 Issue，我已先自行创建。
- [ ] **事前沟通:**
      若改动较大或涉及方向性变更，已在关联 Issue 中与维护者沟通并达成一致。
- [ ] **范围聚焦:** 本 PR 为一项聚焦改动，未包含无关代码。
- [ ] **本地验证:** 已按变更路径实际验证，并写明命令与观察结果。仅声明
      `go build` 通过或测试通过，不视为有效证明。
- [ ] **安全合规:** 代码中无敏感凭据，且符合项目代码规范。
