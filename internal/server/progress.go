// Package server: window/workDoneProgress helpers for reporting indexing
// progress on first run (PRD FR-32).
package server

import (
	"bytes"
	"context"
	"fmt"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"log/slog"
)

// progressReporter sends LSP work-done progress notifications over a jsonrpc2 stream.
// It is a no-op when disabled (client didn't advertise support for window.workDoneProgress).
type progressReporter struct {
	stream  jsonrpc2.Stream
	token   protocol.ProgressToken
	logger  *slog.Logger
	enabled bool
}

// newProgressReporter creates a progressReporter. Pass enabled=false to create a no-op
// reporter that writes nothing.
func newProgressReporter(stream jsonrpc2.Stream, token protocol.ProgressToken, logger *slog.Logger, enabled bool) *progressReporter {
	return &progressReporter{
		stream:  stream,
		token:   token,
		logger:  logger,
		enabled: enabled,
	}
}

// create sends the window/workDoneProgress/create request.
func (pr *progressReporter) create(ctx context.Context) error {
	if !pr.enabled {
		return nil
	}

	// Build WorkDoneProgressCreateParams
	params := protocol.WorkDoneProgressCreateParams{
		Token: pr.token,
	}

	// Marshal params via MarshalJSONTo
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := params.MarshalJSONTo(enc); err != nil {
		pr.logger.Warn("failed to marshal WorkDoneProgressCreateParams", "err", err)
		return err
	}

	// Create and send the request call
	id := jsonrpc2.NewStringID("natural-lsp-progress-create")
	call := jsonrpc2.NewCall(id, "window/workDoneProgress/create", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := pr.stream.Write(ctx, call); err != nil {
		pr.logger.Warn("failed to send window/workDoneProgress/create", "err", err)
		return err
	}

	return nil
}

// begin sends a $/progress notification with WorkDoneProgressBegin.
func (pr *progressReporter) begin(ctx context.Context, title string) error {
	if !pr.enabled {
		return nil
	}

	// Build the WorkDoneProgressBegin value
	beginValue := protocol.WorkDoneProgressBegin{
		Kind:  "begin",
		Title: title,
	}

	// Convert to LSPAny via mustLSPAny
	value := mustLSPAny(beginValue)

	// Build ProgressParams
	params := protocol.ProgressParams{
		Token: pr.token,
		Value: value,
	}

	// Marshal params via MarshalJSONTo
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := params.MarshalJSONTo(enc); err != nil {
		pr.logger.Warn("failed to marshal ProgressParams for begin", "err", err)
		return err
	}

	// Send the notification
	notif := jsonrpc2.NewNotification("$/progress", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := pr.stream.Write(ctx, notif); err != nil {
		pr.logger.Warn("failed to send $/progress begin", "err", err)
		return err
	}

	return nil
}

// report sends a $/progress notification with WorkDoneProgressReport.
func (pr *progressReporter) report(ctx context.Context, current int, total int, path string) error {
	if !pr.enabled {
		return nil
	}

	// Build the message
	message := fmt.Sprintf("%d/%d files", current, total)

	// Build the WorkDoneProgressReport value
	reportValue := protocol.WorkDoneProgressReport{
		Kind:    "report",
		Message: &message,
	}

	// Calculate percentage only if total > 0 (avoid divide by zero)
	if total > 0 {
		// Round to nearest integer: (current * 100 + total/2) / total
		percentage := uint32((current*100 + total/2) / total)
		// Clamp to [0, 100]
		if percentage > 100 {
			percentage = 100
		}
		reportValue.Percentage = &percentage
	}

	// Convert to LSPAny via mustLSPAny
	value := mustLSPAny(reportValue)

	// Build ProgressParams
	params := protocol.ProgressParams{
		Token: pr.token,
		Value: value,
	}

	// Marshal params via MarshalJSONTo
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := params.MarshalJSONTo(enc); err != nil {
		pr.logger.Warn("failed to marshal ProgressParams for report", "err", err)
		return err
	}

	// Send the notification
	notif := jsonrpc2.NewNotification("$/progress", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := pr.stream.Write(ctx, notif); err != nil {
		pr.logger.Warn("failed to send $/progress report", "err", err)
		return err
	}

	return nil
}

// end sends a $/progress notification with WorkDoneProgressEnd.
func (pr *progressReporter) end(ctx context.Context, message string) error {
	if !pr.enabled {
		return nil
	}

	// Build the WorkDoneProgressEnd value
	endValue := protocol.WorkDoneProgressEnd{
		Kind: "end",
	}
	if message != "" {
		endValue.Message = &message
	}

	// Convert to LSPAny via mustLSPAny
	value := mustLSPAny(endValue)

	// Build ProgressParams
	params := protocol.ProgressParams{
		Token: pr.token,
		Value: value,
	}

	// Marshal params via MarshalJSONTo
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := params.MarshalJSONTo(enc); err != nil {
		pr.logger.Warn("failed to marshal ProgressParams for end", "err", err)
		return err
	}

	// Send the notification
	notif := jsonrpc2.NewNotification("$/progress", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := pr.stream.Write(ctx, notif); err != nil {
		pr.logger.Warn("failed to send $/progress end", "err", err)
		return err
	}

	return nil
}
