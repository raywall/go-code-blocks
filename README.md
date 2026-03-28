# go-code-blocks

[![Go Reference](https://pkg.go.dev/badge/github.com/raywall/go-code-blocks.svg)](https://pkg.go.dev/github.com/raywall/go-code-blocks)
[![Go Report Card](https://goreportcard.com/badge/github.com/raywall/go-code-blocks)](https://goreportcard.com/report/github.com/raywall/go-code-blocks)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**go-code-blocks** é uma biblioteca Go que permite montar integrações complexas a partir de blocos independentes e reutilizáveis. Cada bloco encapsula um recurso externo — AWS DynamoDB, S3, Redis, SSM, Secrets Manager, qualquer REST API — e expõe uma API tipada e idiomática. Um bloco de decisão baseado em CEL ([go-decision-engine](https://github.com/raywall/go-decision-engine)) conecta os blocos com lógica de negócio declarativa, sem if/else espalhados pelo código.

```
┌─────────────────────────────────────────────────────────────┐
│                        Container                            │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ decision │  │ dynamodb │  │  redis   │  │ restapi  │  │
│  │  (CEL)   │  │          │  │          │  │ + OAuth2 │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  │
│       │              │              │              │         │
│       └──────────────┴──────────────┴──────────────┘        │
│                    InitAll / ShutdownAll                     │
└─────────────────────────────────────────────────────────────┘
```

## Funcionalidades

- **Blocos prontos** para DynamoDB, S3, Redis, SSM Parameter Store, Secrets Manager e REST APIs
- **Bloco de decisão** com regras CEL compiladas — lógica de negócio declarativa, sem if/else
- **Encadeamento de autenticação** — um bloco OAuth2 pode autorizar outro (`WithTokenProvider`)
- **Options pattern** consistente em todos os blocos — sem structs de configuração expostas
- **Container com lifecycle** — `InitAll` em ordem, `ShutdownAll` em ordem reversa
- **Erros tipados** — `core.ErrItemNotFound`, `core.ErrNotInitialized`, etc.
- **Prefixos automáticos** de chave no Redis e S3
- **Paginação encapsulada** no S3 e SSM; paginação controlada pelo caller no DynamoDB

## Instalação

```bash
go get github.com/raywall/go-code-blocks
```

Requer **Go 1.22** ou superior.

## Início rápido

```go
package main

import (
    "context"
    "log"

    "github.com/raywall/go-code-blocks/blocks/dynamodb"
    "github.com/raywall/go-code-blocks/blocks/redis"
    "github.com/raywall/go-code-blocks/blocks/decision"
    "github.com/raywall/go-code-blocks/core"
)

type Customer struct {
    ID   string `dynamodbav:"id"   json:"id"   decision:"-"`
    Type string `dynamodbav:"type" json:"type" decision:"customer_type"`
}

func main() {
    ctx := context.Background()

    // 1. Declare os blocos
    db := dynamodb.New[Customer]("customers",
        dynamodb.WithRegion("us-east-1"),
        dynamodb.WithTable("customers-prod"),
        dynamodb.WithPartitionKey("id"),
    )

    cache := redis.New("cache",
        redis.WithAddr("localhost:6379"),
        redis.WithKeyPrefix("myapp:"),
    )

    router := decision.New("router",
        decision.WithRule("is-pj", `customer_type == "PJ"`,
            decision.Schema{"customer_type": decision.String}),
        decision.WithRule("is-pf", `customer_type == "PF"`,
            decision.Schema{"customer_type": decision.String}),
    )

    // 2. Registre no container
    app := core.NewContainer()
    app.MustRegister(db)
    app.MustRegister(cache)
    app.MustRegister(router)

    // 3. Inicializa tudo
    if err := app.InitAll(ctx); err != nil {
        log.Fatal(err)
    }
    defer app.ShutdownAll(ctx)

    // 4. Use
    c := Customer{ID: "c1", Type: "PJ"}
    db.PutItem(ctx, c)

    result, _ := router.EvaluateAllFrom(ctx, c)
    if result.Passed("is-pj") {
        fetched, _ := db.GetItem(ctx, "c1", nil)
        cache.SetJSON(ctx, "customer:c1", fetched, 0)
    }
    if result.Passed("is-pf") {
        log.Println("pessoa física não atendida neste canal")
    }
}
```

## Blocos disponíveis

### DynamoDB

```go
import "github.com/raywall/go-code-blocks/blocks/dynamodb"

block := dynamodb.New[T](name,
    dynamodb.WithRegion("us-east-1"),
    dynamodb.WithTable("my-table"),
    dynamodb.WithPartitionKey("id"),
    dynamodb.WithSortKey("sk"),                      // omitir em tabelas sem SK
    dynamodb.WithAWSConfig(cfg),                     // aws.Config pré-construído
    dynamodb.WithEndpoint("http://localhost:8000"),   // DynamoDB Local
)
```

| Método | Descrição |
|---|---|
| `PutItem(ctx, item T)` | Upsert completo |
| `GetItem(ctx, pk, sk)` | Busca por chave primária |
| `DeleteItem(ctx, pk, sk)` | Remoção por chave primária |
| `QueryItems(ctx, QueryInput)` | Query paginada com expressão CEL |
| `ScanItems(ctx, limit, lastKey)` | Scan completo paginado |

### S3

```go
import "github.com/raywall/go-code-blocks/blocks/s3"

block := s3.New(name,
    s3.WithRegion("us-east-1"),
    s3.WithBucket("my-bucket"),
    s3.WithKeyPrefix("uploads/"),
    s3.WithEndpoint("http://localhost:4566"),  // LocalStack / MinIO
)
```

| Método | Descrição |
|---|---|
| `PutObject(ctx, key, body, ...PutOption)` | Upload com Content-Type e metadata |
| `GetObject(ctx, key)` | Download → `(io.ReadCloser, ObjectMetadata, error)` |
| `DeleteObject(ctx, key)` | Remoção de objeto |
| `ListObjects(ctx, prefix)` | Listagem com paginação automática |
| `PresignGetURL(ctx, key, expiry)` | URL temporária de download |

### Redis

```go
import "github.com/raywall/go-code-blocks/blocks/redis"

block := redis.New(name,
    redis.WithAddr("localhost:6379"),
    redis.WithPassword("secret"),
    redis.WithDB(0),
    redis.WithKeyPrefix("myapp:"),
    redis.WithTLS(tlsCfg),
)
```

| Método | Descrição |
|---|---|
| `Set / Get` | String com TTL |
| `SetJSON / GetJSON` | Serialização JSON tipada |
| `Delete(ctx, keys...)` | Remoção em lote |
| `Exists / Expire` | Existência e renovação de TTL |
| `HSet / HGet / HGetAll` | Operações em hash |

### SSM Parameter Store

```go
import "github.com/raywall/go-code-blocks/blocks/parameterstore"

block := parameterstore.New(name,
    parameterstore.WithRegion("us-east-1"),
    parameterstore.WithPathPrefix("/myapp/prod"),
    parameterstore.WithDecryption(),
)
```

| Método | Descrição |
|---|---|
| `GetParameter(ctx, name)` | Parâmetro individual |
| `GetParameterDecrypted(ctx, name)` | Força descriptografia |
| `GetParametersByPath(ctx, path)` | Lote por caminho (paginação automática) |
| `PutParameter(ctx, name, value, type, overwrite)` | Criar / atualizar |
| `DeleteParameter(ctx, name)` | Remover |

### Secrets Manager

```go
import "github.com/raywall/go-code-blocks/blocks/secretsmanager"

block := secretsmanager.New(name,
    secretsmanager.WithRegion("us-east-1"),
)
```

| Método | Descrição |
|---|---|
| `GetSecret / GetSecretBinary` | Ler valor atual |
| `GetSecretJSON(ctx, name, &v)` | Deserializar segredo estruturado |
| `GetSecretVersion(ctx, name, versionID)` | Versão específica |
| `CreateSecret / CreateSecretJSON` | Criar segredo |
| `UpdateSecret / UpdateSecretJSON` | Atualizar (gera nova versão) |
| `DeleteSecret(ctx, name, DeleteOptions)` | Remover com ou sem recovery window |
| `ListSecrets(ctx)` | Listar todos (paginação automática) |
| `RotateSecret(ctx, name)` | Disparar rotação imediata |

### REST API

```go
import "github.com/raywall/go-code-blocks/blocks/restapi"

block := restapi.New(name,
    restapi.WithBaseURL("https://api.example.com/v1"),
    restapi.WithTimeout(10*time.Second),
    restapi.WithHeader("X-API-Version", "2024-01"),

    // escolha uma estratégia de autenticação:
    restapi.WithBearerToken("eyJ..."),
    restapi.WithOAuth2ClientCredentials(tokenURL, clientID, clientSecret, scopes...),
    restapi.WithBasicAuth("user", "pass"),
    restapi.WithAPIKeyHeader("X-API-Key", "abc123"),
    restapi.WithAPIKeyQuery("api_key", "abc123"),
)
```

| Método | Descrição |
|---|---|
| `Get(ctx, path, query)` | GET com query params |
| `Post / Put / Patch(ctx, path, body)` | Verbo com body |
| `Delete / Head(ctx, path)` | Sem body |
| `GetJSON / PostJSON / PutJSON / PatchJSON` | Helpers com deserialização automática |
| `Do(ctx, Request)` | Request totalmente customizado |

#### Encadeamento de token (OAuth2 → Bearer)

Um bloco configurado com `WithOAuth2ClientCredentials` implementa `TokenProvider`. Passe-o diretamente para outro bloco via `WithTokenProvider` — o token é buscado, cacheado e renovado automaticamente, sem que o segundo bloco precise conhecer as credenciais:

```go
auth := restapi.New("auth",
    restapi.WithOAuth2ClientCredentials(tokenURL, clientID, clientSecret),
)

api := restapi.New("api",
    restapi.WithBaseURL("https://api.example.com"),
    restapi.WithTokenProvider(auth),  // ← auth busca/renova o token
)

app.MustRegister(auth)
app.MustRegister(api)  // auth deve ser registrado primeiro
```

### Bloco de decisão (CEL)

```go
import "github.com/raywall/go-code-blocks/blocks/decision"

router := decision.New(name,
    decision.WithRule("is-pj", `customer_type == "PJ"`,
        decision.Schema{"customer_type": decision.String}),
    decision.WithRule("high-value", `amount > 10000.0`,
        decision.Schema{"amount": decision.Float}),
)
```

| Método | Input | Escopo |
|---|---|---|
| `Evaluate(ctx, ruleName, map)` | `map[string]any` | Uma regra |
| `EvaluateAll(ctx, map)` | `map[string]any` | Todas as regras, concorrente |
| `EvaluateFrom(ctx, ruleName, struct)` | struct com `decision:` tags | Uma regra |
| `EvaluateAllFrom(ctx, struct)` | struct com `decision:` tags | Todas as regras, concorrente |

Tipos suportados nas expressões CEL: `decision.String`, `decision.Int`, `decision.Float`, `decision.Bool`.

Campos ignorados pela engine recebem a tag `decision:"-"`.

O `*Result` retornado expõe:

```go
result.Passed("is-pj")   // bool
result.Failed("is-pf")   // bool
result.PassedNames()      // []string
result.Any()              // pelo menos uma regra passou
result.All()              // todas as regras passaram
result.None()             // nenhuma regra passou
result.Err("is-pj")       // error de avaliação da regra
```

## Configuração AWS

Todos os blocos AWS aceitam as mesmas opções de credenciais, com a seguinte ordem de prioridade:

```go
// Maior prioridade: aws.Config pré-construído (multi-conta, testes)
dynamodb.WithAWSConfig(myCfg)

// Região + credential chain padrão (variáveis de ambiente, IAM role, ~/.aws)
dynamodb.WithRegion("sa-east-1")

// Profile nomeado do ~/.aws/credentials
dynamodb.WithProfile("staging")

// Endpoint local (DynamoDB Local, LocalStack, MinIO)
dynamodb.WithEndpoint("http://localhost:8000")
```

## Container

```go
app := core.NewContainer()

// Register retorna erro em nome duplicado
if err := app.Register(block); err != nil { ... }

// MustRegister entra em panic — ideal para wiring no main
app.MustRegister(block)

// Inicializa na ordem de registro
if err := app.InitAll(ctx); err != nil { ... }

// Desliga na ordem inversa (defer)
defer app.ShutdownAll(ctx)

// Recuperar bloco tipado sem type assertion manual
users, err := core.Get[*dynamodb.Block[User]](app, "users")
```

## Erros sentinela

```go
import "github.com/raywall/go-code-blocks/core"

core.ErrItemNotFound      // item não encontrado no banco / cache
core.ErrNotInitialized    // operação chamada antes de Init
core.ErrBlockNotFound     // nome não registrado no container
core.ErrAlreadyRegistered // nome duplicado no container
```

## Desenvolvimento local

O sample `samples/local-dev/` inclui um `docker-compose.yml` com DynamoDB Local, LocalStack (S3 + SSM + Secrets Manager) e Redis:

```bash
cd samples/local-dev
docker compose up -d
go run .
```

## Samples

| Diretório | Demonstra |
|---|---|
| `samples/database/` | DynamoDB — CRUD, Query e Scan |
| `samples/cache/` | Redis — strings, JSON, hash, TTL |
| `samples/storage/` | S3 — upload, download, presign, listagem |
| `samples/config/` | SSM Parameter Store — hierarquia, SecureString |
| `samples/secrets/` | Secrets Manager — JSON estruturado, rotação |
| `samples/full-stack/` | DynamoDB + Redis + S3 + SSM + Secrets Manager |
| `samples/decision/` | Bloco de decisão + DynamoDB (roteamento PJ/PF) |
| `samples/decision-pipeline/` | CEL + DynamoDB + Redis + SSM (contratos) |
| `samples/restapi/` | REST API — todos os verbos e estratégias de auth |
| `samples/restapi-chained/` | OAuth2 chaining + decision + DynamoDB |
| `samples/local-dev/` | Ambiente local com docker-compose |

## Layout do projeto

```
go-code-blocks/
├── core/                        # Block interface, Container, erros, Get[T]
├── internal/
│   └── awscfg/                  # Resolução de aws.Config (compartilhada)
├── blocks/
│   ├── decision/                # Regras CEL via go-decision-engine
│   ├── dynamodb/                # DynamoDB tipado com generics
│   ├── s3/                      # Object storage
│   ├── redis/                   # Cache e estruturas de dados
│   ├── parameterstore/          # SSM Parameter Store
│   ├── secretsmanager/          # AWS Secrets Manager
│   └── restapi/                 # HTTP REST com OAuth2, Basic, API Key
└── samples/                     # Exemplos executáveis por bloco e combinados
```

## Dependências

| Módulo | Uso |
|---|---|
| `github.com/aws/aws-sdk-go-v2` | SDK AWS (DynamoDB, S3, SSM, Secrets Manager) |
| `github.com/redis/go-redis/v9` | Cliente Redis |
| `github.com/raywall/go-decision-engine` | Motor de regras CEL |

## Licença

MIT — veja [LICENSE](LICENSE).