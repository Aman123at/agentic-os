# aosd is a single Go binary

The Agent runtime, command execution, Session management, the HTTP/WebSocket API, and the embedded Desktop assets all live in one Go binary, `aosd`, with module boundaries inside it rather than process boundaries. Go was chosen over Rust because end-to-end latency is dominated by LLM round-trips and the commands themselves, not by the microseconds of executor overhead where Rust would win; Go's goroutines, PTY and filesystem-watch libraries, official OpenAI SDK, fast compiles and trivial static cross-compilation (amd64 + arm64) buy more speed of delivery than Rust's memory profile would buy at runtime.

## Considered Options

- **Rust**: rejected; its advantages (no GC, tighter memory) do not move the latency that users feel.
- **Separate executor process/container**: rejected for v1 in favour of in-container privilege separation (see ADR-0004).
