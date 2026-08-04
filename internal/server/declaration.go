package server

import "go.lsp.dev/protocol"

// provideDeclaration handles the textDocument/declaration request (feature 31, T1, FR-58).
// It is a thin provider that mirrors textDocument/definition — for most Natural constructs,
// "declaration" and "definition" coincide since there is no header/impl split.
//
// Minimal implementation (OQ-1): convert DeclarationParams to DefinitionParams
// (both embed TextDocumentPositionParams identically) and delegate to provideDefinition.
// This keeps declaration resolution logic on the definition path, avoiding duplication.
func provideDeclaration(hctx *handlerContext, params protocol.DeclarationParams) ([]protocol.Location, error) {
	// Convert DeclarationParams to DefinitionParams by constructing one with
	// the same TextDocumentPositionParams (URI + Position).
	definitionParams := protocol.DefinitionParams{
		TextDocumentPositionParams: params.TextDocumentPositionParams,
	}
	// Delegate to the shared definition logic
	return provideDefinition(hctx, definitionParams)
}
