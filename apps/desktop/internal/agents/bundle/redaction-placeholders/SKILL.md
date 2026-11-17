---
name: redaction-placeholders
description: >-
  Handle redaction placeholders in conversation, files, or tool output: markers
  such as <PRIVATE_EMAIL_…> or <SECRET_…>, and reserved stand-ins such as
  redacted-…@private.invalid, https://private.invalid/r/…, 192.0.2.x,
  198.51.100.x, 203.0.113.x, or 2001:db8::…. Copy them byte for byte and never
  guess the hidden value. Load before putting one in a tool call, file, command,
  or reply.
---

# Redaction placeholders

A local privacy filter replaced some values in this conversation before the
request left the user's machine. The mapping from each placeholder to its
original value stays on that machine. You never see it, and nothing you write
can reveal it.

Two shapes appear. [references/shapes.md](references/shapes.md) lists every
kind.

- **Markers** such as `<PRIVATE_EMAIL_…>`, `<PRIVATE_PHONE_…>`, or `<SECRET_…>`,
  where `…` is a hex suffix. Secrets, phone numbers, card and account numbers,
  names, street addresses, and dates always use this shape.
- **Natural stand-ins**: well-formed values from namespaces that can never be
  real, such as `redacted-…@private.invalid`, `https://private.invalid/r/…`, and
  addresses in `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, or
  `2001:db8::/32`.

## Rules

1. Copy every placeholder byte for byte. Do not change its case, shorten it,
   split it across lines, or encode or escape it differently.
2. Never invent a string of the same shape, and never reuse a placeholder for a
   different value.
3. Never guess, reconstruct, or ask for the original value.

## Tool calls

By default the filter puts the original value back into tool-call arguments
before the tool runs. Write the placeholder exactly where the value belongs.

- Quote as if the real value could contain any character: spaces, quotes, `$`,
  backslashes, or newlines. When the task allows, refer to the value instead of
  inlining it: `$VAR`, `source .env`, or a program that reads the file itself.
- Do every transformation inside the tool, never by hand: base64, URL encoding,
  length, hashing, splitting, or case changes. Computing them from the
  placeholder text describes the placeholder, not the value.
- Restoration matches the exact placeholder text only. Do not build it from
  pieces or reformat it inside a larger string.

## Reading results back

The filter also runs on tool output, so a value you wrote comes back as a
placeholder when you read it. Within one request the same value always gets the
same placeholder: equal placeholders mean equal values, and different
placeholders mean different values. Use that to compare keys or confirm a write.
This is not data corruption. Do not "fix" a file because it shows a placeholder
where you wrote the value.

## Natural stand-ins

A stand-in such as `203.0.113.7` or `redacted-…@private.invalid` hides a real
value. It is not a configuration mistake or a documentation example. Do not
change it, do not replace it with another example value, and do not tell the
user it is a documentation address or needs updating.

## Replying to the user

With response restore on, which is the default, the user reads your reply with
the original values in place. Refer to a value normally and do not warn that it
is hidden.

That restore also turns a `<SECRET_…>` marker in a reply back into the secret.
Keep secret markers out of replies unless the user asks for the literal value.
Name the secret instead, such as "the key you sent", and put `$VAR` in its place
in a command you show rather than run.

## When the task needs the real value

If a task cannot be done through a tool call, for example when the user asks you
to judge the content of a secret, say that the value is hidden by local privacy
settings. You may suggest an allowlist entry in those settings for values that
are safe to share. Do not ask the user to paste the value, and do not suggest
turning privacy protection off.

## Signs that restoration failed

Restoration can be turned off for tool calls, and it misses a placeholder whose
bytes were altered. Reading a file back proves nothing, because the filter turns
the restored value into the same placeholder again. Watch for a placeholder
reaching the tool literally instead:

- a shell error naming part of a marker, such as
  `SECRET_…: No such file or directory`, because the shell read `<` and `>` as
  redirections;
- a DNS failure for `private.invalid`, or a timeout connecting to a `192.0.2.x`,
  `198.51.100.x`, or `203.0.113.x` address;
- a tool reporting a value with the exact length or text of the placeholder.

When you see one, stop immediately, write nothing further, and tell the user
which placeholder reached the tool unrestored and which files or commands were
affected, so they can check their privacy settings.
