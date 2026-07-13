package server

import (
	"go.lsp.dev/protocol"
)

// provideSignatureHelp is the LSP signature help provider stub (feature 17, T1 RED phase).
// It is called when the client requests textDocument/signatureHelp.
// Currently returns nil, nil (no signature at any position), which marshals to JSON "null".
//
// CRITICAL: When wiring the dispatch in server.go, the result MUST be marshaled via
// (*protocol.SignatureHelp).MarshalJSONTo(jsontext.NewEncoder(&buf)) — NOT json.Marshal —
// because SignatureHelp contains union/Nullable fields that require the protocol encoder.
// See the divergence note in tasks.md and server.go handleInitialize for the pattern.
func provideSignatureHelp(hctx *handlerContext, params protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	// Stub: return nil, nil (no signature help at any position during RED phase).
	return nil, nil
}
