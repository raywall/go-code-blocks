// samples/storage/main.go
//
// Demonstra o uso do bloco S3 de forma isolada.
//
// Operações cobertas:
//   - PutObject     — upload com Content-Type e metadata customizados
//   - GetObject     — download com leitura do body e metadata
//   - DeleteObject  — remoção de um objeto
//   - ListObjects   — listagem paginada por prefixo (paginação automática)
//   - PresignGetURL — geração de URL temporária de download
//
// Variáveis de ambiente:
//
//	AWS_REGION      região AWS (default: us-east-1)
//	S3_BUCKET       bucket alvo (default: my-assets-dev)
//	S3_KEY_PREFIX   prefixo de chave (default: samples/)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/blocks/s3"
	"github.com/raywall/go-code-blocks/core"
)

func main() {
	ctx := context.Background()

	assets := s3.New("assets",
		s3.WithRegion(envOr("AWS_REGION", "us-east-1")),
		s3.WithBucket(envOr("S3_BUCKET", "my-assets-dev")),
		s3.WithKeyPrefix(envOr("S3_KEY_PREFIX", "samples/")),
		// s3.WithEndpoint("http://localhost:4566"), // LocalStack
		// s3.WithPathStyle(),                       // MinIO / LocalStack
	)

	app := core.NewContainer()
	app.MustRegister(assets)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. Upload de texto simples ────────────────────────────────────────────
	slog.Info("── PutObject (text) ──")
	readme := []byte("# go-code-blocks\nArquivo de exemplo.\n")
	if err := assets.PutObject(
		ctx,
		"docs/readme.txt",
		bytes.NewReader(readme),
		s3.WithContentType("text/plain; charset=utf-8"),
	); err != nil {
		slog.Error("PutObject text", "err", err)
	} else {
		slog.Info("text uploaded", "key", "docs/readme.txt")
	}

	// ── 2. Upload de JSON com metadata ────────────────────────────────────────
	slog.Info("── PutObject (json + metadata) ──")
	type Report struct {
		GeneratedAt string         `json:"generated_at"`
		RecordCount int            `json:"record_count"`
		Totals      map[string]any `json:"totals"`
	}
	report := Report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		RecordCount: 1024,
		Totals:      map[string]any{"revenue": 98_750.50, "orders": 312},
	}
	reportBytes, _ := json.Marshal(report)
	if err := assets.PutObject(
		ctx,
		"reports/2024-q4.json",
		bytes.NewReader(reportBytes),
		s3.WithContentType("application/json"),
		s3.WithMetadata(map[string]string{
			"author":  "batch-job",
			"version": "1",
		}),
	); err != nil {
		slog.Error("PutObject json", "err", err)
	} else {
		slog.Info("report uploaded", "key", "reports/2024-q4.json", "bytes", len(reportBytes))
	}

	// ── 3. Upload de imagem (bytes fixos como placeholder) ────────────────────
	slog.Info("── PutObject (binary) ──")
	// Simulando bytes de uma imagem PNG (header mágico real)
	fakePNG := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if err := assets.PutObject(
		ctx,
		"images/avatar.png",
		bytes.NewReader(fakePNG),
		s3.WithContentType("image/png"),
		s3.WithMetadata(map[string]string{"owner": "usr_001"}),
	); err != nil {
		slog.Error("PutObject binary", "err", err)
	} else {
		slog.Info("image uploaded", "key", "images/avatar.png")
	}

	// ── 4. Download e leitura do body ─────────────────────────────────────────
	slog.Info("── GetObject ──")
	body, meta, err := assets.GetObject(ctx, "reports/2024-q4.json")
	if err != nil {
		slog.Error("GetObject", "err", err)
	} else {
		defer body.Close()
		content, _ := io.ReadAll(body)
		slog.Info("object downloaded",
			"content_type", meta.ContentType,
			"size_bytes", meta.ContentLength,
			"etag", meta.ETag,
			"last_modified", meta.LastModified.Format(time.RFC3339),
		)
		// Redecodificar para confirmar
		var downloaded Report
		if err := json.Unmarshal(content, &downloaded); err == nil {
			slog.Info("report content",
				"record_count", downloaded.RecordCount,
				"generated_at", downloaded.GeneratedAt,
			)
		}
	}

	// ── 5. URL pré-assinada com validade de 15 minutos ────────────────────────
	slog.Info("── PresignGetURL ──")
	url, err := assets.PresignGetURL(ctx, "images/avatar.png", 15*time.Minute)
	if err != nil {
		slog.Error("PresignGetURL", "err", err)
	} else {
		slog.Info("pre-signed URL generated", "expires_in", "15m", "url", url)
	}

	// ── 6. Listagem por prefixo ───────────────────────────────────────────────
	slog.Info("── ListObjects ──")
	// Lista tudo no prefixo raiz do bloco (samples/)
	allObjects, err := assets.ListObjects(ctx, "")
	if err != nil {
		slog.Error("ListObjects (all)", "err", err)
	} else {
		slog.Info("all objects", "count", len(allObjects))
		for _, obj := range allObjects {
			slog.Info("  object",
				"key", obj.Key,
				"size", obj.Size,
				"last_modified", obj.LastModified.Format(time.RFC3339),
			)
		}
	}

	// Lista apenas o subdiretório de reports
	reportObjects, err := assets.ListObjects(ctx, "reports/")
	if err != nil {
		slog.Error("ListObjects (reports/)", "err", err)
	} else {
		slog.Info("reports/", "count", len(reportObjects))
	}

	// ── 7. Deleção de objetos ─────────────────────────────────────────────────
	slog.Info("── DeleteObject ──")
	for _, key := range []string{"docs/readme.txt", "reports/2024-q4.json", "images/avatar.png"} {
		if err := assets.DeleteObject(ctx, key); err != nil {
			slog.Error("DeleteObject", "key", key, "err", err)
		} else {
			slog.Info("deleted", "key", key)
		}
	}

	// Confirmar que a listagem está vazia
	remaining, _ := assets.ListObjects(ctx, "")
	slog.Info("objects after cleanup", "count", len(remaining))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
