package server

import (
	"go.lsp.dev/protocol"
)

// providePrepareCallHierarchy (feature 18, T1) is a stub provider that returns
// an empty array for the textDocument/prepareCallHierarchy request.
func providePrepareCallHierarchy(hctx *handlerContext, params protocol.CallHierarchyPrepareParams) ([]protocol.CallHierarchyItem, error) {
	return nil, nil
}

// provideIncomingCalls (feature 18, T1) is a stub provider that returns
// an empty array for the callHierarchy/incomingCalls request.
func provideIncomingCalls(hctx *handlerContext, params protocol.CallHierarchyIncomingCallsParams) ([]protocol.CallHierarchyIncomingCall, error) {
	return nil, nil
}

// provideOutgoingCalls (feature 18, T1) is a stub provider that returns
// an empty array for the callHierarchy/outgoingCalls request.
func provideOutgoingCalls(hctx *handlerContext, params protocol.CallHierarchyOutgoingCallsParams) ([]protocol.CallHierarchyOutgoingCall, error) {
	return nil, nil
}
