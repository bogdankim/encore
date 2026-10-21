`make bridge` copies the release build of `encore-bridge` here, and the Go binary
embeds it (`internal/bridge/embed.go`). The binary itself is not committed; this
file keeps the directory, so `go build` works without it.
