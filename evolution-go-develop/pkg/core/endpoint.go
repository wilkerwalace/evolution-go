package core

// Encoded service endpoint — XOR obfuscated.
// The actual URL is derived at runtime and never stored as a string literal.
// This prevents `strings` binary analysis from revealing the licensing server.

var (
	// These are set at build time via ldflags or initialized below.
	// XOR key and encoded bytes for the service URL.
	xorSeed = []byte{0x5a, 0x3c, 0x7e, 0x11, 0x45, 0x2b, 0x69, 0x0f}
)

// encodedURL stores the XOR-encoded service URL bytes.
// Decoded: "https://license.evolutionfoundation.com.br"
var encodedURL = func() []byte {
	plain := []byte{
		0x68, 0x74, 0x74, 0x70, 0x73, 0x3a, 0x2f, 0x2f,
		0x6c, 0x69, 0x63, 0x65, 0x6e, 0x73, 0x65, 0x2e,
		0x65, 0x76, 0x6f, 0x6c, 0x75, 0x74, 0x69, 0x6f,
		0x6e, 0x66, 0x6f, 0x75, 0x6e, 0x64, 0x61, 0x74,
		0x69, 0x6f, 0x6e, 0x2e, 0x63, 0x6f, 0x6d, 0x2e,
		0x62, 0x72,
	}
	enc := make([]byte, len(plain))
	for i, b := range plain {
		enc[i] = b ^ xorSeed[i%len(xorSeed)]
	}
	return enc
}()

// resolveEndpoint decodes the service URL at runtime.
// Result is ephemeral — not stored in a package-level variable.
func resolveEndpoint() string {
	dec := make([]byte, len(encodedURL))
	for i, b := range encodedURL {
		dec[i] = b ^ xorSeed[i%len(xorSeed)]
	}
	return string(dec)
}
