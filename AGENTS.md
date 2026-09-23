# Agent guidelines

These guidelines apply to the entire repository. Paths below are relative to the
repository root.

## Task completion checks

Before finishing each task, format and fix lint issues in its changed files. Run
from the repository root; replace `<files>` with explicit quoted paths of that
row's type. Skip untouched types and deleted files; preserve unrelated files and
user changes, applying fixes manually if crate-wide tools would change them.

<!-- markdownlint-configure-file { "MD013": { "tables": false } } -->

| Files                                 | Format / lint fix commands                                                                                                     |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Go                                    | `gofmt -w <files>`; `(cd <module> && go vet ./...)` (fix diagnostics manually)                                                 |
| Rust (run inside each affected crate) | `cargo fmt --all`; `cargo clippy --locked --all-targets --fix --allow-dirty --allow-staged -- -D warnings`; `cargo fmt --all`  |
| JS / TS / JSX / TSX / MJS / CJS       | `bunx oxlint@1.19.0 --fix --deny-warnings <files>`; `bunx prettier@3.6.2 --write <files>`                                      |
| JSON / JSONC / CSS / HTML / YAML      | `bunx prettier@3.6.2 --write <files>`                                                                                          |
| Markdown                              | `bunx prettier@3.6.2 --write --prose-wrap always <files>`; `bunx --package markdownlint-cli@0.45.0 markdownlint --fix <files>` |

Go modules: `core`, `convo`, `contracts`. Rust crates: `apps/desktop/src-tauri`,
`apps/privacy-worker`, `apps/classifier-worker`. If Tauri needs staged sidecars,
run `make desktop-sidecar`. For desktop TS, also run
`(cd apps/desktop && bun run typecheck)`; it does not replace linting.

Recheck after fixing: `gofmt -l <files>` must print nothing; rerun `go vet`; use
`cargo fmt --all -- --check`; rerun Clippy without
`--fix --allow-dirty --allow-staged`, Oxlint/markdownlint without `--fix`, and
Prettier with `--check` instead of `--write`. Finish with `git diff --check` and
the task's required tests. Report unavailable tools or remaining failures.

## Upstream forwarding identity

AstrLink is an API gateway. Requests sent to upstream providers must not
identify AstrLink as the forwarding client.

- Do not inject AstrLink branding into upstream headers (including `originator`,
  `User-Agent`, `Via`, `X-Powered-By`, and `X-AstrLink-*`), URL parameters,
  generated request IDs, metadata, system prompts, or request bodies.
- Keep gateway-owned headers local; strip the reserved `X-AstrLink-*` namespace
  before HTTP forwarding and WebSocket handshakes, including target overlays.
- When a provider requires a client identity, reuse its shared identity policy
  and keep related fields consistent. Codex, Claude, and Grok subscription
  identity enforcement defaults to on, with independent persisted controls in
  Routing. Neither mode may introduce an AstrLink originator or User-Agent.
- Apply this rule to inference, retries, protocol conversion, model discovery,
  connection tests, OAuth/device authorization, token refresh, and quota/profile
  requests. Verify the final outgoing request, not only intermediate headers.
- Preserve caller-authored prompts, files, and tool schemas. Do not remove or
  rewrite user content merely because it mentions AstrLink. Local UI, logs,
  storage, control APIs, and explicitly installed debug tools may retain their
  product names; they are not gateway-injected upstream identity.

## Documentation changes

- Do not modify README files, including those in subdirectories, unless the user
  explicitly requests README changes.
- Do not add documentation files unless the user explicitly requests them or
  they are necessary to complete the requested task. Avoid unsolicited notes,
  summaries, reports, and implementation plans in the repository.

## GitHub issues and pull requests

Apply these rules when preparing or submitting an issue or PR, including when
using `gh`. Drafting a body does not authorize publishing it: create or edit
GitHub issues, PRs, or comments only when the user explicitly requests that
action.

**Issues:** Read `.agents/github/ISSUE.md` before drafting an issue. Check its
scope rules, then search `docs/guides/`, `CONTRIBUTING.md`, the README, relevant
code, and existing issues. Answer usage, configuration, or integration questions
from those sources instead of filing them. For an in-scope bug or feature, fill
the agent template as the entire body; do not use the human GitHub issue forms.
Quote the user's request faithfully, preserving its language and line breaks.
Keep answers short and factual. Record actual behavior, impact, frequency, and
applicable type-specific details. For features, describe the current limitation
and use case. Ask only for required facts that cannot be established from
available evidence, and wait before filing; do not invent answers or ask the
user to confirm a template. If a required condition is unmet, explain it and do
not file.

**Pull requests:** Before drafting a PR:

- Compare `git config user.name` and `git config user.email` with historical
  core developers in `git log`; do not change git configuration. If the current
  user is not a historical core developer, disclose AI-generated or AI-assisted
  code in the body. Never add an agent as a co-author.
- For a PR on behalf of the project owner, use
  `.github/PULL_REQUEST_TEMPLATE.md` for Chinese requests or
  `.github/PULL_REQUEST_TEMPLATE/en.md` for English requests, unless the owner
  explicitly requests the agent template.
