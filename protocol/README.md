# Tacet plugin protocol

The JSON Schema files under `schema/v1` are the single source of the message contract between the Go host and plugins (architecture 4.2). The Go host and every SDK follow them; tests check that the kernel's type tables match.

- Transport: JSON-RPC 2.0 over stdin / stdout. Standard output carries protocol frames only; standard error carries redacted, size-limited diagnostics.
- Framing: a 4-byte big-endian length followed by that many bytes of UTF-8 JSON. Receivers check the length against the 4 MiB limit before allocating.
- Task messages carry `job_id`, `attempt_id`, `owner_generation` and the protocol version. Large data travels through controlled resource references, never inside messages.
- Streaming results, byte / item credit, heartbeats, deadlines and cancellation acknowledgements are Tacet extensions (`stream.schema.json`). Control messages must still flow while data is back-pressured.

| File | Contents |
| --- | --- |
| `common.schema.json` | Spans, resource references, task references, errors, exception type codes |
| `manifest.schema.json` | Plugin and rule-pack manifests |
| `initialize.schema.json` | Version negotiation |
| `stream.schema.json` | Stream items, credit and end |
