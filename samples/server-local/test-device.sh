#!/bin/bash
# samples/server-local/test-device.sh
#
# Simula um rastreador OBD/GPS enviando mensagens para o servidor local.
# Use para testar sem um dispositivo físico.
#
# Uso:
#   chmod +x test-device.sh
#   ./test-device.sh
#
# Requer: netcat (nc)

HOST="${HOST:-localhost}"
PORT="${PORT:-5001}"

echo "Simulando rastreador OBD conectando em $HOST:$PORT"
echo ""

# ── Simular TK103 login + posição NMEA ───────────────────────────────────────
{
  echo "imei:123456789012345,tracker,0000,,F,120000.000,A,2330.0000,S,04651.0000,W,0.00,;"
  sleep 1
  echo '$GPRMC,120001.000,A,2330.1234,S,04651.5678,W,0.50,180.00,010125,,,A'
  sleep 1
  echo '$GPRMC,120031.000,A,2330.2345,S,04651.6789,W,1.20,182.00,010125,,,A'
  sleep 1
  echo '$GPRMC,120101.000,A,2330.3456,S,04651.7890,W,2.10,185.00,010125,,,A'
  sleep 2
  echo '$GPRMC,120201.000,A,2330.4567,S,04651.8901,W,0.00,185.00,010125,,,A'
} | nc "$HOST" "$PORT"

echo ""
echo "Simulação concluída."