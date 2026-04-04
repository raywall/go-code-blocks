// blocks/cnab/block.go
//
// Package cnab provides a declarative CNAB 240 and CNAB 400 file reader block.
//
// CNAB (Centro Nacional de Automação Bancária) is the Brazilian banking file
// interchange standard. The two variants differ only in line width and record
// structure:
//
//	CNAB 240 — 240 chars/line; Header de Arquivo, Header de Lote,
//	           Segmentos (A, B, C…), Trailer de Lote, Trailer de Arquivo.
//
//	CNAB 400 — 400 chars/line; Header, Detalhe, Trailer.
//
// The block is configured by declaring field layouts that mirror the bank's
// technical specification document — field names, column positions (1-indexed),
// and data types. No parsing code needs to be written by the caller.
//
// # CNAB 240 example
//
//	reader := cnab.New("remessa-itau",
//	    cnab.WithFormat(cnab.Format240),
//
//	    cnab.WithFileHeader(
//	        cnab.NumericField("banco_codigo",   1,  3).Describe("Código do banco"),
//	        cnab.NumericField("lote",           4,  7),
//	        cnab.NumericField("tipo_registro",  8,  8),
//	        cnab.Field("nome_empresa",         73, 102),
//	        cnab.DateField("data_geracao",     144, 151),
//	        cnab.NumericField("sequencia",     158, 163),
//	    ),
//
//	    cnab.WithSegment("A",
//	        cnab.NumericField("banco_codigo_favorecido", 21, 23),
//	        cnab.NumericField("agencia",                 24, 28),
//	        cnab.Field("nome_favorecido",               43, 72),
//	        cnab.DecimalField("valor",                  120, 134, 2),
//	        cnab.DateField("data_pagamento",            145, 152),
//	    ),
//
//	    cnab.WithSegment("B",
//	        cnab.Field("logradouro",  19,  53),
//	        cnab.Field("cidade",      64,  83),
//	        cnab.Field("cep",         84,  91),
//	        cnab.Field("uf",          92,  93),
//	    ),
//
//	    cnab.WithFileTrailer(
//	        cnab.NumericField("total_lotes",    18, 23),
//	        cnab.NumericField("total_registros", 24, 29),
//	    ),
//	)
//
// # CNAB 400 example
//
//	reader := cnab.New("retorno-bradesco",
//	    cnab.WithFormat(cnab.Format400),
//
//	    cnab.WithHeader(
//	        cnab.NumericField("tipo_registro",  1,  1),
//	        cnab.NumericField("codigo_retorno", 2,  2),
//	        cnab.Field("nome_banco",           77, 94),
//	        cnab.DateField("data_geracao",     95, 100, "020106"),
//	    ),
//
//	    cnab.WithDetail(
//	        cnab.NumericField("tipo_registro",     1,   1),
//	        cnab.NumericField("codigo_ocorrencia", 109, 110),
//	        cnab.Field("nome_pagador",            218, 257),
//	        cnab.DecimalField("valor_titulo",      153, 165, 2),
//	        cnab.DateField("data_vencimento",      120, 125, "020106"),
//	        cnab.DateField("data_credito",         296, 301, "020106"),
//	    ),
//
//	    cnab.WithTrailer(
//	        cnab.NumericField("tipo_registro",      1,  1),
//	        cnab.NumericField("total_registros",   395, 400),
//	    ),
//	)
//
// # Parsing
//
//	// From a file path
//	result, err := reader.ParseFile(ctx, "/data/retorno.txt")
//
//	// From any io.Reader (uploaded file, S3 object, etc.)
//	result, err := reader.Parse(ctx, s3Body)
//
//	// Navigate results
//	fmt.Println(result.FileHeader["nome_empresa"])
//
//	for _, batch := range result.Batches {
//	    for _, seg := range batch.Segments {
//	        if seg.Code == "A" {
//	            fmt.Println(seg.Data["nome_favorecido"], seg.Data["valor"])
//	        }
//	    }
//	}
//
//	for _, detail := range result.Details {
//	    fmt.Println(detail["nome_pagador"], detail["valor_titulo"])
//	}
package cnab

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/core"
)

