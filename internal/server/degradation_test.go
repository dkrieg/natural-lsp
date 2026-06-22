package server

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// panicAnalyzer is a test analyzer that panics for paths containing "panicking"
// and succeeds for all others. Used to test panic recovery in ProcessFiles.
type panicAnalyzer struct {
	underlyingResult model.FileAnalysis
}

func (pa *panicAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	if filepath.Base(path) == "panicking.NSP" {
		panic(fmt.Sprintf("analyzer panic on path: %s", path))
	}
	return pa.underlyingResult, nil
}

// TestProcessFiles tests per-file graceful degradation (FR-43):
// - Skip files exceeding max_file_size with SkipTooLarge
// - Skip files matching excluded paths with SkipExcluded
// - Process unrecognized extensions as ObjectUnknown (not dropped)
// - Recover from analyzer panics without aborting the batch
// - Observable (log) line emitted for each skip/recovery
//
// Test batch: [good, oversized, unknown-ext, panicking]
func TestProcessFiles(t *testing.T) {
	testCases := []struct {
		name            string
		maxFileSize     int64
		excludePatterns []string
		// Each file in batch: path, content, expectedObjectType, expectedSkip, expectedLog
		files []struct {
			relPath            string
			content            []byte
			expectedObjectType model.ObjectType
			expectedSkipReason config.SkipReason
			expectLogOnSkip    bool
		}
	}{
		{
			name:            "GoodOversizedUnknownAndPanicking_FR43",
			maxFileSize:     100, // 100 bytes limit
			excludePatterns: []string{},
			files: []struct {
				relPath            string
				content            []byte
				expectedObjectType model.ObjectType
				expectedSkipReason config.SkipReason
				expectLogOnSkip    bool
			}{
				{
					relPath:            "good.NSP",
					content:            []byte("* Good program\nWRITE 'HELLO'\nEND"),
					expectedObjectType: model.ObjectProgram,
					expectedSkipReason: "",
					expectLogOnSkip:    false,
				},
				{
					relPath:            "oversized.NSP",
					content:            []byte("* This file has more than 100 bytes of content and should be skipped due to exceeding max_file_size configuration limit set in the test"),
					expectedObjectType: model.ObjectUnknown, // should not be analyzed
					expectedSkipReason: config.SkipTooLarge,
					expectLogOnSkip:    true,
				},
				{
					relPath:            "unknown-ext.txt",
					content:            []byte("not a natural file"),
					expectedObjectType: model.ObjectUnknown,
					expectedSkipReason: "",
					expectLogOnSkip:    false,
				},
				{
					relPath:            "panicking.NSP",
					content:            []byte("triggers panic in analyzer"),
					expectedObjectType: model.ObjectUnknown, // fallback on panic
					expectedSkipReason: "",                  // treated as error recovery, not a skip
					expectLogOnSkip:    true,
				},
			},
		},
		{
			name:            "ExcludedDirectory_FR43",
			maxFileSize:     5_000_000,
			excludePatterns: []string{"archive"},
			files: []struct {
				relPath            string
				content            []byte
				expectedObjectType model.ObjectType
				expectedSkipReason config.SkipReason
				expectLogOnSkip    bool
			}{
				{
					relPath:            "src/program.NSP",
					content:            []byte("* Program\nWRITE 'OK'\nEND"),
					expectedObjectType: model.ObjectProgram,
					expectedSkipReason: "",
					expectLogOnSkip:    false,
				},
				{
					relPath:            "archive/old.NSP",
					content:            []byte("* Old program\nWRITE 'OLD'\nEND"),
					expectedObjectType: model.ObjectUnknown,
					expectedSkipReason: config.SkipExcluded,
					expectLogOnSkip:    true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up config with the max file size and exclude patterns
			cfg := config.Defaults()
			cfg.Workspace.MaxFileSize = tc.maxFileSize
			cfg.Workspace.Exclude = tc.excludePatterns

			// Create a test logger to capture log output
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Create the analyzer stub (succeeds for normal paths, panics on .panic paths)
			az := &panicAnalyzer{
				underlyingResult: model.FileAnalysis{ObjectType: model.ObjectProgram},
			}

			// Prepare the file batch: ordered paths and path -> content map.
			// Paths are provided in tc.files definition order so that results[i]
			// corresponds to tc.files[i] — ProcessFiles preserves caller-supplied order.
			orderedPaths := make([]string, len(tc.files))
			contents := make(map[string][]byte, len(tc.files))
			for i, f := range tc.files {
				orderedPaths[i] = f.relPath
				contents[f.relPath] = f.content
			}

			// Act: call ProcessFiles with the ordered batch
			results := ProcessFiles(cfg, az, orderedPaths, contents, logger)

			// Assert: check results for each file
			for i, expectedFile := range tc.files {
				if i >= len(results) {
					t.Errorf("expected result for file %d (%s), got none", i, expectedFile.relPath)
					continue
				}

				result := results[i]

				// Assert: check that ObjectType matches expectation
				if result.ObjectType != expectedFile.expectedObjectType {
					t.Errorf("file %s: ObjectType = %v, want %v",
						expectedFile.relPath, result.ObjectType, expectedFile.expectedObjectType)
				}

				// Assert: check skip reason
				if expectedFile.expectedSkipReason != "" {
					if result.SkipReason != expectedFile.expectedSkipReason {
						t.Errorf("file %s: SkipReason = %v, want %v",
							expectedFile.relPath, result.SkipReason, expectedFile.expectedSkipReason)
					}
				} else {
					if result.SkipReason != "" {
						t.Errorf("file %s: SkipReason = %v, want empty",
							expectedFile.relPath, result.SkipReason)
					}
				}

				// Assert: check for log output on skip/recovery
				if expectedFile.expectLogOnSkip {
					logContent := logBuf.String()
					if logContent == "" {
						t.Errorf("file %s: expected log line for skip/recovery, got none", expectedFile.relPath)
					}
					// Check that the file path appears in the log
					if !bytes.Contains([]byte(logContent), []byte(expectedFile.relPath)) {
						t.Errorf("file %s: log output = %q", expectedFile.relPath, logContent)
						// Note: This may not always contain the exact path depending on log structure,
						// but we expect it to be mentioned somewhere for observability
					}
				}
			}

			// Assert: verify we got results for all input files
			if len(results) != len(tc.files) {
				t.Errorf("expected %d results, got %d", len(tc.files), len(results))
			}
		})
	}
}

// FuzzProcessFile is the executable proof of the FR-43 liveness property:
// a batch of files — even with arbitrary path names and bytes — must never
// panic the server. ProcessFiles (and the analysis path it wraps) must always
// return control to the caller, even for malformed/unrecognized inputs.
//
// The real analysis/natural.Analyzer is used behind the seam so the fuzz
// exercises classification + the T5 recovery wrapper together (ADR-013).
//
// The seed corpus is drawn from existing testdata/objecttype/ fixtures
// (representing known-good Natural code) plus an empty input, anchoring
// the mutation engine in representative patterns.
//
// Feature 03 Task T8; FR-43, ADR-013.
func FuzzProcessFile(f *testing.F) {
	// Seed corpus: a couple of existing fixture files + empty input
	for _, name := range []string{
		"program.NSP",
		"copycode.NSC",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "objecttype", name))
		if err != nil {
			f.Fatalf("seed %s: %v", name, err)
		}
		f.Add(name, data)
	}
	// Add an empty input to test edge case
	f.Add("empty.NSP", []byte{})

	f.Fuzz(func(t *testing.T, path string, content []byte) {
		// Arrange: use defaults config and the real natural analyzer
		cfg := config.Defaults()
		az := natural.New(nil) // no custom extension mappings for this test

		// Use a silent logger (discard output) for fuzzing so the output doesn't
		// become noisy with thousands of log lines during mutation
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))

		// Act: process a single-file batch with arbitrary path and content.
		// The "never panics" property is enforced by the fuzzer automatically:
		// any panic detected is reported as a failure and a crash-input written
		// to the testdata/fuzz/ corpus.
		results := ProcessFiles(cfg, az, []string{path}, map[string][]byte{path: content}, logger)

		// Assert: ProcessFiles always returns exactly one result (the input batch
		// had exactly one file) and it never returns nil (even on unrecognized
		// extensions, it returns a FileProcessResult with ObjectUnknown).
		if len(results) != 1 {
			t.Errorf("ProcessFiles returned %d results for 1-file batch, want 1", len(results))
		}
		if results[0].RelPath != path {
			t.Errorf("result path %q, want %q", results[0].RelPath, path)
		}
	})
}

