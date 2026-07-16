package server

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"natural-lsp/internal/config"
)

// TestMalformedDefinitionParams tests that textDocument/definition with malformed params
// returns InvalidParams (not a crash) and the server loop survives to handle subsequent requests.
// This verifies the handler inherits the per-request panic recovery and has proper param validation.
func TestMalformedDefinitionParams(t *testing.T) {
	// Arrange: build message sequence: initialize → initialized → malformed definition → valid shutdown

	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Malformed definition params: garbage JSON (invalid UTF-8 or syntax)
	// Also test: completely missing params, wrong type (number instead of object)
	malformedID := jsonrpc2.NewNumberID(2)
	malformedCall := jsonrpc2.NewCall(malformedID, "textDocument/definition", jsonrpc2.RawMessage(`{garbage}`))

	// Follow-up valid request to verify loop is alive
	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Write all messages as framed
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, malformedCall, shutdownCall} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without error (not crashed by malformed params)
	if err != nil {
		t.Fatalf("Run failed: %v; expected to handle malformed params gracefully", err)
	}

	// Parse responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response (we only care about the malformed handling)
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read malformed definition response
	malformedBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse malformed definition response: %v", err)
	}
	malformedMsg, err := jsonrpc2.DecodeMessage(malformedBody)
	if err != nil {
		t.Fatalf("failed to decode malformed response: %v", err)
	}

	malformedResp, ok := malformedMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for malformed request, got %T", malformedMsg)
	}

	// Assert: the response must have the correct id and an error (not a result)
	if malformedResp.ID() != malformedID {
		t.Errorf("malformed response id = %v, want %v", malformedResp.ID(), malformedID)
	}

	// The response should have an error (InvalidParams) since params are malformed
	if malformedResp.Err() == nil {
		t.Errorf("malformed definition params should produce an error; got result: %v", malformedResp.Result())
	} else {
		errTyped, ok := malformedResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("malformed response error is %T, not *jsonrpc2.Error: %v", malformedResp.Err(), malformedResp.Err())
		} else if errTyped.Code != jsonrpc2.InvalidParams {
			t.Errorf("malformed response error code = %v, want %v (InvalidParams)", errTyped.Code, jsonrpc2.InvalidParams)
		}
	}

	// Read shutdown response and assert it succeeded (proving loop is alive)
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}

	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}

	if shutdownResp.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownResp.ID(), shutdownID)
	}

	if shutdownResp.Err() != nil {
		t.Errorf("shutdown should succeed after malformed definition params; got error: %v", shutdownResp.Err())
	}
}

// TestMalformedReferenceParams tests that textDocument/references with malformed params
// returns InvalidParams (not a crash) and the server loop survives to handle subsequent requests.
func TestMalformedReferenceParams(t *testing.T) {
	// Arrange: build message sequence: initialize → initialized → malformed references → valid shutdown

	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Malformed references params: wrong type (array instead of object)
	malformedID := jsonrpc2.NewNumberID(2)
	malformedCall := jsonrpc2.NewCall(malformedID, "textDocument/references", jsonrpc2.RawMessage(`[1,2,3]`))

	// Follow-up valid request
	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Write all messages as framed
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, malformedCall, shutdownCall} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without error
	if err != nil {
		t.Fatalf("Run failed: %v; expected to handle malformed params gracefully", err)
	}

	// Parse responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read malformed references response
	malformedBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse malformed references response: %v", err)
	}
	malformedMsg, err := jsonrpc2.DecodeMessage(malformedBody)
	if err != nil {
		t.Fatalf("failed to decode malformed response: %v", err)
	}

	malformedResp, ok := malformedMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for malformed request, got %T", malformedMsg)
	}

	// Assert: response has correct id and an error
	if malformedResp.ID() != malformedID {
		t.Errorf("malformed response id = %v, want %v", malformedResp.ID(), malformedID)
	}

	if malformedResp.Err() == nil {
		t.Errorf("malformed references params should produce an error; got result: %v", malformedResp.Result())
	} else {
		errTyped, ok := malformedResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("malformed response error is %T, not *jsonrpc2.Error: %v", malformedResp.Err(), malformedResp.Err())
		} else if errTyped.Code != jsonrpc2.InvalidParams {
			t.Errorf("malformed response error code = %v, want %v (InvalidParams)", errTyped.Code, jsonrpc2.InvalidParams)
		}
	}

	// Read shutdown response and assert it succeeded
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}

	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}

	if shutdownResp.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownResp.ID(), shutdownID)
	}

	if shutdownResp.Err() != nil {
		t.Errorf("shutdown should succeed after malformed references params; got error: %v", shutdownResp.Err())
	}
}

// TestMalformedWorkspaceSymbolParams tests that workspace/symbol with malformed params
// returns InvalidParams (not a crash) and the server loop survives to handle subsequent requests.
func TestMalformedWorkspaceSymbolParams(t *testing.T) {
	// Arrange: build message sequence: initialize → initialized → malformed workspace/symbol → valid shutdown

	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Malformed workspace/symbol params: a number instead of an object (wrong type)
	malformedID := jsonrpc2.NewNumberID(2)
	malformedCall := jsonrpc2.NewCall(malformedID, "workspace/symbol", jsonrpc2.RawMessage(`42`))

	// Follow-up valid request
	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Write all messages as framed
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, malformedCall, shutdownCall} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without error
	if err != nil {
		t.Fatalf("Run failed: %v; expected to handle malformed params gracefully", err)
	}

	// Parse responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read malformed workspace/symbol response
	malformedBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse malformed workspace/symbol response: %v", err)
	}
	malformedMsg, err := jsonrpc2.DecodeMessage(malformedBody)
	if err != nil {
		t.Fatalf("failed to decode malformed response: %v", err)
	}

	malformedResp, ok := malformedMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for malformed request, got %T", malformedMsg)
	}

	// Assert: response has correct id and an error
	if malformedResp.ID() != malformedID {
		t.Errorf("malformed response id = %v, want %v", malformedResp.ID(), malformedID)
	}

	if malformedResp.Err() == nil {
		t.Errorf("malformed workspace/symbol params should produce an error; got result: %v", malformedResp.Result())
	} else {
		errTyped, ok := malformedResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("malformed response error is %T, not *jsonrpc2.Error: %v", malformedResp.Err(), malformedResp.Err())
		} else if errTyped.Code != jsonrpc2.InvalidParams {
			t.Errorf("malformed response error code = %v, want %v (InvalidParams)", errTyped.Code, jsonrpc2.InvalidParams)
		}
	}

	// Read shutdown response and assert it succeeded
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}

	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}

	if shutdownResp.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownResp.ID(), shutdownID)
	}

	if shutdownResp.Err() != nil {
		t.Errorf("shutdown should succeed after malformed workspace/symbol params; got error: %v", shutdownResp.Err())
	}
}

// defaultTestConfig returns a default configuration for testing
func defaultTestConfig() config.Config {
	cfg := config.Defaults()
	return cfg
}
