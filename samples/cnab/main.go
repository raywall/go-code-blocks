// samples/cnab/main.go
//
// Demonstra a leitura de arquivos CNAB 240 e CNAB 400 usando o bloco cnab.
//
// O sample cria arquivos de teste em memória para não depender de arquivos
// reais, mas a mesma API funciona com qualquer io.Reader (arquivo em disco,
// objeto S3, upload HTTP, etc.).
//
// Layouts usados:
//
//	CNAB 240 — Layout de pagamentos FEBRABAN (simplificado)
//	CNAB 400 — Layout de retorno bancário (simplificado)
package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/blocks/cnab"
	"github.com/raywall/go-code-blocks/core"
)

func main() {
	ctx := context.Background()

	// ── Bloco CNAB 240 ────────────────────────────────────────────────────────
	//
	// Layout de remessa de pagamentos (FEBRABAN CNAB 240).
	// Cada campo declara nome, posição inicial, posição final (1-indexed)
	// e tipo — exatamente como aparece no manual técnico do banco.

	remessa := cnab.New("remessa-pagamentos",
		cnab.WithFormat(cnab.Format240),

		// Registro tipo 0 — Header de Arquivo
		cnab.WithFileHeader(
			cnab.NumericField("banco_codigo", 1, 3).Describe("Código do banco na compensação"),
			cnab.NumericField("lote", 4, 7).Describe("Lote de serviço (0000 no header)"),
			cnab.NumericField("tipo_registro", 8, 8).Describe("Tipo de registro (0=Header Arquivo)"),
			cnab.Field("brancos", 9, 17).Describe("Uso FEBRABAN/CNAB"),
			cnab.NumericField("tipo_inscricao", 18, 18).Describe("Tipo de inscrição da empresa"),
			cnab.NumericField("cnpj_cpf", 19, 32).Describe("Número de inscrição da empresa"),
			cnab.Field("convenio", 33, 52).Describe("Código do convênio no banco"),
			cnab.NumericField("agencia", 53, 57).Describe("Agência mantenedora da conta"),
			cnab.Field("agencia_dv", 58, 58),
			cnab.NumericField("conta", 59, 70).Describe("Número da conta corrente"),
			cnab.Field("conta_dv", 71, 71),
			cnab.Field("dac", 72, 72).Describe("Dígito verificador da agência/conta"),
			cnab.Field("nome_empresa", 73, 102).Describe("Nome da empresa"),
			cnab.Field("nome_banco", 103, 132).Describe("Nome do banco"),
			cnab.Field("uso_febraban", 133, 142),
			cnab.NumericField("codigo_remessa", 143, 143).Describe("Código remessa/retorno (1=remessa)"),
			cnab.DateField("data_geracao", 144, 151).Describe("Data de geração do arquivo DDMMAAAA"),
			cnab.NumericField("hora_geracao", 152, 157).Describe("Hora de geração HHMMSS"),
			cnab.NumericField("sequencia", 158, 163).Describe("Número sequencial do arquivo"),
			cnab.NumericField("versao_layout", 164, 166).Describe("Número da versão do layout"),
			cnab.NumericField("densidade", 167, 171).Describe("Densidade de gravação em BPI"),
		),

		// Registro tipo 1 — Header de Lote
		cnab.WithBatchHeader(
			cnab.NumericField("banco_codigo", 1, 3),
			cnab.NumericField("lote", 4, 7).Describe("Número sequencial do lote"),
			cnab.NumericField("tipo_registro", 8, 8).Describe("Tipo de registro (1=Header Lote)"),
			cnab.Field("operacao", 9, 9).Describe("C=Crédito, D=Débito"),
			cnab.NumericField("tipo_servico", 10, 11).Describe("20=Pagamento Fornecedor, 30=Salários"),
			cnab.NumericField("forma_lancamento", 12, 13),
			cnab.NumericField("versao_lote", 14, 16),
		),

		// Registro tipo 3, Segmento A — Dados do pagamento
		cnab.WithSegment("A",
			cnab.NumericField("banco_codigo", 1, 3),
			cnab.NumericField("lote", 4, 7),
			cnab.NumericField("tipo_registro", 8, 8),
			cnab.NumericField("numero_registro", 9, 13).Describe("Número sequencial no lote"),
			cnab.Field("segmento", 14, 14).Describe("Código do segmento (A)"),
			cnab.NumericField("tipo_movimento", 15, 15).Describe("0=Inclusão, 5=Alteração"),
			cnab.NumericField("instrucao_movimento", 16, 17),
			cnab.NumericField("banco_favorecido", 21, 23),
			cnab.NumericField("agencia_favorecido", 24, 28),
			cnab.Field("agencia_favorecido_dv", 29, 29),
			cnab.NumericField("conta_favorecido", 30, 41),
			cnab.Field("conta_favorecido_dv", 42, 42),
			cnab.Field("dac_favorecido", 43, 43),
			cnab.Field("nome_favorecido", 44, 73).Describe("Nome do favorecido"),
			cnab.Field("documento_empresa", 74, 93).Describe("Nº do documento na empresa"),
			cnab.DateField("data_pagamento", 94, 101).Describe("Data do pagamento DDMMAAAA"),
			cnab.Field("tipo_moeda", 102, 104).Describe("BRL, USD…"),
			cnab.DecimalField("valor", 120, 134, 2).Describe("Valor do pagamento (2 decimais)"),
			cnab.Field("autenticacao_banco", 135, 154),
			cnab.Field("nosso_numero", 155, 174).Describe("Nosso número"),
			cnab.NumericField("codigo_ocorrencia", 231, 232).Describe("00=Não ocorrência"),
		),

		// Registro tipo 3, Segmento B — Informações complementares
		cnab.WithSegment("B",
			cnab.NumericField("banco_codigo", 1, 3),
			cnab.NumericField("lote", 4, 7),
			cnab.NumericField("tipo_registro", 8, 8),
			cnab.NumericField("numero_registro", 9, 13),
			cnab.Field("segmento", 14, 14),
			cnab.NumericField("tipo_inscricao", 18, 18).Describe("1=CPF, 2=CNPJ"),
			cnab.NumericField("cnpj_cpf_favorecido", 19, 32),
			cnab.Field("logradouro", 33, 62),
			cnab.NumericField("numero", 63, 67),
			cnab.Field("complemento", 68, 82),
			cnab.Field("bairro", 83, 97),
			cnab.Field("cidade", 98, 117),
			cnab.NumericField("cep", 118, 122),
			cnab.Field("cep_sufixo", 123, 125),
			cnab.Field("uf", 126, 127),
			cnab.Field("aviso", 228, 228).Describe("S=sim, N=não"),
		),

		// Registro tipo 5 — Trailer de Lote
		cnab.WithBatchTrailer(
			cnab.NumericField("banco_codigo", 1, 3),
			cnab.NumericField("lote", 4, 7),
			cnab.NumericField("tipo_registro", 8, 8),
			cnab.NumericField("qtd_registros", 18, 23).Describe("Quantidade de registros no lote"),
			cnab.DecimalField("soma_valores", 24, 41, 2).Describe("Somatória dos valores"),
		),

		// Registro tipo 9 — Trailer de Arquivo
		cnab.WithFileTrailer(
			cnab.NumericField("banco_codigo", 1, 3),
			cnab.NumericField("lote", 4, 7),
			cnab.NumericField("tipo_registro", 8, 8),
			cnab.NumericField("total_lotes", 18, 23).Describe("Quantidade de lotes"),
			cnab.NumericField("total_registros", 24, 29).Describe("Quantidade de registros do arquivo"),
		),
	)

	// ── Bloco CNAB 400 ────────────────────────────────────────────────────────
	//
	// Layout de retorno bancário (CNAB 400, simplificado).

	retorno := cnab.New("retorno-cobranca",
		cnab.WithFormat(cnab.Format400),

		cnab.WithHeader(
			cnab.NumericField("tipo_registro", 1, 1).Describe("0=Header"),
			cnab.NumericField("codigo_retorno", 2, 2).Describe("2=Retorno"),
			cnab.Field("zeros", 3, 9),
			cnab.Field("codigo_banco_cedente", 26, 46),
			cnab.Field("nome_banco", 77, 94).Describe("Nome do banco"),
			cnab.DateField("data_geracao", 95, 100, "020106").Describe("Data de gravação DDMMAA"),
			cnab.NumericField("sequencia", 395, 400).Describe("Número sequencial do arquivo"),
		),

		cnab.WithDetail(
			cnab.NumericField("tipo_registro", 1, 1).Describe("1=Detalhe"),
			cnab.Field("agencia_cedente", 25, 29),
			cnab.Field("conta_cedente", 30, 36),
			cnab.Field("nosso_numero", 71, 82).Describe("Identificação do título no banco"),
			cnab.NumericField("codigo_ocorrencia", 109, 110).Describe("06=Liquidação, 02=Entrada"),
			cnab.DateField("data_ocorrencia", 111, 116, "020106").Describe("Data da ocorrência DDMMAA"),
			cnab.Field("numero_documento", 117, 126).Describe("Número do documento"),
			cnab.DateField("data_vencimento", 147, 152, "020106"),
			cnab.DecimalField("valor_titulo", 153, 165, 2).Describe("Valor do título em centavos"),
			cnab.DecimalField("valor_desconto", 172, 183, 2),
			cnab.DecimalField("valor_abatimento", 184, 195, 2),
			cnab.DecimalField("valor_iof", 196, 207, 2),
			cnab.DecimalField("valor_pago", 208, 219, 2).Describe("Valor pago pelo sacado"),
			cnab.DecimalField("valor_liquido", 220, 231, 2).Describe("Valor líquido a ser creditado"),
			cnab.DecimalField("valor_outras_deducoes", 232, 243, 2),
			cnab.DecimalField("valor_outros_creditos", 244, 255, 2),
			cnab.Field("nome_pagador", 218, 257),
			cnab.DateField("data_credito", 296, 301, "020106").Describe("Data prevista de crédito"),
			cnab.NumericField("sequencia", 395, 400),
		),

		cnab.WithTrailer(
			cnab.NumericField("tipo_registro", 1, 1).Describe("9=Trailer"),
			cnab.NumericField("total_registros", 395, 400).Describe("Número de registros incluindo header/trailer"),
		),
	)

	// ── Container ─────────────────────────────────────────────────────────────

	app := core.NewContainer()
	app.MustRegister(remessa)
	app.MustRegister(retorno)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── Exemplo 1: Parse de CNAB 240 ─────────────────────────────────────────

	slog.Info("═══ CNAB 240 — Parse de remessa de pagamentos ═══")

	cnab240Data := build240Sample()
	result240, err := remessa.Parse(ctx, bytes.NewReader(cnab240Data))
	if err != nil {
		slog.Error("parse 240 failed", "err", err)
		os.Exit(1)
	}

	slog.Info("arquivo parseado",
		"linhas", result240.TotalLines,
		"lotes", len(result240.Batches),
		"erros", len(result240.ParseErrors),
	)

	if result240.FileHeader != nil {
		slog.Info("header",
			"empresa", result240.FileHeader["nome_empresa"],
			"banco", result240.FileHeader["nome_banco"],
			"data", result240.FileHeader["data_geracao"],
			"sequencia", result240.FileHeader["sequencia"],
		)
	}

	for i, batch := range result240.Batches {
		slog.Info(fmt.Sprintf("lote %d", i+1),
			"qtd_registros", batch.Trailer["qtd_registros"],
			"soma_valores", batch.Trailer["soma_valores"],
		)
		for _, seg := range batch.Segments {
			switch seg.Code {
			case "A":
				slog.Info("  segmento A",
					"favorecido", seg.Data["nome_favorecido"],
					"valor", seg.Data["valor"],
					"pagamento", seg.Data["data_pagamento"],
				)
			case "B":
				slog.Info("  segmento B",
					"cidade", seg.Data["cidade"],
					"uf", seg.Data["uf"],
				)
			}
		}
	}

	for _, pe := range result240.ParseErrors {
		slog.Warn("parse error", "linha", pe.Line, "msg", pe.Message)
	}

	// ── Exemplo 2: Parse de CNAB 400 ─────────────────────────────────────────

	slog.Info("═══ CNAB 400 — Parse de retorno de cobrança ═══")

	cnab400Data := build400Sample()
	result400, err := retorno.Parse(ctx, bytes.NewReader(cnab400Data))
	if err != nil {
		slog.Error("parse 400 failed", "err", err)
		os.Exit(1)
	}

	slog.Info("arquivo parseado",
		"linhas", result400.TotalLines,
		"detalhes", len(result400.Details),
		"erros", len(result400.ParseErrors),
	)

	if result400.FileHeader != nil {
		slog.Info("header",
			"banco", result400.FileHeader["nome_banco"],
			"data", result400.FileHeader["data_geracao"],
		)
	}

	for i, detail := range result400.Details {
		slog.Info(fmt.Sprintf("detalhe %d", i+1),
			"ocorrencia", detail["codigo_ocorrencia"],
			"valor", detail["valor_titulo"],
			"pago", detail["valor_pago"],
			"vencimento", detail["data_vencimento"],
		)
	}

	// ── Exemplo 3: Layout introspection ──────────────────────────────────────
	// Útil para gerar formulários no front-end.

	slog.Info("═══ Layout do bloco (para uso no front) ═══")

	layout := remessa.Layout()
	for recordName, rl := range layout {
		slog.Info(fmt.Sprintf("record: %s (%d campos)", recordName, len(rl.Fields)))
		for _, f := range rl.Fields {
			desc := f.Description
			if desc == "" {
				desc = "-"
			}
			slog.Info(fmt.Sprintf("  campo: %-30s cols %3d–%3d  tipo: %-8s  desc: %s",
				f.Name, f.Start, f.End, fieldTypeName(f.Type), desc))
		}
	}
}

