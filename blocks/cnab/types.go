// blocks/cnab/types.go
package cnab

import "time"

// ── Format ───────────────────────────────────────────────────────────────────

// Format identifies the CNAB line width standard.
type Format int

const (
	// Format240 is the CNAB 240 standard (FEBRABAN) — 240 characters per line.
	// Structure: FileHeader | BatchHeader | Segments (A, B, C…) | BatchTrailer | FileTrailer
	Format240 Format = 240

	// Format400 is the CNAB 400 standard — 400 characters per line.
	// Structure: FileHeader | Details | FileTrailer
	Format400 Format = 400
)

// ── Field type ────────────────────────────────────────────────────────────────

// FieldType determines how raw bytes are converted to a Go value.
type FieldType int

const (
	// Alpha is a left-aligned, space-padded string field.
	Alpha FieldType = iota

	// Numeric is a right-aligned, zero-padded integer field.
	Numeric

	// Decimal is a numeric field with an implicit decimal separator.
	// Use WithDecimalPlaces to set how many digits are fractional.
	// E.g. "000001234" with 2 decimal places → 12.34
	Decimal

	// Date parses the field as a date. Default format is DDMMYYYY.
	// Use WithDateFormat to override.
	Date

	// Boolean treats "1" or "S" as true, anything else as false.
	Boolean
)

// ── FieldDef ─────────────────────────────────────────────────────────────────

// FieldDef describes one field inside a CNAB record.
//
// Positions are 1-indexed to match bank documentation directly.
// A field spanning columns 1–3 means Start=1, End=3 (3 bytes).
type FieldDef struct {
	Name          string
	Start         int // 1-indexed, inclusive
	End           int // 1-indexed, inclusive
	Type          FieldType
	DecimalPlaces int    // Decimal fields only
	DateFormat    string // Date fields only; default "02012006" (DDMMYYYY)
	Description   string // Optional documentation
}

// Field creates a FieldDef with Alpha type. This is the most common field type.
//
//	cnab.Field("nome_empresa", 73, 102)
func Field(name string, start, end int) FieldDef {
	return FieldDef{Name: name, Start: start, End: end, Type: Alpha}
}

// NumericField creates a FieldDef with Numeric type.
//
//	cnab.NumericField("numero_documento", 74, 93)
func NumericField(name string, start, end int) FieldDef {
	return FieldDef{Name: name, Start: start, End: end, Type: Numeric}
}

// DecimalField creates a FieldDef with Decimal type and the given decimal places.
//
//	cnab.DecimalField("valor", 153, 167, 2)  // 15 digits, 2 decimal places
func DecimalField(name string, start, end, decimalPlaces int) FieldDef {
	return FieldDef{Name: name, Start: start, End: end, Type: Decimal, DecimalPlaces: decimalPlaces}
}

// DateField creates a FieldDef with Date type.
// format follows Go's time.Parse convention; defaults to "02012006" (DDMMYYYY).
//
//	cnab.DateField("data_pagamento", 94, 101)            // DDMMYYYY
//	cnab.DateField("data_vencimento", 94, 101, "020106") // DDMMYY
func DateField(name string, start, end int, format ...string) FieldDef {
	f := "02012006"
	if len(format) > 0 && format[0] != "" {
		f = format[0]
	}
	return FieldDef{Name: name, Start: start, End: end, Type: Date, DateFormat: f}
}

// BoolField creates a FieldDef with Boolean type.
//
//	cnab.BoolField("indicativo_pix", 198, 198)
func BoolField(name string, start, end int) FieldDef {
	return FieldDef{Name: name, Start: start, End: end, Type: Boolean}
}

// Describe adds a human-readable description to a FieldDef.
// Useful for documentation and for rendering forms in the front-end.
//
//	cnab.Field("banco_codigo", 1, 3).Describe("Código do banco compensação")
func (f FieldDef) Describe(description string) FieldDef {
	f.Description = description
	return f
}

// ── RecordLayout ─────────────────────────────────────────────────────────────

// RecordLayout holds the field definitions for one record type.
type RecordLayout struct {
	Fields []FieldDef
}

// ── Parsed result types ───────────────────────────────────────────────────────

// Record is a single parsed CNAB record: a map of field name → Go value.
// Values are typed according to their FieldDef:
//   - Alpha   → string
//   - Numeric → int64
//   - Decimal → float64
//   - Date    → time.Time
//   - Boolean → bool
type Record map[string]any

// Batch groups the header, detail segments and trailer of one CNAB 240 lote.
type Batch struct {
	// Header holds the parsed Lote Header (record type 1).
	Header Record
	// Segments is an ordered list of all detail segments in this batch.
	Segments []Segment
	// Trailer holds the parsed Lote Trailer (record type 5).
	Trailer Record
	// LineStart is the 1-indexed line number of the batch header in the file.
	LineStart int
}

// Segment is a single CNAB 240 detalhe record (record type 3).
type Segment struct {
	// Code is the segment code letter (A, B, C, J, O, …).
	Code string
	// Data holds the parsed fields of this segment.
	Data Record
	// Line is the 1-indexed line number in the file.
	Line int
}

// ParseResult is the top-level result of parsing a CNAB file.
type ParseResult struct {
	// Format is the detected (or configured) CNAB format.
	Format Format
	// FileHeader holds the parsed file header record (record type 0 for CNAB 240).
	FileHeader Record
	// Batches holds the parsed batches (CNAB 240 only).
	Batches []Batch
	// Details holds the parsed detail records (CNAB 400 only).
	Details []Record
	// FileTrailer holds the parsed file trailer record (record type 9).
	FileTrailer Record
	// TotalLines is the number of lines read from the file.
	TotalLines int
	// ParseErrors contains non-fatal per-line errors encountered during parsing.
	ParseErrors []ParseError
}

// ParseError describes a non-fatal error on a specific line.
type ParseError struct {
	Line    int
	Message string
}

// ── Block internal config (used by options) ───────────────────────────────────

type blockConfig struct {
	format Format

	// CNAB 240
	fileHeader   RecordLayout
	batchHeader  RecordLayout
	segments     map[string]RecordLayout // key = segment code, e.g. "A", "B"
	batchTrailer RecordLayout
	fileTrailer  RecordLayout

	// CNAB 400
	header  RecordLayout
	detail  RecordLayout
	trailer RecordLayout

	// Parsing behaviour
	skipUnknownSegments bool
	dateLocation        *time.Location
}
