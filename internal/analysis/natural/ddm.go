// Package natural: extraction of Data Definition Module (DDM) field definitions
// from `.NSD` files (feature 12-hover, task T2A).
//
// DDM files are NOT Natural source — they are fixed-column tabular report formats
// exported by NaturalONE / SPoD. They must NOT be routed through the Natural
// lexer/recursive-descent parser. Instead, this module implements a dedicated
// line-scanner that reads the fixed-column format and populates model.DataDefinition.
package natural

import (
	"strings"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// DDM fixed-column offsets (0-based byte positions, verified against natls and
// Software AG DDM Editor documentation):
//
//	T@0  (1 char)  — field type: ' '=elementary, 'G'=group, 'P'=periodic, 'M'=MU
//	L@2  (1 char)  — level digit 1–7
//	DB@4 (2 chars) — Adabas 2-char short name (not preserved)
//	Name@7  (32 chars) — field name, padded with spaces
//	F@41    (1 char)   — format letter (A, N, P, I, …)
//	Leng@43 (4 chars)  — length / precision (e.g. "8", "9,2")
//
// Columns S@49, D@51, and Remark@53+ are present in full lines but are not extracted.
const (
	ddmOffsetType   = 0
	ddmOffsetLevel  = 2
	ddmOffsetName   = 7
	ddmNameLen      = 32
	ddmOffsetFormat = 41
	ddmOffsetLength = 43
	ddmLengthLen    = 4
	// ddmMinFieldLen is the minimum line length required to read the Name column.
	// Lines shorter than this cannot carry a valid field row.
	ddmMinFieldLen = ddmOffsetName + 1 // at least one name byte
)

// extractDDMDefinitions parses the exported DDM report format from content
// and returns field definitions in source order.
//
// The format is fixed-column (not whitespace-delimited). Groups (T=G or T=P)
// have no format/length (empty Type) and contain higher-level children. Both
// multiple-value fields (T=M) and periodic groups (T=P) repeat, so each is
// tagged with a single unbounded (*) Dimensions entry — the DDM report records
// no occurrence count. A periodic group is thus both a group (empty Type,
// nested Children) and an array (one unbounded dimension); an ordinary group
// (T=G) carries no dimension.
//
// SQL DDMs (TYPE: SQL header) are out of scope — returns nil immediately.
// A nil return is not an error; it means "no Adabas field definitions to expose".
//
// Never panics; any line that cannot be parsed is skipped without error (FR-43).
func extractDDMDefinitions(content string) []model.DataDefinition {
	lines := splitLines(content)

	// Quick scan: if this is a SQL DDM, it is out of scope for field extraction.
	// SQL DDMs describe relational tables, not Adabas field layouts.
	for _, line := range lines {
		if isSQLTypeLine(line) {
			return nil
		}
		if isHeaderLine(line) {
			break // TYPE line not present; stop scanning header section
		}
	}

	var definitions []model.DataDefinition
	var stack []*model.DataDefinition // open groups awaiting children

	for lineNo, line := range lines {
		if shouldSkipLine(line) {
			continue
		}

		level, typeFlag, ok := parseLevelAndType(line)
		if !ok {
			continue
		}

		name := parseName(line)
		if name == "" {
			continue
		}

		ddType, dimensions := parseFormatAndDimensions(line, typeFlag)

		// Compute NameRange: the span of the field name in the source line (feature 28, T8b)
		nameRange := computeNameRange(line, lineNo)

		def := model.DataDefinition{
			Name:       name,
			Level:      level,
			Type:       ddType,
			Dimensions: dimensions,
			Range: model.Range{
				Start: model.Position{Line: lineNo + 1, Column: 1},
				End:   model.Position{Line: lineNo + 1, Column: len(line) + 1},
			},
			NameRange: nameRange,
		}

		// Pop the stack to the nearest ancestor with a strictly lower level.
		for len(stack) > 0 && stack[len(stack)-1].Level >= level {
			stack = stack[:len(stack)-1]
		}

		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, def)
			// A group that was just added as a child: push a pointer to it.
			if isGroupType(typeFlag) {
				stack = append(stack, &parent.Children[len(parent.Children)-1])
			}
		} else {
			definitions = append(definitions, def)
			// A top-level group: push a pointer to the last top-level entry.
			if isGroupType(typeFlag) {
				stack = append(stack, &definitions[len(definitions)-1])
			}
		}
	}

	return definitions
}

