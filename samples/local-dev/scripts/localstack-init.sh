#!/bin/bash
# scripts/localstack-init.sh
#
# Executado automaticamente pelo LocalStack ao inicializar.
# Cria os recursos AWS necessários para o sample de local-dev.

set -euo pipefail

REGION="us-east-1"
ENDPOINT="http://localhost:4566"

AWS="aws --endpoint-url=$ENDPOINT --region=$REGION"

echo "[init] Creating S3 bucket..."
$AWS s3 mb s3://local-bucket --region "$REGION" || true

echo "[init] Done. Resources created:"
$AWS s3 ls