// ── Helpers de sample ─────────────────────────────────────────────────────────

func fieldTypeName(t cnab.FieldType) string {
	switch t {
	case cnab.Alpha:
		return "alpha"
	case cnab.Numeric:
		return "numeric"
	case cnab.Decimal:
		return "decimal"
	case cnab.Date:
		return "date"
	case cnab.Boolean:
		return "boolean"
	default:
		return "unknown"
	}
}

// build240Sample cria um arquivo CNAB 240 mínimo com:
// Header de Arquivo + 1 Lote (Header + Segmento A + Segmento B + Trailer) + Trailer de Arquivo
func build240Sample() []byte {
	pad := func(s string, n int) string {
		if len(s) >= n {
			return s[:n]
		}
		return s + fmt.Sprintf("%*s", n-len(s), "")
	}
	zpad := func(n int, width int) string {
		return fmt.Sprintf("%0*d", width, n)
	}

	hoje := time.Now()
	dataStr := hoje.Format("02012006")
	horaStr := hoje.Format("150405")

	// Header de Arquivo (tipo 0)
	fileHeader := "341" + // banco_codigo
		"0000" + // lote
		"0" + // tipo_registro
		"         " + // brancos
		"2" + // tipo_inscricao
		zpad(12345678000195, 14) + // cnpj
		pad("CONVENIO123456789012", 20) + // convenio
		"12345" + // agencia
		"1" + // dv
		"000123456789" + // conta
		"0" + // dv
		"1" + // dac
		pad("EMPRESA EXEMPLO LTDA", 30) + // nome_empresa
		pad("BANCO ITAU SA", 30) + // nome_banco
		"          " + // uso_febraban
		"1" + // codigo_remessa
		dataStr + // data_geracao
		horaStr + // hora_geracao
		zpad(1, 6) + // sequencia
		"089" + // versao_layout
		zpad(0, 5) + // densidade
		fmt.Sprintf("%-29s", "") // brancos restantes

	fileHeader = pad(fileHeader, 240)

	// Header de Lote (tipo 1)
	batchHeader := "341" + "0001" + "1" +
		"C" + // operacao
		"20" + // tipo_servico
		"01" + // forma_lancamento
		"040" + // versao_lote
		fmt.Sprintf("%-224s", "")
	batchHeader = pad(batchHeader, 240)

	// Segmento A (tipo 3)
	segA := "341" + "0001" + "3" +
		zpad(1, 5) + // numero_registro
		"A" + // segmento
		"0" + // tipo_movimento
		"00" + // instrucao_movimento
		"   " + // uso_febraban (3)
		"033" + // banco_favorecido
		"12345" + // agencia_favorecido
		"1" + // dv
		"000098765432" + // conta_favorecido
		"0" + // dv
		"1" + // dac
		pad("JOAO DA SILVA SANTOS", 30) + // nome_favorecido
		pad("DOC-00001", 20) + // documento_empresa
		"01042025" + // data_pagamento (01/04/2025)
		"BRL" + // tipo_moeda
		fmt.Sprintf("%-15s", "") + // uso_febraban
		zpad(150000, 15) + // valor (R$ 1.500,00 com 2 decimais)
		fmt.Sprintf("%-76s", "") // restante
	segA = pad(segA, 240)

	// Segmento B (tipo 3)
	segB := "341" + "0001" + "3" +
		zpad(2, 5) + // numero_registro
		"B" + // segmento
		"   " + // uso_febraban
		"2" + // tipo_inscricao
		zpad(12345678000195, 14) + // cnpj
		pad("RUA DAS FLORES", 30) + // logradouro
		zpad(100, 5) + // numero
		pad("APTO 42", 15) + // complemento
		pad("CENTRO", 15) + // bairro
		pad("SAO PAULO", 20) + // cidade
		zpad(1310, 5) + // cep
		"100" + // cep_sufixo
		"SP" + // uf
		fmt.Sprintf("%-101s", "") // restante
	segB = pad(segB, 240)

	// Trailer de Lote (tipo 5)
	batchTrailer := "341" + "0001" + "5" +
		"          " + // uso_febraban
		zpad(4, 6) + // qtd_registros (header+segA+segB+trailer=4)
		zpad(150000, 18) + // soma_valores
		fmt.Sprintf("%-205s", "")
	batchTrailer = pad(batchTrailer, 240)

	// Trailer de Arquivo (tipo 9)
	fileTrailer := "341" + "9999" + "9" +
		"         " + // uso_febraban
		zpad(1, 6) + // total_lotes
		zpad(6, 6) + // total_registros
		fmt.Sprintf("%-219s", "")
	fileTrailer = pad(fileTrailer, 240)

	var buf bytes.Buffer
	for _, line := range []string{fileHeader, batchHeader, segA, segB, batchTrailer, fileTrailer} {
		buf.WriteString(line + "\n")
	}
	return buf.Bytes()
}