// TestAnalyzeOne characterizes the extracted single-file FR-43-safe analysis helper
// (Feature 04 Task 3). It verifies that analyzeOne correctly:
//  1. Analyzes a recognized file with valid content → correct ObjectType, no SkipReason
//  2. Skips oversized files → SkipTooLarge, zero FileAnalysis
//  3. Skips excluded paths → SkipExcluded, zero FileAnalysis
//  4. Classifies unrecognized extensions → ObjectUnknown, no skip (degradation, not skip)
//  5. Recovers from analyzer panics → zero FileAnalysis, no panic escape
//
// Feature 04 Task 3; FR-43.
func TestAnalyzeOne(t *testing.T) {
	testCases := []struct {
		name               string
		relPath            string
		content            []byte
		maxFileSize        int64
		excludePatterns    []string
		shouldTriggerPanic bool // if true, use panicAnalyzer and relPath must be "panicking.NSP"
		expectedObjectType model.ObjectType
		expectedSkipReason config.SkipReason
		expectLogOnEvent   bool // true if we expect a log line (skip or panic recovery)
	}{
		{
			name:               "RecognizedFileWithValidContent",
			relPath:            "src/good.NSP",
			content:            []byte("* Good program\nWRITE 'HELLO'\nEND"),
			maxFileSize:        5_000_000,
			excludePatterns:    []string{},
			shouldTriggerPanic: false,
			expectedObjectType: model.ObjectProgram,
			expectedSkipReason: "",
			expectLogOnEvent:   false,
		},
		{
			name:               "FilesExceedingMaxFileSize_FR43",
			relPath:            "src/oversized.NSP",
			content:            []byte("x x x x x x x x x x x x x x x x x x x x x x x x x x x x x x"), // 59 bytes
			maxFileSize:        50,
			excludePatterns:    []string{},
			shouldTriggerPanic: false,
			expectedObjectType: model.ObjectUnknown, // not analyzed
			expectedSkipReason: config.SkipTooLarge,
			expectLogOnEvent:   true,
		},
		{
			name:               "ExcludedPath_FR43",
			relPath:            "archive/old.NSP",
			content:            []byte("* Program\nEND"),
			maxFileSize:        5_000_000,
			excludePatterns:    []string{"archive"},
			shouldTriggerPanic: false,
			expectedObjectType: model.ObjectUnknown, // not analyzed
			expectedSkipReason: config.SkipExcluded,
			expectLogOnEvent:   true,
		},
		{
			name:               "UnrecognizedExtension_Degradation",
			relPath:            "data/readme.txt",
			content:            []byte("plain text file"),
			maxFileSize:        5_000_000,
			excludePatterns:    []string{},
			shouldTriggerPanic: false,
			expectedObjectType: model.ObjectUnknown,
			expectedSkipReason: "", // degradation: not skipped, just classified as unknown
			expectLogOnEvent:   false,
		},
		{
			name:               "AnalyzerPanic_Recovered_FR43",
			relPath:            "panicking.NSP",
			content:            []byte("* Program\nEND"),
			maxFileSize:        5_000_000,
			excludePatterns:    []string{},
			shouldTriggerPanic: true,
			expectedObjectType: model.ObjectUnknown, // fallback on recovery
			expectedSkipReason: "",                  // not a skip, it's a recovery
			expectLogOnEvent:   true,
		},
		{
			name:               "ZeroMaxFileSize_NoLimit_FR43",
			relPath:            "src/large.NSP",
			content:            []byte("* Large program\nWRITE 'This file is large but MaxFileSize=0 means no limit'\nEND"),
			maxFileSize:        0, // 0 = no limit (consistent with watcher in sync.go)
			excludePatterns:    []string{},
			shouldTriggerPanic: false,
			expectedObjectType: model.ObjectProgram, // should be analyzed, not skipped
			expectedSkipReason: "",                  // no skip when MaxFileSize == 0
			expectLogOnEvent:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up config with max file size and exclude patterns
			cfg := config.Defaults()
			cfg.Workspace.MaxFileSize = tc.maxFileSize
			cfg.Workspace.Exclude = tc.excludePatterns

			// Create a test logger to capture log output
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Create the analyzer stub (uses panicAnalyzer which panics for "panicking.NSP")
			az := &panicAnalyzer{
				underlyingResult: model.FileAnalysis{ObjectType: model.ObjectProgram},
			}

			// Act: call analyzeOne with the test file
			result := analyzeOne(cfg, az, tc.relPath, tc.content, logger)

			// Assert: check the result fields
			if result.RelPath != tc.relPath {
				t.Errorf("RelPath = %q, want %q", result.RelPath, tc.relPath)
			}

			if result.ObjectType != tc.expectedObjectType {
				t.Errorf("ObjectType = %v, want %v", result.ObjectType, tc.expectedObjectType)
			}

			if result.SkipReason != tc.expectedSkipReason {
				t.Errorf("SkipReason = %q, want %q", result.SkipReason, tc.expectedSkipReason)
			}

			// Assert: check for log output on skip/recovery events
			logContent := logBuf.String()
			if tc.expectLogOnEvent {
				if logContent == "" {
					t.Errorf("expected log line for skip/recovery, got none")
				}
				// Verify the path appears in the log
				if !bytes.Contains([]byte(logContent), []byte(tc.relPath)) {
					t.Errorf("expected relPath %q in log, got: %s", tc.relPath, logContent)
				}
			} else {
				if logContent != "" {
					t.Errorf("expected no log output, got: %s", logContent)
				}
			}

			// Assert: verify FileAnalysis is empty when skipped
			// (It's the zero value on skip or panic recovery)
			if tc.expectedSkipReason != "" && result.FileAnalysis.ObjectType != "" {
				t.Errorf("when skip reason is set, FileAnalysis should be zero value, got ObjectType %v", result.FileAnalysis.ObjectType)
			}

			// If panic was expected, verify it didn't escape (test passes = no panic)
			if tc.shouldTriggerPanic {
				// Test framework would catch a panic, so absence of panic here means recovery worked
				_ = true
			}
		})
	}
}
