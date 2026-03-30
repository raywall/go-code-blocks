// samples/server-local/main.go
//
// Servidor TCP local que recebe dados de rastreadores veiculares OBD/GPS
// e os exibe no terminal e persiste no DynamoDB.
//
// Compatível com rastreadores que usam protocolos ASCII como TK103, GT06,
// Coban e similares — qualquer dispositivo que abre uma conexão TCP e
// envia pacotes de posicionamento em texto ou hex.
//
// Para rodar:
//
//	go run .
//
// Aponte o rastreador para o IP público desta máquina na porta 5001.
// Os dados recebidos são exibidos em tempo real e opcionalmente salvos
// no DynamoDB (configure DYNAMO_ENDPOINT para DynamoDB Local ou use
// AWS_REGION + TRACKING_TABLE para a AWS real).
//
// Variáveis de ambiente:
//
//	PORT              porta TCP            (default: 5001)
//	DYNAMO_ENDPOINT   endpoint DynamoDB    (default: http://localhost:8000)
//	AWS_REGION        região AWS           (default: us-east-1)
//	TRACKING_TABLE    tabela de registros  (default: tracking-local)
//	STORE_MESSAGES    "true" para persistir no DynamoDB (default: false)
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/blocks/server"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelo de rastreamento ────────────────────────────────────────────────────

// TrackingRecord representa uma mensagem recebida de um rastreador.
type TrackingRecord struct {
	// ID é a chave primária: endereço remoto + timestamp em ms.
	ID         string `dynamodbav:"id"          json:"id"`
	DeviceAddr string `dynamodbav:"device_addr" json:"device_addr"`
	RawHex     string `dynamodbav:"raw_hex"     json:"raw_hex"`
	RawString  string `dynamodbav:"raw_string"  json:"raw_string"`
	Protocol   string `dynamodbav:"protocol"    json:"protocol"`
	ParsedData any    `dynamodbav:"parsed_data" json:"parsed_data,omitempty"`
	ReceivedAt string `dynamodbav:"received_at" json:"received_at"`
}

