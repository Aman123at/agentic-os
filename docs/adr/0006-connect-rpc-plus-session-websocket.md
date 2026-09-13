# Connect-RPC for the API, a plain WebSocket for Session I/O

The Desktop, the in-container CLI, and a future Host-side CLI all consume the same API, so it is defined once in protobuf and served with Connect-RPC, generating typed Go handlers and TypeScript clients and carrying the live event stream as a server stream. Terminal Session input and output use a separate binary WebSocket, because browsers cannot do bidirectional streaming over Connect and keystroke echo must stay under 30 ms.

## Considered Options

- **REST + JSON with OpenAPI-generated types**: rejected; weaker end-to-end typing and more hand-written glue for streaming.
- **Everything over one WebSocket**: rejected; loses generated contracts and request/response semantics.
