# Request trajectory

`events[]` on a request record is the gateway pipeline. Read it in order.

| kind | Meaning |
| --- | --- |
| `accepted` | Local inference accepted the request and assigned an id |
| `privacy` | Request privacy policy ran (`allow`, `warn`, `block`, or `redact`) |
| `routed` | Route / `astrlink/auto` category and target were chosen |
| `upstream` | The selected service was invoked |
| `restore` | Privacy placeholders were restored on the way back |
| `completed` | Terminal status written (`succeeded`, `failed`, `cancelled`, `blocked`) |

Retries appear as **child** records (`parent_request_id` set). `get_request_children` lists them. The root keeps `child_count` and the successful or last-failed outcome.

Useful session fields:

- `session_id` groups turns from one client conversation
- `previous_response_id` / `output_response_id` are protocol cursors
- `audit` on the record is flags only (what was captured), not the ciphertext

If `status` is `blocked`, start with the `privacy` event and `privacy_restore` counts. If the model name looks wrong, start with `routed` and `requested_model`. If the client saw a 5xx after a delay, compare root `events` with child retries.
