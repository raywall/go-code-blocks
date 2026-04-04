// blocks/cnab/parser.go
package cnab

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ── cnab240 record type codes (column 8) ─────────────────────────────────────

const (
	rt240FileHeader  = "0"
	rt240BatchHeader = "1"
	rt240Detail      = "3"
	rt240BatchTrail  = "5"
	rt240FileTrail   = "9"
)

// ── cnab400 record type codes (column 1) ─────────────────────────────────────

const (
	rt400Header  = "0"
	rt400Detail  = "1"
	rt400Trailer = "9"
)

// ── parse240 ──────────────────────────────────────────────────────────────────

func parse240(r io.Reader, cfg *blockConfig) (*ParseResult, error) {
	result := &ParseResult{Format: Format240}
	scanner := bufio.NewScanner(r)
	lineNum := 0

	var currentBatch *Batch

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Normalise length: CNAB 240 lines must be exactly 240 chars.
		line = normaliseLine(line, 240)

		// Record type is column 8 (index 7) for CNAB 240.
		if len([]rune(line)) < 8 {
			result.ParseErrors = append(result.ParseErrors, ParseError{
				Line:    lineNum,
				Message: fmt.Sprintf("line too short: %d chars", utf8.RuneCountInString(line)),
			})
			continue
		}

		recType := col(line, 8, 8)

		switch recType {
		case rt240FileHeader:
			rec, err := parseRecord(line, cfg.fileHeader, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			result.FileHeader = rec

		case rt240BatchHeader:
			rec, err := parseRecord(line, cfg.batchHeader, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			batch := &Batch{Header: rec, LineStart: lineNum}
			result.Batches = append(result.Batches, *batch)
			currentBatch = &result.Batches[len(result.Batches)-1]

		case rt240Detail:
			// Segment code is column 14 (index 13) for CNAB 240.
			segCode := strings.TrimSpace(col(line, 14, 14))
			layout, ok := cfg.segments[segCode]
			if !ok {
				if !cfg.skipUnknownSegments {
					result.ParseErrors = append(result.ParseErrors, ParseError{
						Line:    lineNum,
						Message: fmt.Sprintf("unknown segment %q", segCode),
					})
				}
				continue
			}
			rec, err := parseRecord(line, layout, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			seg := Segment{Code: segCode, Data: rec, Line: lineNum}
			if currentBatch != nil {
				currentBatch.Segments = append(currentBatch.Segments, seg)
			}

		case rt240BatchTrail:
			rec, err := parseRecord(line, cfg.batchTrailer, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			if currentBatch != nil {
				currentBatch.Trailer = rec
			}
			currentBatch = nil

		case rt240FileTrail:
			rec, err := parseRecord(line, cfg.fileTrailer, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			result.FileTrailer = rec

		default:
			result.ParseErrors = append(result.ParseErrors, ParseError{
				Line:    lineNum,
				Message: fmt.Sprintf("unknown record type %q", recType),
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("cnab240: scan: %w", err)
	}

	result.TotalLines = lineNum
	return result, nil
}

// ── parse400 ──────────────────────────────────────────────────────────────────

func parse400(r io.Reader, cfg *blockConfig) (*ParseResult, error) {
	result := &ParseResult{Format: Format400}
	scanner := bufio.NewScanner(r)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := normaliseLine(scanner.Text(), 400)

		if utf8.RuneCountInString(line) < 1 {
			continue
		}

		recType := col(line, 1, 1)

		switch recType {
		case rt400Header:
			rec, err := parseRecord(line, cfg.header, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			result.FileHeader = rec

		case rt400Detail:
			rec, err := parseRecord(line, cfg.detail, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			result.Details = append(result.Details, rec)

		case rt400Trailer:
			rec, err := parseRecord(line, cfg.trailer, cfg.dateLocation)
			if err != nil {
				result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Message: err.Error()})
			}
			result.FileTrailer = rec

		default:
			result.ParseErrors = append(result.ParseErrors, ParseError{
				Line:    lineNum,
				Message: fmt.Sprintf("unknown record type %q", recType),
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("cnab400: scan: %w", err)
	}

	result.TotalLines = lineNum
	return result, nil
}

// ── parseRecord ───────────────────────────────────────────────────────────────

// parseRecord extracts all fields defined in layout from line and returns
// a Record (map[string]any) with typed values.
func parseRecord(line string, layout RecordLayout, loc *time.Location) (Record, error) {
	if loc == nil {
		loc = time.UTC
	}
	runes := []rune(line)
	rec := make(Record, len(layout.Fields))

	for _, fd := range layout.Fields {
		if fd.Start < 1 || fd.End < fd.Start {
			return rec, fmt.Errorf("field %q: invalid position %d–%d", fd.Name, fd.Start, fd.End)
		}
		s := fd.Start - 1 // convert to 0-indexed
		e := fd.End
		if s >= len(runes) {
			rec[fd.Name] = zeroValue(fd)
			continue
		}
		if e > len(runes) {
			e = len(runes)
		}
		raw := string(runes[s:e])

		val, err := convertField(raw, fd, loc)
		if err != nil {
			return rec, fmt.Errorf("field %q (cols %d–%d): %w", fd.Name, fd.Start, fd.End, err)
		}
		rec[fd.Name] = val
	}
	return rec, nil
}

// ── Field conversion ──────────────────────────────────────────────────────────

func convertField(raw string, fd FieldDef, loc *time.Location) (any, error) {
	switch fd.Type {
	case Alpha:
		return strings.TrimRight(raw, " "), nil

	case Numeric:
		trimmed := strings.TrimLeft(raw, " 0")
		if trimmed == "" {
			return int64(0), nil
		}
		n, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("numeric: %q: %w", raw, err)
		}
		return n, nil

	case Decimal:
		trimmed := strings.TrimLeft(strings.TrimSpace(raw), "0")
		if trimmed == "" {
			return float64(0), nil
		}
		n, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decimal: %q: %w", raw, err)
		}
		divisor := int64(1)
		for i := 0; i < fd.DecimalPlaces; i++ {
			divisor *= 10
		}
		return float64(n) / float64(divisor), nil

	case Date:
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.Trim(trimmed, "0") == "" {
			return time.Time{}, nil
		}
		layout := fd.DateFormat
		if layout == "" {
			layout = "02012006"
		}
		t, err := time.ParseInLocation(layout, trimmed, loc)
		if err != nil {
			return nil, fmt.Errorf("date %q with layout %q: %w", trimmed, layout, err)
		}
		return t, nil

	case Boolean:
		trimmed := strings.TrimSpace(raw)
		return trimmed == "1" || strings.EqualFold(trimmed, "S"), nil

	default:
		return strings.TrimRight(raw, " "), nil
	}
}

// zeroValue returns the typed zero for a field when the line is too short.
func zeroValue(fd FieldDef) any {
	switch fd.Type {
	case Numeric:
		return int64(0)
	case Decimal:
		return float64(0)
	case Date:
		return time.Time{}
	case Boolean:
		return false
	default:
		return ""
	}
}

// ── Utilities ─────────────────────────────────────────────────────────────────

// col extracts rune-based columns from a CNAB line (1-indexed, inclusive).
func col(line string, start, end int) string {
	runes := []rune(line)
	s := start - 1
	e := end
	if s >= len(runes) {
		return ""
	}
	if e > len(runes) {
		e = len(runes)
	}
	return string(runes[s:e])
}

// normaliseLine ensures the line is exactly width runes, padding with spaces
// or trimming trailing carriage returns.
func normaliseLine(line string, width int) string {
	// Remove Windows carriage return
	line = strings.TrimRight(line, "\r")
	runes := []rune(line)
	if len(runes) == width {
		return line
	}
	if len(runes) > width {
		return string(runes[:width])
	}
	return line + strings.Repeat(" ", width-len(runes))
}