- For other agent-created PRs, fill `.agents/github/PR.md` as the entire body.
  Quote the user's request faithfully. Keep the body and later comments short
  and factual; do not paste unfiltered AI-generated text.
- Follow the selected template's issue-linking, scope, and verification rules.
  Record commands or steps actually run and observed results; merely saying that
  a build or tests passed is insufficient. If a required condition is unmet,
  explain it and do not open the PR.

## Desktop UI

### Reuse shared components

All new or updated desktop UI must use the existing shared component library
first: `apps/desktop/src/components/ui` for primitives, and
`apps/desktop/src/components` for composed patterns such as `Panel`, `Field`,
`ChoiceCard`, `EmptyState`, and `ConfirmDialog`.

- Check for an existing component before writing page-specific controls or
  layout patterns. Reuse it instead of duplicating its markup and styles.
- If a reusable capability is missing, extend the shared component or add it to
  the component library first, then consume it from the page.
- Keep colors, typography, spacing, and radii on the existing design tokens. Do
  not introduce another UI library or a separate page-level design system
  without an explicit project decision.
- Keep page-specific business logic and composition in the page; keep reusable
  visuals, control behavior, and accessibility in the shared components.

### Keep scrolling inside the active panel

Do not put a tall tab body (model lists, protocol rows, logs) in a page-level
`overflow-y-auto` scroller. Unmounting it collapses the document and the browser
clamps `scrollTop` to 0 — the whole page snaps to the header.

- Keep the workspace `overflow-hidden` and scroll inside the tab panel with
  `min-h-0 flex-1 overflow-y-auto`, as in request records and safety policy.
- Let the panel fill the remaining workspace. Move tall connection forms,
  filters, or charts into their own tab or a collapsible section.
- Preserve the shared `TabsContent` behavior that restores ancestor scroll when
  Radix Tabs focuses a new panel, unless providing a replacement.
- Avoid scrolling the page when changing tabs. Use
  `focus({ preventScroll: true })` when moving focus; if a scroll operation is
  necessary, restore the affected ancestor's `scrollTop`.

Regression to avoid: switching from a long model list to「入口协议」in the API
service editor previously jumped the form back to the top.

### Protect vertical working space

UI design must consider **usable height**, not only width. The primary list,
editor, or preview must receive the majority of the workspace; a layout is not
finished if stacked navigation and explanatory chrome leave only half the window
for the actual task.

- Budget the full vertical stack: native title bar, workspace padding, page
  header, tabs, section headings, descriptions, toolbars, and their gaps.
- Consolidate navigation into one row where possible. Do not repeat the same
  title in a tab, panel header, and list toolbar. Put search, counts, and
  primary actions in one compact toolbar; reveal supporting explanations on
  demand.
- Use the shared compact page header and existing component size variants.
  Preserve readable text and usable controls; do not recover height by shrinking
  everything or hiding essential actions.
- Give the primary region the remaining height with `min-h-0 flex-1` and follow
  the panel scrolling rules above.

Verify layouts in the actual application shell, including native title-bar
spacing:

1. Check both width and height at 1280×720 and 1024×600, plus a narrow layout.
2. For list, editor, and preview workspaces, aim for at least **60% of the
   usable workspace height** in the primary region. If it falls below that,
   consolidate or collapse secondary UI before accepting the design.
3. Check long content, expanded help, empty states, and tab switches.
4. Record the actual primary-region height during verification; a screenshot of
   a spacious window alone is insufficient.

Regression to avoid: the privacy allowlist previously lost about half its
working height to two tab rows, a repeated panel title, explanatory text, and a
separate toolbar.

### Use in-app dialogs

On macOS, AstrLink's Tauri WebView (WKWebView via wry) does not show JavaScript
dialogs. `window.confirm`, `window.alert`, and `window.prompt` are silent
no-ops; **`confirm()` always returns `false`**. Save or Delete may appear to do
nothing, or follow a false cancellation path.

- Use the shared `ConfirmDialog` for confirmations. Existing in-app patterns
  include `token-dialog` / `token-dialog-backdrop` in request records, access
  tokens, and safety policy.
- Do not add `@tauri-apps/plugin-dialog` unless the project explicitly adopts
  that dependency.
- Browser and unit tests may mock `window.confirm`; production desktop UI must
  not rely on it for real user confirmation.

Regression to avoid: enabling request-body capture requires confirmation before
sending `audit_risk_acknowledged: true`. Using `window.confirm` previously
showed “已取消开启正文捕获” immediately without displaying a dialog.

## Desktop development reload

Do not put a `/__astrlink_build` poller, `location.reload()` loop, or Cmd+R
handler inside the frontend bundle. A top-level module error kills that script
and hot-reload goes silent until `make dev` is restarted.

The debug Rust host in `apps/desktop/src-tauri/src/dev_reload.rs` polls the
generation endpoint and calls `webview.reload()`. Failures must log to stderr.
The Rsbuild plugin only publishes the generation; it must not drive the reload.
