package live

import _ "embed"

// bridgeTS is the read-only SSE export the pi extension runs. It is embedded
// (rather than shipped next to the binary) so a single pitago binary is
// self-contained.
//
// It holds no session token and no secret: the bearer token is minted by the
// running pi process into its own descriptor, and the descriptor — not this
// file — is what carries the 0600 discipline. This file is program text, the
// same class as pitago's own binary, and it is installed into the user's
// pi extensions dir as 0644 (see InstallBridge).
//
//go:embed pitago-live-bridge.ts
var bridgeTS string

// BridgeSource is the embedded extension source, for tests and for
// InstallBridge, which writes it where pi will load it for future sessions.
func BridgeSource() string { return bridgeTS }