// isSQLTypeLine reports whether line is the "TYPE: SQL" header that marks a
// relational (SQL) DDM. Only an exact "SQL" token is matched so that names
// like "SQL-EXTENDED" are not accidentally suppressed.
func isSQLTypeLine(line string) bool {
	const prefix = "TYPE: "
	return len(line) >= len(prefix)+3 &&
		line[:len(prefix)] == prefix &&
		line[len(prefix):len(prefix)+3] == "SQL" &&
		(len(line) == len(prefix)+3 || line[len(prefix)+3] == ' ' || line[len(prefix)+3] == '\r')
}

// isHeaderLine reports whether line is any TYPE: header line (used to stop the
// SQL-scan loop once the header section ends).
func isHeaderLine(line string) bool {
	const prefix = "TYPE: "
	return len(line) >= len(prefix) && line[:len(prefix)] == prefix
}

// shouldSkipLine reports whether a DDM report line carries no field data and
// should be skipped during field extraction.
func shouldSkipLine(line string) bool {
	if len(line) == 0 {
		return true // blank line
	}
	if line[0] == '*' {
		return true // comment line (e.g. superdescriptor source-field annotations)
	}
	// DB: file header and TYPE: DDM type header.
	// Guard each prefix individually so a 5-char line "TYPE:" does not
	// cause an out-of-range slice on the 6-char "TYPE: " prefix.
	if len(line) >= 4 && line[:4] == "DB: " {
		return true
	}
	if len(line) >= 6 && line[:6] == "TYPE: " {
		return true
	}
	// Column-header row: "T L DB Name …"
	if len(line) > 1 && line[0] == 'T' && line[1] == ' ' && strings.Contains(line, "Name") {
		return true
	}
	// Dashed separator line between header and data rows.
	if isDashedLine(line) {
		return true
	}
	// End-of-report sentinel emitted by NaturalONE / SPoD.
	if strings.Contains(line, "DDM OUTPUT TERMINATED") {
		return true
	}
	return false
}

// parseLevelAndType extracts the level digit (L@2) and type flag (T@0) from a
// DDM data line. Returns (level, typeFlag, true) on success or (0, 0, false)
// when the line is too short or the level position holds a non-digit.
//
// A non-digit at L@2 is the reliable signal that this line is not a data row
// (header or noise rows that survived shouldSkipLine).
func parseLevelAndType(line string) (level int, typeFlag rune, ok bool) {
	if len(line) < ddmMinFieldLen {
		return 0, 0, false
	}
	if line[ddmOffsetLevel] < '0' || line[ddmOffsetLevel] > '9' {
		return 0, 0, false
	}
	level = int(line[ddmOffsetLevel] - '0')
	typeFlag = ' '
	switch line[ddmOffsetType] {
	case 'G', 'P', 'M':
		typeFlag = rune(line[ddmOffsetType])
	}
	return level, typeFlag, true
}

// parseName extracts and normalizes the field name from the Name column (offset 7,
// width 32). Names are trimmed of padding and uppercased. Returns "" when the name
// column is absent or empty after trimming.
func parseName(line string) string {
	if len(line) <= ddmOffsetName {
		return ""
	}
	end := ddmOffsetName + ddmNameLen
	if end > len(line) {
		end = len(line)
	}
	return strings.ToUpper(strings.TrimSpace(line[ddmOffsetName:end]))
}

