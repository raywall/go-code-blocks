// blocks/cnab/options.go
package cnab

import "time"

// Option configures a CNAB Block.
type Option func(*blockConfig)

// ── Format ────────────────────────────────────────────────────────────────────

// WithFormat sets the CNAB line width. Required.
// Use Format240 for FEBRABAN CNAB 240 or Format400 for CNAB 400.
func WithFormat(f Format) Option {
	return func(c *blockConfig) { c.format = f }
}

// ── CNAB 240 layouts ──────────────────────────────────────────────────────────

// WithFileHeader defines the fields for the CNAB 240 file header (registro tipo 0).
//
//	cnab.WithFileHeader(
//	    cnab.NumericField("banco_codigo",    1,  3).Describe("Código do banco"),
//	    cnab.NumericField("lote",            4,  7).Describe("Lote de serviço"),
//	    cnab.NumericField("tipo_registro",   8,  8).Describe("Tipo de registro"),
//	    cnab.Field("nome_empresa",          73, 102).Describe("Nome da empresa"),
//	    cnab.DateField("data_geracao",      144, 151).Describe("Data de geração"),
//	)
func WithFileHeader(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.fileHeader = RecordLayout{Fields: fields} }
}

// WithBatchHeader defines the fields for the CNAB 240 lote header (registro tipo 1).
func WithBatchHeader(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.batchHeader = RecordLayout{Fields: fields} }
}

// WithSegment defines the fields for a CNAB 240 detalhe segment identified
// by its code letter (A, B, C, J, O, N, …).
// The segment code is read from column 14 of each detail line.
//
//	cnab.WithSegment("A",
//	    cnab.NumericField("banco_codigo_favorecido", 21, 23),
//	    cnab.NumericField("agencia",                 24, 28),
//	    cnab.Field("nome_favorecido",               43, 72),
//	    cnab.DecimalField("valor",                  120, 134, 2),
//	)
func WithSegment(code string, fields ...FieldDef) Option {
	return func(c *blockConfig) {
		if c.segments == nil {
			c.segments = make(map[string]RecordLayout)
		}
		c.segments[code] = RecordLayout{Fields: fields}
	}
}

// WithBatchTrailer defines the fields for the CNAB 240 lote trailer (registro tipo 5).
func WithBatchTrailer(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.batchTrailer = RecordLayout{Fields: fields} }
}

// WithFileTrailer defines the fields for the CNAB 240 file trailer (registro tipo 9).
func WithFileTrailer(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.fileTrailer = RecordLayout{Fields: fields} }
}

// ── CNAB 400 layouts ──────────────────────────────────────────────────────────

// WithHeader defines the fields for the CNAB 400 file header.
// The header record is identified by record type "0" in column 1.
//
//	cnab.WithHeader(
//	    cnab.NumericField("tipo_registro", 1, 1),
//	    cnab.NumericField("codigo_retorno", 2, 2),
//	    cnab.Field("nome_banco", 77, 94),
//	    cnab.DateField("data_geracao", 95, 100, "020106"), // DDMMYY
//	)
func WithHeader(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.header = RecordLayout{Fields: fields} }
}

// WithDetail defines the fields for CNAB 400 detail records.
// Detail records are identified by record type "1" in column 1.
//
//	cnab.WithDetail(
//	    cnab.NumericField("tipo_registro",      1,   1),
//	    cnab.Field("nome_pagador",             218, 257),
//	    cnab.DecimalField("valor_titulo",       153, 165, 2),
//	    cnab.DateField("data_vencimento",       120, 125, "020106"),
//	    cnab.NumericField("codigo_ocorrencia",  109, 110),
//	)
func WithDetail(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.detail = RecordLayout{Fields: fields} }
}

// WithTrailer defines the fields for the CNAB 400 file trailer.
// The trailer record is identified by record type "9" in column 1.
func WithTrailer(fields ...FieldDef) Option {
	return func(c *blockConfig) { c.trailer = RecordLayout{Fields: fields} }
}

// ── Behaviour options ─────────────────────────────────────────────────────────

// WithSkipUnknownSegments instructs the parser to silently ignore CNAB 240
// segments whose code is not registered via WithSegment, instead of recording
// a ParseError. Useful when you only care about a subset of segments.
func WithSkipUnknownSegments() Option {
	return func(c *blockConfig) { c.skipUnknownSegments = true }
}

// WithDateLocation sets the time.Location used when parsing Date fields.
// Defaults to time.UTC.
func WithDateLocation(loc *time.Location) Option {
	return func(c *blockConfig) { c.dateLocation = loc }
}
