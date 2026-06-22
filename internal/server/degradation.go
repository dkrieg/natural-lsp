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
	// Build a set of recognized extensions for quick lookup (case-insensitive).
	recognizedExts := make(map[string]bool, len(cfg.Workspace.Extensions))
	for _, ext := range cfg.Workspace.Extensions {
		recognizedExts[strings.ToUpper(ext)] = true
	}

	results := make([]FileProcessResult, len(paths))

	for i, relPath := range paths {
		content := contents[relPath]
		result := FileProcessResult{
			RelPath:      relPath,
			ObjectType:   model.ObjectUnknown,
			SkipReason:   "",
			FileAnalysis: model.FileAnalysis{},
		}

		// Check: file exceeds max file size.
		if int64(len(content)) > cfg.Workspace.MaxFileSize {
			result.SkipReason = config.SkipTooLarge
			logger.Info("skipping file",
				slog.String("path", relPath),
				slog.String("reason", string(config.SkipTooLarge)),
			)
			results[i] = result
			continue
		}

		// Check: file is inside an excluded directory.
		if cfg.IsExcluded(relPath) {
			result.SkipReason = config.SkipExcluded
			logger.Info("skipping file",
				slog.String("path", relPath),
				slog.String("reason", string(config.SkipExcluded)),
			)
			results[i] = result
			continue
		}

		// Check: unrecognized extension — keep as ObjectUnknown, skip analysis.
		ext := strings.ToUpper(filepath.Ext(relPath))
		if !recognizedExts[ext] {
			results[i] = result
			continue
		}

		// Call the analyzer inside a closure so that the deferred recover() is
		// scoped tightly to az.Analyze. This prevents accidentally catching panics
		// from the test framework (e.g. t.Fatal) or from loop-control code.
		analyzeFile(az, relPath, content, &result, logger)

		results[i] = result
	}

	return results
}

// analyzeFile calls az.Analyze for a single file and populates result.
// A panic from az.Analyze is caught and logged; result is left at its zero
// value (ObjectUnknown) so the batch continues (FR-43).
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