// build400Sample cria um arquivo CNAB 400 mínimo com Header + 2 Detalhes + Trailer.
func build400Sample() []byte {
	pad := func(s string, n int) string {
		if len(s) >= n {
			return s[:n]
		}
		return s + fmt.Sprintf("%*s", n-len(s), "")
	}
	zpad := func(n, width int) string {
		return fmt.Sprintf("%0*d", width, n)
	}

	// Header (tipo 0)
	header := "0" + // tipo_registro
		"2" + // codigo_retorno
		fmt.Sprintf("%-23s", "") + // zeros/brancos
		fmt.Sprintf("%-30s", "BANCO BRADESCO SA") + // nome_banco (cols 77-94 no layout real)
		fmt.Sprintf("%-350s", "") + // preenchimento
		zpad(1, 6) // sequencia
	header = pad(header, 400)

	// Detalhe 1 (tipo 1) — título liquidado
	detail1 := "1" + // tipo_registro
		fmt.Sprintf("%-23s", "") + // brancos
		"12345" + // agencia_cedente (cols 25-29)
		"0012345" + // conta_cedente
		fmt.Sprintf("%-34s", "") + // brancos
		"000000012345" + // nosso_numero (cols 71-82)
		fmt.Sprintf("%-26s", "") + // brancos
		"06" + // codigo_ocorrencia: liquidação
		"010425" + // data_ocorrencia DDMMAA
		"DOC-202500001 " + // numero_documento
		fmt.Sprintf("%-20s", "") + // brancos
		"010425" + // data_vencimento
		fmt.Sprintf("%-20s", "") + // brancos
		zpad(150000, 13) + // valor_titulo (R$ 1.500,00)
		fmt.Sprintf("%-6s", "") + // brancos
		zpad(0, 12) + // valor_desconto
		zpad(0, 12) + // valor_abatimento
		zpad(0, 12) + // valor_iof
		zpad(150000, 12) + // valor_pago
		zpad(150000, 12) + // valor_liquido
		zpad(0, 12) + // outras_deducoes
		zpad(0, 12) + // outros_creditos
		fmt.Sprintf("%-39s", "JOSE SILVA PAGADOR") + // nome_pagador
		fmt.Sprintf("%-38s", "") + // brancos
		"020425" + // data_credito
		fmt.Sprintf("%-93s", "") + // brancos
		zpad(1, 6) // sequencia
	detail1 = pad(detail1, 400)

	// Detalhe 2 (tipo 1) — título em aberto
	detail2 := "1" +
		fmt.Sprintf("%-23s", "") +
		"12345" +
		"0012345" +
		fmt.Sprintf("%-34s", "") +
		"000000012346" +
		fmt.Sprintf("%-26s", "") +
		"02" + // codigo_ocorrencia: entrada confirmada
		"010425" +
		"DOC-202500002 " +
		fmt.Sprintf("%-20s", "") +
		"150425" + // vencimento 15/04/2025
		fmt.Sprintf("%-20s", "") +
		zpad(250000, 13) + // R$ 2.500,00
		fmt.Sprintf("%-6s", "") +
		zpad(0, 12) + zpad(0, 12) + zpad(0, 12) +
		zpad(0, 12) + zpad(0, 12) + zpad(0, 12) + zpad(0, 12) +
		fmt.Sprintf("%-39s", "MARIA OLIVEIRA") +
		fmt.Sprintf("%-38s", "") +
		"000000" + // sem data de crédito ainda
		fmt.Sprintf("%-93s", "") +
		zpad(2, 6)
	detail2 = pad(detail2, 400)

	// Trailer (tipo 9)
	trailer := "9" +
		fmt.Sprintf("%-393s", "") +
		zpad(4, 6) // total registros (header+det1+det2+trailer)
	trailer = pad(trailer, 400)

	var buf bytes.Buffer
	for _, line := range []string{header, detail1, detail2, trailer} {
		buf.WriteString(line + "\n")
	}
	return buf.Bytes()
}