const (
	blockTracking = "tracking-db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		tcpPort        = envOrInt("PORT", 5001)
		dynamoEndpoint = envOr("DYNAMO_ENDPOINT", "http://localhost:8000")
		awsRegion      = envOr("AWS_REGION", "us-east-1")
		trackingTable  = envOr("TRACKING_TABLE", "tracking-local")
		storeMessages  = envOr("STORE_MESSAGES", "false") == "true"
	)

	// ── aws.Config com credenciais para DynamoDB Local ou AWS real ────────────
	var trackingDB *dynamodb.Block[TrackingRecord]

	if dynamoEndpoint != "" && dynamoEndpoint != "disabled" {
		// Credenciais fictícias para DynamoDB Local; ignoradas se DYNAMO_ENDPOINT
		// não estiver configurado e o SDK usar a credential chain padrão (AWS real).
		fakeCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(awsRegion),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider("test", "test", "test"),
			),
		)
		if err != nil {
			slog.Warn("aws config failed — DynamoDB desabilitado", "err", err)
		} else {
			trackingDB = dynamodb.New[TrackingRecord](blockTracking,
				dynamodb.WithAWSConfig(fakeCfg),
				dynamodb.WithTable(trackingTable),
				dynamodb.WithPartitionKey("id"),
				dynamodb.WithEndpoint(dynamoEndpoint),
			)
		}
	}

	// ── Handler TCP ───────────────────────────────────────────────────────────
	// Cada dispositivo que conecta recebe sua própria goroutine.
	// O handler lê mensagens em loop até o cliente desconectar.
	connHandler := func(ctx context.Context, conn *server.Conn) {
		defer conn.Close()

		addr := conn.RemoteAddr()
		slog.Info("dispositivo conectado", "addr", addr)

		for {
			// Lê o próximo pacote do rastreador
			data, err := conn.ReadMessage()
			if err != nil {
				slog.Info("dispositivo desconectado", "addr", addr, "err", err)
				return
			}

			now := time.Now().UTC()
			rawHex := hex.EncodeToString(data)
			rawStr := strings.TrimSpace(string(data))
			proto := detectProtocol(data)

			// Exibe no terminal — igual ao código original, mas estruturado
			slog.Info("mensagem recebida",
				"addr", addr,
				"protocol", proto,
				"hex", rawHex,
				"string", rawStr,
				"bytes", len(data),
			)

			// Parsed: tenta extrair campos do protocolo detectado
			parsed := parseMessage(proto, data)
			if parsed != nil {
				slog.Info("  → dados extraídos", "parsed", parsed)
			}

			// Persiste no DynamoDB se habilitado
			if storeMessages && trackingDB != nil {
				record := TrackingRecord{
					ID:         fmt.Sprintf("%s_%d", sanitizeAddr(addr), now.UnixMilli()),
					DeviceAddr: addr,
					RawHex:     rawHex,
					RawString:  rawStr,
					Protocol:   proto,
					ParsedData: parsed,
					ReceivedAt: now.Format(time.RFC3339Nano),
				}
				if err := trackingDB.PutItem(ctx, record); err != nil {
					slog.Warn("falha ao salvar no DynamoDB", "err", err)
				} else {
					slog.Debug("registro salvo", "id", record.ID)
				}
			}

			// Alguns rastreadores esperam um ACK após cada pacote.
			// Descomente e ajuste conforme o protocolo do seu dispositivo:
			// conn.Write([]byte("LOAD"))  // ACK TK103
			// conn.Write([]byte{0x01})    // ACK binário genérico
		}
	}

	// ── Bloco TCP ─────────────────────────────────────────────────────────────
	tcpBlock := server.NewTCP("obd-tracker",
		server.WithTCPPort(tcpPort),
		server.WithConnHandler(connHandler),
		server.WithBufSize(2048),
		// Timeout de leitura generoso para dispositivos que enviam posição a cada 30 s
		server.WithConnReadTimeout(5*time.Minute),
	)

	// ── Container ─────────────────────────────────────────────────────────────
	app := core.NewContainer()

	if trackingDB != nil {
		app.MustRegister(trackingDB)
	}
	app.MustRegister(tcpBlock)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("falha na inicialização", "err", err)
		os.Exit(1)
	}

	slog.Info("servidor OBD/GPS pronto",
		"port", tcpPort,
		"store", storeMessages,
		"dynamo", dynamoEndpoint,
	)
	slog.Info("aponte o rastreador para este IP na porta indicada")

	// Aguarda sinal de encerramento (CTRL+C / SIGTERM)
	<-ctx.Done()
	slog.Info("encerrando servidor...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.ShutdownAll(shutCtx); err != nil {
		slog.Error("erro no shutdown", "err", err)
	}
	slog.Info("servidor encerrado")
}

// ── Detecção de protocolo ─────────────────────────────────────────────────────

// detectProtocol tenta identificar o protocolo do rastreador pelo conteúdo
// do pacote. Os protocolos ASCII mais comuns têm prefixos característicos.
func detectProtocol(data []byte) string {
	if len(data) == 0 {
		return "unknown"
	}
	s := string(data)
	switch {
	case strings.HasPrefix(s, "##"):
		return "TK103-login"
	case strings.HasPrefix(s, "imei:"):
		return "TK103-imei"
	case strings.Contains(s, "$GPRMC") || strings.Contains(s, "$GNRMC"):
		return "NMEA-GPRMC"
	case strings.Contains(s, "$GPGGA") || strings.Contains(s, "$GNGGA"):
		return "NMEA-GPGGA"
	case strings.HasPrefix(s, "+RESP:"):
		return "Queclink"
	case strings.HasPrefix(s, "$$"):
		return "GT06-like"
	case len(data) > 2 && data[0] == 0x78 && data[1] == 0x78:
		return "GT06-binary"
	case strings.Contains(s, "GPRMC") || strings.Contains(s, "GPGGA"):
		return "NMEA-generic"
	default:
		return "unknown"
	}
}

