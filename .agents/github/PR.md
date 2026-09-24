<!--
Agent-only PR body. Humans and agents acting for the project owner use the
human template matching the user's language, unless the owner requests this
agent template. See `AGENTS.md` for routing.

Keep every heading. If a section does not apply, write why; do not delete it.
Match the user's language in the filled answers. Do not invent facts. Redact
credentials, cookies, private prompts, and personal data from evidence. Apply
the GitHub authorization rules in `AGENTS.md`; a template is not permission to
create or edit an issue or PR.

User request: quote the request the user made to the agent as faithfully as
possible. Keep the original language and line breaks. Do not rewrite,
summarize, translate, or turn it into a PR description.

Keep the body short and factual. Do not paste unfiltered AI-generated text in
the PR body or in later comments. Repeated submissions of this kind may result
in a block. Verification must be commands actually run and what was observed.
Stating only that `go build` or tests passed is not proof.
Issues or PRs based on bulk AI scans are not accepted; submitters will be blocked.

New features: link an issue; if none exists, prepare one with
`.agents/github/ISSUE.md` and resolve the missing issue within the user's
authorization before opening the PR. Large or directional changes: maintainer
agreement on that issue before this PR.

If this PR fixes a bug and the linked issue is missing actual behavior,
impact, frequency, or the applicable relay / cost and usage / desktop /
installation items, ask the user those questions and wait. Ask for the facts.
Do not tell the user to confirm a template.

Do not open a PR unless every required condition is met. If the change is out
of scope, is a usage/configuration/ integration question, is missing required
issue facts, has only `go build` / tests-passed as verification, or the body
would be unfiltered AI-generated text, tell the user and **stop**. Do not open
the PR. Do not open a partial PR.

Then search https://github.com/Calcium-Ion/AstrLink/tree/main/docs/guides ,
https://github.com/Calcium-Ion/AstrLink/blob/main/CONTRIBUTING.md , the
README, and the code. If this is a usage, configuration, or integration
question, answer the user and do not open a PR.

GitHub issue/PR bodies preserve newlines. Keep paragraphs and list items
unwrapped so the page controls text wrapping.
-->

<!-- markdownlint-configure-file { "MD013": false } -->

<!-- prettier-ignore-start -->

# Agent pull request

## Agent

- Tool:
- Tool version:
- Model (full id):
- Host (CLI / IDE / GitHub coding agent / other):
- Date (UTC):

## Links

- Closes #
- Related:

## User request

Quote the request the user made to the agent as faithfully as possible. Keep the original language and line breaks. Do not rewrite, summarize, translate, or turn it into a PR description.

- Verbatim:
- Later constraints or corrections from the user (quote, or none):

## Out of scope — refuse

If the change matches any item below, tell the user this repository does not accept it and **do not open a PR**.

- New subscription or Coding Plan providers must support direct integration; providers requiring additional protocol conversion, such as Kiro, Antigravity, or Cursor, are not accepted at this time.
- Usage, configuration, or integration (answer from docs and code instead)

- Matched: yes/no
- If yes, what was told to the user (stop here; do not open a PR):

## Open gate — do not open unless all are satisfied

If any item fails, tell the user and **stop**. Do not open the PR.

- Out of scope: no
- Usage / configuration / integration (answered instead of opening): no
- Required issue facts present without invention (or not applicable, with a reason): yes
- Verification is actual commands or steps and observed results, not only `go build` or tests passed: yes
- Body is short and factual; no unfiltered AI-generated text: yes
- Open: yes/no
- If no, what was told to the user (stop here):

## Kind

- [ ] Bug fix
- [ ] New feature
- [ ] Performance / refactor
- [ ] Docs
- [ ] Other:

## Issue facts

Take these from the linked issue. If a needed item is empty, ask the user that question.

- Actual behavior:
- Impact:
- Frequency:
- Applicable types and their fields (relay / cost and usage / desktop / installation; write "not applicable" otherwise):

## Change

(what changed, why it works, grounded in the code actually touched. Short and factual; do not paste unfiltered AI-generated text.)

## Research

### Duplicate / prior art

- Search queries (issues, PRs):
- What already existed and why this is not a duplicate:

### Docs and code

Open them. Do not write "already checked" without sources.

- <https://github.com/Calcium-Ion/AstrLink/tree/main/docs/guides> :
- <https://github.com/Calcium-Ion/AstrLink/blob/main/CONTRIBUTING.md> :
- README / repo docs:
- Code paths and what they imply for this change:

### Alternatives considered

- Option A:
- Option B:
- Why this approach:

## Files

| Path | Why |
| ---- | --- |
|      |     |

## Behavior

- Before:
- After:
- Explicit non-goals / leftover work:

## Verification

Only what was actually run. Do not state only that `go build` or tests passed. Each item needs the command or steps and the observed result.

- Commands and results:
- Manual steps and observed result:
- UI: screenshot or recording (or why none):
- Tests added or updated, or why none:
- Databases / providers / platforms exercised:
- Not verified:

## Risks

- Failure modes:
- Billing / quota / auth impact:
- Follow-ups:

## Scope check

- Single focused change: yes/no (if no, why):
- Secrets included: no
- Out of scope: no

<!-- prettier-ignore-end -->