// Block is a CNAB file reader block.
// It holds the field layouts declared via options and exposes Parse / ParseFile
// methods that return a fully typed ParseResult.
type Block struct {
	name string
	cfg  blockConfig
}

// New creates a new CNAB Block.
//
//	reader := cnab.New("remessa",
//	    cnab.WithFormat(cnab.Format240),
//	    cnab.WithFileHeader( ... ),
//	    cnab.WithSegment("A", ... ),
//	    cnab.WithFileTrailer( ... ),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{
		segments:     make(map[string]RecordLayout),
		dateLocation: time.UTC,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block{name: name, cfg: cfg}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block. Validates the configuration.
func (b *Block) Init(_ context.Context) error {
	if b.cfg.format != Format240 && b.cfg.format != Format400 {
		return fmt.Errorf("cnab %q: format not configured; use WithFormat(cnab.Format240) or WithFormat(cnab.Format400)", b.name)
	}
	if b.cfg.format == Format240 && len(b.cfg.segments) == 0 {
		return fmt.Errorf("cnab %q: CNAB 240 requires at least one segment; use WithSegment", b.name)
	}
	if b.cfg.format == Format400 && len(b.cfg.detail.Fields) == 0 {
		return fmt.Errorf("cnab %q: CNAB 400 requires WithDetail", b.name)
	}
	return nil
}

// Shutdown implements core.Block. No-op — CNAB block holds no open resources.
func (b *Block) Shutdown(_ context.Context) error { return nil }

// Parse reads CNAB data from r and returns the fully typed ParseResult.
// The caller is responsible for closing r after Parse returns.
//
//	// From an S3 object
//	obj, _ := assets.GetObject(ctx, "retorno.txt")
//	defer obj.Close()
//	result, err := cnabBlock.Parse(ctx, obj)
func (b *Block) Parse(_ context.Context, r io.Reader) (*ParseResult, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}
	switch b.cfg.format {
	case Format240:
		return parse240(r, &b.cfg)
	case Format400:
		return parse400(r, &b.cfg)
	default:
		return nil, fmt.Errorf("cnab %q: unsupported format %d", b.name, b.cfg.format)
	}
}

// ParseFile opens the file at path and parses it. The file is closed when
// parsing completes.
//
//	result, err := cnabBlock.ParseFile(ctx, "/var/data/remessa.txt")
func (b *Block) ParseFile(ctx context.Context, path string) (*ParseResult, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cnab %q: open %q: %w", b.name, path, err)
	}
	defer f.Close()
	return b.Parse(ctx, f)
}

// Layout returns the configured field definitions for documentation or
// front-end form generation purposes.
// The returned map keys are record identifiers:
//   - "file_header", "batch_header", "batch_trailer", "file_trailer" (CNAB 240)
//   - "segment_A", "segment_B", … (CNAB 240 segments)
//   - "header", "detail", "trailer" (CNAB 400)
func (b *Block) Layout() map[string]RecordLayout {
	result := make(map[string]RecordLayout)
	switch b.cfg.format {
	case Format240:
		result["file_header"] = b.cfg.fileHeader
		result["batch_header"] = b.cfg.batchHeader
		result["batch_trailer"] = b.cfg.batchTrailer
		result["file_trailer"] = b.cfg.fileTrailer
		for code, layout := range b.cfg.segments {
			result["segment_"+code] = layout
		}
	case Format400:
		result["header"] = b.cfg.header
		result["detail"] = b.cfg.detail
		result["trailer"] = b.cfg.trailer
	}
	return result
}

// checkInit returns core.ErrNotInitialized when the block has not been
// initialised (Init never called or called before registering in Container).
func (b *Block) checkInit() error {
	if b.cfg.format == 0 {
		return fmt.Errorf("cnab %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

// Ensure Block implements core.Block.
var _ core.Block = (*Block)(nil)
