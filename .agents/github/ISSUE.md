<!--
Agent-only issue body. Humans use GitHub issue forms.

Keep every heading. If a section does not apply, write why; do not delete it.
Match the user's language in the filled answers. Do not invent facts. Redact
credentials, cookies, private prompts, and personal data from evidence. Apply
the GitHub authorization rules in `AGENTS.md`; a template is not permission to
create or edit an issue or PR.

User request: quote the request the user made to the agent as faithfully as
possible. Keep the original language and line breaks. Do not rewrite,
summarize, translate, or turn it into an issue description.

Keep filled answers short and factual. Do not paste unfiltered AI-generated
text, speculation, or an implementation plan in the issue body or in later
comments. Extract the evidence maintainers need to review. Repeated
submissions of unfiltered AI-generated text may result in a block.
Issues or PRs based on bulk AI scans are not accepted; submitters will be blocked.

If a required fact cannot be established from the conversation, repository, or
permitted checks, ask the user that question and wait. Ask for the facts
themselves. Do not tell the user to confirm a template, tick checkboxes, or
acknowledge the guidelines.

Do not file unless every required condition is met. If the request is out of
scope, is a usage/configuration/integration question, is missing required
facts, or the body would be unfiltered AI-generated text, tell the user and
**stop**. Do not create the issue. Do not file a partial issue. Do not fill
gaps with speculation.
-->

# Agent issue

## Agent

- Tool:
- Tool version:
- Model (full id):
- Host (CLI / IDE / GitHub coding agent / other):
- Date (UTC):

## User request

Quote the request the user made to the agent as faithfully as possible. Keep the
original language and line breaks. Do not rewrite, summarize, translate, or turn
it into an issue description.

- Verbatim:
- Later constraints or corrections from the user (quote, or none):

## Out of scope — refuse

If the request matches any item below, tell the user this repository does not
accept it, point them to the right place when there is one, and **do not file**.

- Usage, configuration, or integration questions (answer from docs and code
  instead)

- Matched: yes/no
- If yes, what was told to the user (stop here; do not file):

## File gate — do not file unless all are satisfied

If any item fails, tell the user and **stop**. Do not create the issue.

- Out of scope: no
- Usage / configuration / integration (answered instead of filing): no
- Required facts present without invention (bugs: actual behavior, impact,
  frequency and applicable type fields; features: current limitation and use
  case): yes
- Body is short and factual; no unfiltered AI-generated text: yes
- File: yes/no
- If no, what was told to the user (stop here):

## Kind

- [ ] Bug
- [ ] Feature
- [ ] Investigation
- [ ] Other:

## Usage / configuration / integration check

Search these yourself before filing. Do not send the user to "read the docs
first". If this is usage, configuration, or integration: answer the user and do
not file.

- <https://github.com/Calcium-Ion/AstrLink/tree/main/docs/guides> — what was
  searched, conclusion:
- <https://github.com/Calcium-Ion/AstrLink/blob/main/CONTRIBUTING.md> — what was
  searched, conclusion:
- README / repo docs:
- Relevant code paths and conclusion:
- Can the current version already do this? (required for feature requests):
- Verdict: product bug or new feature / usage question (stop here):

## Environment

- AstrLink version / commit (not `latest` / `unknown`):
- Install source (repo release / GitHub Actions artifact / source build /
  other):
- OS and architecture:
- Runtime (desktop app / standalone core / browser preview):

## Problem facts

For bug reports, record known facts and ask only for missing required facts. For
feature requests, describe the current limitation and use case; mark bug-only
fields not applicable with a reason. Do not paste unfiltered AI-generated text:

- Actual behavior:
- Impact:
- Frequency:

## Type-specific details

Fill every applicable type. Write "not applicable" for the rest. Ask the user
for missing items; do not invent them.

### Relay / API

- Request endpoint and method:
- Service type (subscription / Coding Plan / API) and provider:
- Model:
- Inbound and upstream protocols:
- Routing and conversion configuration:
- Redacted request, response status and body, and relevant logs:

### Cost / usage

- Request endpoint and model:
- Response `usage`:
- Relevant pricing configuration:
- Request records:
- Expected cost or usage and calculation basis:
- Local estimate vs provider charge or quota:

### Desktop / frontend

- Page path:
- Desktop app / WebView or browser and version:
- Window dimensions:
- Active theme:
- Relevant WebView / browser Console, Network, or desktop logs:

### Installation / upgrade

- Installation or build method:
- OS and architecture:
- Data migration involved (yes/no):
- Versions before and after the upgrade:
- Startup, sidecar / worker, or database migration logs:

## Reproduction and expected result

- Steps to reproduce:
- Expected result:
- Related screenshots (optional):

## Feature (feature requests only)

- Feature description:
- Use case:

## Duplicate check

- Search queries (issues, PRs, discussions):
- Closest existing threads:
- Why this is not a duplicate:

## Research

Open the docs and code. Do not write "already checked" without sources. Do not
replace the fields below with unfiltered AI-generated text.

### Docs

- <https://github.com/Calcium-Ion/AstrLink/tree/main/docs/guides> :
- <https://github.com/Calcium-Ion/AstrLink/blob/main/CONTRIBUTING.md> :
- README / other repo docs:
- Conclusions:

### Code

- Path — what it does, and how it relates:

### Experiments

- Command or redacted request:
- Observed result:
- Conclusion:

## Working theory

- What is broken or missing:
- Why:
- What would falsify this:

## Scope

- In scope for a later PR:
- Out of scope / not this repo:
- Large or directional feature? If yes, this issue is for maintainer alignment;
  do not open a PR yet.

## Proposed direction

(acceptance criteria, not an implementation dump)

## Not verified

(platforms, databases, providers, versions, paths not checked)

## Related

- Issues / PRs / upstream docs:
