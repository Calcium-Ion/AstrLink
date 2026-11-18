# Agent issue and PR checks

Use this checklist only when `AGENTS.md` selects an agent template. Core
maintainers skip it when filing an ordinary issue or using a human PR template.
If a required check fails, explain why and stop. A draft does not authorize
publication.

## Scope and duplicates

- Answer usage, configuration, and integration questions from `docs/guides/`,
  `CONTRIBUTING.md`, the README, and relevant code instead of filing them. For a
  feature request, establish what the current version cannot do.
- New subscription or Coding Plan providers must support direct integration.
  Providers that require additional protocol conversion, such as Kiro or Cursor,
  are out of scope. Issues and PRs based on bulk AI scans are also out of scope.
- Search existing issues and PRs, and discussions for issue reports. If the
  request is already covered, give the user the existing thread instead.

## Issue evidence

For a bug report or bug-fix PR, establish what happened, what should have
happened, how to reproduce it, who is affected, and how often it happens.
Identify the exact AstrLink version or commit, install source, runtime, OS, and
architecture. If the report comes from a modified fork, confirm it on an
unmodified, supported version.

Include the evidence for each affected area:

- **Relay / API:** endpoint and method, service type (subscription, Coding Plan,
  or API), provider, model, inbound and upstream protocols, and routing or
  conversion settings. Include the redacted request, response status and body,
  and relevant logs. If the error comes from upstream, compare the equivalent
  direct request with the request through AstrLink to locate the difference.
- **Cost / usage:** endpoint, model, response `usage`, pricing settings, request
  record, and expected calculation. Distinguish the local estimate from a
  provider charge or quota.
- **Desktop:** page, app/WebView or browser version, window dimensions, theme,
  and relevant console, network, or desktop logs.
- **Installation / upgrade:** install or build method, versions before and
  after, whether a data migration ran, and relevant startup, worker, or
  migration logs.

For a feature, give the current limitation, requested behavior, and use case. A
large or directional feature needs maintainer agreement on the issue before a
PR. Ask the user only for required facts missing from the available evidence,
then wait. Do not ask them to confirm the template or invent an answer.

## PR checks

- Keep the change focused. Link the issue for a new feature. If none exists,
  prepare an issue draft first; get authorization before filing it.
- Show what happened before and after. Give the command or manual steps you
  actually ran and the observed result. A bare “build passed” or “tests passed”
  does not show that the changed path works. Include a screenshot or recording
  for a visual change. Identify anything material you did not verify.
- Classify rollback as a **two-way** door if reverting restores the old
  behavior. Use **one-way** if reverting alone cannot undo the effect, such as
  lost data after a migration. Name the affected area under **Blast Radius**.

## Writing the agent body

- Write for a maintainer who did not see the conversation. Use the user's
  language, keep every heading, and explain briefly when a field does not apply.
  Fill in the agent line; use `unknown` for runtime metadata you cannot see. Do
  not guess the model ID.
- In **Request summary**, summarize the user's intent and later constraints in 1
  to 4 sentences. In **Summary**, describe the problem or change, not the
  conversation. If a flow or structure changes, show only the affected part: use
  a pseudocode diff for logic, a tree for calls or modules, or a Mermaid diagram
  for request sequences, routing decisions, or state transitions. For a typo or
  constant change, a sentence is enough.
- In **Evidence**, include the relevant facts above. Use exact files, symbols,
  commands, and observed results. Link long logs. Redact API keys, tokens,
  cookies, private prompts, and personal data, including in screenshots and
  quoted output.
- Keep paragraphs unwrapped in the submitted body. Review and condense the body
  and later comments; leave out speculation and unfiltered AI output.
