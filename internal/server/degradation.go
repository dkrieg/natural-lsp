package server

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"natural-lsp/internal/analysis"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// FileProcessResult holds the outcome of processing a single file.
type FileProcessResult struct {
	RelPath      string
	ObjectType   model.ObjectType
	SkipReason   config.SkipReason
	FileAnalysis model.FileAnalysis
}

// ProcessFiles processes a batch of files with graceful degradation (FR-43).
// paths must be provided in the desired output order; contents maps each path
// to its raw bytes. For each file:
//  1. Skip if content length > cfg.Workspace.MaxFileSize — record SkipTooLarge
//  2. Skip if cfg.IsExcluded(relPath) returns true — record SkipExcluded
//  3. Classify unrecognized extensions as ObjectUnknown — still present in output, not dropped
//  4. Recover from any panic in az.Analyze without aborting the batch — log the recovery
//  5. Every skip/recovery emits an observable structured log line with "path" and "reason"
//
// Results are returned in the same order as paths.
func ProcessFiles(cfg config.Config, az analysis.Analyzer, paths []string, contents map[string][]byte, logger *slog.Logger) []FileProcessResult {
	results := make([]FileProcessResult, len(paths))

	for i, relPath := range paths {
		content := contents[relPath]
		results[i] = analyzeOne(cfg, az, relPath, content, logger)
	}

	return results
}

// analyzeFile calls az.Analyze for a single file and writes the resulting
// ObjectType and FileAnalysis into result. If az.Analyze panics, the panic
// is recovered and logged with "path" and "reason" (the panic value as a
// string); result's ObjectType and FileAnalysis are left at their pre-call
// values (ObjectUnknown and zero FileAnalysis respectively) so the batch
// continues (FR-43).
func analyzeFile(az analysis.Analyzer, relPath string, content []byte, result *FileProcessResult, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("recovered from analyzer panic",
				slog.String("path", relPath),
				slog.String("reason", fmt.Sprintf("%v", r)),
			)
		}
	}()

	fa, err := az.Analyze(relPath, content)
	if err != nil {
		logger.Warn("analyzer error",
			slog.String("path", relPath),
			slog.String("reason", err.Error()),
		)
		return
	}
	result.ObjectType = fa.ObjectType
	result.FileAnalysis = fa
}

// analyzeOne processes a single file with FR-43 graceful degradation.
// It performs size checks, exclusion checks, extension recognition, and
// panic recovery all in one place, so that degradation rules are consistently
// applied whether the call comes from ProcessFiles, the document store (Task 4),
// or the file watcher (Task 9).
//
// For each file:
//  1. Skip if content length > cfg.Workspace.MaxFileSize — record SkipTooLarge.
//     cfg.Workspace.MaxFileSize == 0 means unlimited; the size check is skipped.
//  2. Skip if cfg.IsExcluded(relPath) returns true — record SkipExcluded
//  3. Classify unrecognized extensions as ObjectUnknown — still present in output, not dropped
//  4. Recover from any panic in az.Analyze without aborting — log the recovery
//  5. Every skip/recovery emits an observable structured log line with "path" and "reason"
//
// Feature 04 Task 3; FR-43.
func analyzeOne(cfg config.Config, az analysis.Analyzer, relPath string, content []byte, logger *slog.Logger) FileProcessResult {
	result := FileProcessResult{
		RelPath:    relPath,
		ObjectType: model.ObjectUnknown,
	}

	// Check: file exceeds max file size.
	if cfg.Workspace.MaxFileSize > 0 && int64(len(content)) > cfg.Workspace.MaxFileSize {
		result.SkipReason = config.SkipTooLarge
		logger.Info("skipping file",
			slog.String("path", relPath),
			slog.String("reason", string(config.SkipTooLarge)),
		)
		return result
	}

	// Check: file is inside an excluded directory.
	if cfg.IsExcluded(relPath) {
		result.SkipReason = config.SkipExcluded
		logger.Info("skipping file",
			slog.String("path", relPath),
			slog.String("reason", string(config.SkipExcluded)),
		)
		return result
	}

	// Build a set of recognized extensions for quick lookup (case-insensitive).
	recognizedExts := make(map[string]bool, len(cfg.Workspace.Extensions))
	for _, ext := range cfg.Workspace.Extensions {
		recognizedExts[strings.ToUpper(ext)] = true
	}

	// Check: unrecognized extension — keep as ObjectUnknown, skip analysis.
	ext := strings.ToUpper(filepath.Ext(relPath))
	if !recognizedExts[ext] {
		return result
	}

	// Call the analyzer inside a closure so that the deferred recover() is
	// scoped tightly to az.Analyze. This prevents accidentally catching panics
	// from the test framework (e.g. t.Fatal) or from loop-control code.
	analyzeFile(az, relPath, content, &result, logger)

	return result
}