// parseFormatAndDimensions extracts the format+length type string (columns F@41
// and Leng@43) and, for MU/PE fields, attaches a single unbounded Dimensions entry.
//
// A periodic group (T=P) is a group — it has no format/length columns, so its Type
// is empty and its children nest by level — but the group itself repeats, so it
// carries the same single unbounded (*) Dimensions entry as a multiple-value (MU)
// field: the DDM report records no occurrence count for either. An ordinary group
// (T=G) has no dimension. See .claude/knowledge/natural/ddm-format.md.
func parseFormatAndDimensions(line string, typeFlag rune) (ddType string, dimensions []model.ArrayDimension) {
	if isGroupType(typeFlag) {
		// A periodic group repeats: tag it with an unbounded dimension, but it
		// still has no format/length (empty Type), unlike an elementary MU field.
		if typeFlag == 'P' {
			return "", []model.ArrayDimension{{Lower: 1, UpperUnbounded: true}}
		}
		return "", nil
	}

	// F@41: single format letter (A, N, P, I, …). Blank or dash means absent.
	var format string
	if len(line) > ddmOffsetFormat {
		ch := line[ddmOffsetFormat]
		if ch != ' ' && ch != '-' {
			format = string(ch)
		}
	}

	// Leng@43: up to 4 chars for length/precision (e.g. "8", "9,2", "  50").
	var length string
	if len(line) > ddmOffsetLength {
		end := ddmOffsetLength + ddmLengthLen
		if end > len(line) {
			end = len(line)
		}
		length = strings.TrimSpace(line[ddmOffsetLength:end])
	}

	ddType = format + length

	// MU (multiple-value) fields carry an unbounded (*) dimension. The DDM report
	// does not record the occurrence count, so UpperUnbounded is set.
	if typeFlag == 'M' {
		dimensions = []model.ArrayDimension{{Lower: 1, UpperUnbounded: true}}
	}

	return ddType, dimensions
}

// isGroupType reports whether typeFlag designates a group or periodic-group field
// (T=G or T=P). Groups have no format/length columns and act as nesting containers.
func isGroupType(typeFlag rune) bool {
	return typeFlag == 'G' || typeFlag == 'P'
}

// splitLines splits content into lines, normalizing CRLF to LF first.
func splitLines(content string) []string {
	return strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

// isDashedLine reports whether every character in line is a dash or a space,
// matching the column-separator row in DDM reports.
func isDashedLine(line string) bool {
	for _, ch := range line {
		if ch != '-' && ch != ' ' {
			return false
		}
	}
	return true
}

// computeNameRange computes the NameRange (selection range for the field name) within a DDM line.
// The name column starts at fixed offset ddmOffsetName (7 bytes, 0-based).
// Since parseName trims the field, we reconstruct the name's actual byte span in the line.
//
// Returns a model.Range with Start and End (1-based line, 1-based column — model coordinates).
// The range spans from the first non-space character in the name field to the last.
// If the name cannot be located (short line, empty after trim), returns a zero Range (FR-43).
func computeNameRange(line string, lineNo int) model.Range {
	// Extract the 32-char name field from offset 7
	if len(line) <= ddmOffsetName {
		return model.Range{} // Line too short; return zero range
	}

	end := ddmOffsetName + ddmNameLen
	if end > len(line) {
		end = len(line)
	}
	nameField := line[ddmOffsetName:end]

	// Find the first and last non-space character within nameField
	first := -1
	last := -1
	for i, ch := range nameField {
		if ch != ' ' {
			if first == -1 {
				first = i
			}
			last = i
		}
	}

	// If the name field is all spaces, return zero range
	if first == -1 {
		return model.Range{}
	}

	// Compute byte offsets in the original line (0-based)
	nameStart := ddmOffsetName + first  // Start of the name (0-based)
	nameEnd := ddmOffsetName + last + 1 // End of the name (exclusive, 0-based)

	// Convert to model coordinates (1-based line, 1-based column)
	return model.Range{
		Start: model.Position{Line: lineNo + 1, Column: nameStart + 1},
		End:   model.Position{Line: lineNo + 1, Column: nameEnd + 1},
	}
}