// ── Parsing básico de mensagens ───────────────────────────────────────────────

// parseMessage extrai campos relevantes de protocolos conhecidos.
// Retorna nil para protocolos não reconhecidos ou mensagens mal-formadas.
func parseMessage(proto string, data []byte) map[string]any {
	s := strings.TrimSpace(string(data))

	switch proto {
	case "NMEA-GPRMC", "NMEA-generic":
		return parseNMEA(s)

	case "TK103-imei":
		// Formato: "imei:123456789012345,tracker,..."
		parts := strings.SplitN(s, ",", 3)
		if len(parts) >= 2 {
			return map[string]any{
				"imei": strings.TrimPrefix(parts[0], "imei:"),
				"type": parts[1],
			}
		}

	case "TK103-login":
		return map[string]any{"type": "login", "raw": s}

	case "Queclink":
		// +RESP:GTFRI,... → campos separados por vírgula
		fields := strings.Split(s, ",")
		if len(fields) > 2 {
			return map[string]any{
				"type":   fields[0],
				"fields": fields[1:],
			}
		}
	}
	return nil
}

// parseNMEA extrai latitude, longitude, data e hora de uma sentença $GPRMC.
// Formato: $GPRMC,hhmmss,A,llll.ll,a,yyyyy.yy,a,x.x,x.x,ddmmyy,...
func parseNMEA(s string) map[string]any {
	var sentence string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$GPRMC") || strings.HasPrefix(line, "$GNRMC") {
			sentence = line
			break
		}
	}
	if sentence == "" {
		return nil
	}

	parts := strings.Split(sentence, ",")
	if len(parts) < 7 {
		return nil
	}

	result := map[string]any{
		"sentence": parts[0],
		"time_utc": parts[1],
		"status":   parts[2], // A=active, V=void
	}

	// Latitude: ddmm.mmmmm + N/S
	if parts[3] != "" && parts[4] != "" {
		result["lat_raw"] = parts[3]
		result["lat_dir"] = parts[4]
		if lat, ok := nmeaToDecimal(parts[3], parts[4]); ok {
			result["latitude"] = lat
		}
	}

	// Longitude: dddmm.mmmmm + E/W
	if parts[5] != "" && parts[6] != "" {
		result["lon_raw"] = parts[5]
		result["lon_dir"] = parts[6]
		if lon, ok := nmeaToDecimal(parts[5], parts[6]); ok {
			result["longitude"] = lon
		}
	}

	if len(parts) > 7 && parts[7] != "" {
		result["speed_knots"] = parts[7]
	}
	if len(parts) > 9 && parts[9] != "" {
		result["date"] = parts[9]
	}

	return result
}

// nmeaToDecimal converte o formato NMEA ddmm.mmmmm para graus decimais.
func nmeaToDecimal(raw, dir string) (float64, bool) {
	if len(raw) < 4 {
		return 0, false
	}

	// Encontra o ponto decimal para determinar quantos dígitos são graus
	dotIdx := strings.Index(raw, ".")
	if dotIdx < 2 {
		return 0, false
	}

	var degrees, minutes float64
	fmt.Sscanf(raw[:dotIdx-2], "%f", &degrees)
	fmt.Sscanf(raw[dotIdx-2:], "%f", &minutes)

	decimal := degrees + minutes/60.0
	if dir == "S" || dir == "W" {
		decimal = -decimal
	}
	return decimal, true
}

// ── helpers ───────────────────────────────────────────────────────────────────

// sanitizeAddr transforma "192.168.1.1:45231" em "192_168_1_1_45231"
// para uso seguro como parte de uma chave DynamoDB.
func sanitizeAddr(addr string) string {
	r := strings.NewReplacer(".", "_", ":", "_", "[", "", "]", "")
	return r.Replace(addr)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	fmt.Sscanf(v, "%d", &n)
	if n == 0 {
		return fallback
	}
	return n
}
