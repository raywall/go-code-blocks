# go-code-blocks

[![Go Reference](https://pkg.go.dev/badge/github.com/raywall/go-code-blocks.svg)](https://pkg.go.dev/github.com/raywall/go-code-blocks)
[![Go Report Card](https://goreportcard.com/badge/github.com/raywall/go-code-blocks)](https://goreportcard.com/report/github.com/raywall/go-code-blocks)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**go-code-blocks** é uma biblioteca Go que permite montar integrações complexas a partir de blocos independentes e reutilizáveis. Cada bloco encapsula um recurso externo — AWS DynamoDB, S3, Redis, SSM, Secrets Manager, qualquer REST API — e expõe uma API tipada e idiomática. Um bloco de decisão baseado em CEL ([go-decision-engine](https://github.com/raywall/go-decision-engine)) conecta os blocos com lógica de negócio declarativa, sem if/else espalhados pelo código.

```
┌──────────────────────────────────────────────────────────────────┐
│                           Container                              │
│                                                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────────┐    │
│  │ decision │  │ dynamodb │  │  redis   │  │    restapi     │    │
│  │  (CEL)   │  │          │  │          │  │ Pipeline / DAG │    │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └───────┬────────┘    │
│       │             │             │                │             │
│       └─────────────┴─────────────┴────────────────┘             │
│                      InitAll / ShutdownAll                       │
└──────────────────────────────────────────────────────────────────┘
```

## Funcionalidades

- **Blocos prontos** para DynamoDB, S3, Redis, SSM Parameter Store, Secrets Manager e REST APIs
- **Bloco de decisão** com regras CEL compiladas — lógica de negócio declarativa, sem if/else
- **Encadeamento de autenticação** — um bloco OAuth2 pode autorizar outro (`WithTokenProvider`)
- **Execução concorrente** — `FanOut` para requests independentes em paralelo; `Pipeline` com grafo de dependências (DAG) que maximiza o paralelismo em ondas
- **Cascade abort** — quando um step essencial falha, todos os dependentes são abortados automaticamente sem fazer chamadas desnecessárias
- **Retry com backoff** — política de retry configurável por step ou por pipeline, com detecção automática de erros transientes
- **Options pattern** consistente em todos os blocos — sem structs de configuração expostas
- **Container com lifecycle** — `InitAll` em ordem, `ShutdownAll` em ordem reversa
- **Erros tipados** — `core.ErrItemNotFound`, `core.ErrNotInitialized`, `restapi.ErrSkipped`, etc.
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
| `QueryItems(ctx, QueryInput)` | Query paginada com expressão |
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
| `FanOut(ctx, requests, ...opts)` | N requests independentes em paralelo |
| `Pipeline(ctx, steps, ...opts)` | DAG de steps com dependências e retry |

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

#### Execução concorrente com FanOut

`FanOut` executa todos os requests de forma totalmente paralela numa única chamada. Ideal quando as requisições são independentes entre si:

```go
results, err := api.FanOut(ctx, map[string]restapi.Request{
    "user":    {Path: "/users/123"},
    "catalog": {Path: "/products?limit=50"},
    "rates":   {Path: "/shipping/rates"},
}, restapi.WithDefaultRetry(restapi.RetryPolicy{
    MaxAttempts: 3,
    Delay:       100 * time.Millisecond,
    Backoff:     2.0,
}))

var user User
results.JSON("user", &user)
```

#### Pipeline com DAG e cascade abort

`Pipeline` organiza os steps em ondas de execução usando ordenação topológica. Dentro de cada onda, todos os steps independentes correm em paralelo. Steps de ondas posteriores recebem os resultados das ondas anteriores para construir seus requests dinamicamente.

Quando um step falha, todos os steps que dependem dele — direta ou transitivamente — são abortados automaticamente (`ErrSkipped`), sem fazer nenhuma chamada HTTP desnecessária:

```go
//  Wave 0: [user, catalog]  ──────────── paralelos, sem dependências
//  Wave 1: [orders, payments] ────────── paralelos, dependem de "user"
//  Wave 2: [summary] ─────────────────── depende de "orders" + "catalog"
//
//  Se "user" falhar → orders, payments e summary são abortados em cascata.

steps := []restapi.PipelineStep{
    {
        Name:  "user",
        Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
            return restapi.Request{Path: "/users/123"}, nil
        },
    },
    {
        Name:  "catalog",
        Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
            return restapi.Request{Path: "/products?limit=10"}, nil
        },
    },
    {
        Name:      "orders",
        DependsOn: []string{"user"},         // abortado se "user" falhar
        Retry: &restapi.RetryPolicy{         // retry específico por step
            MaxAttempts: 3,
            Delay:       200 * time.Millisecond,
            Backoff:     2.0,
        },
        Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
            var u User
            if err := prev.JSON("user", &u); err != nil {
                return restapi.Request{}, err
            }
            return restapi.Request{Path: "/orders?user_id=" + u.ID}, nil
        },
    },
    {
        Name:      "summary",
        DependsOn: []string{"orders", "catalog"},
        Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
            // usa dados de ambas as waves anteriores
            var orders []Order
            var catalog []Product
            prev.JSON("orders", &orders)
            prev.JSON("catalog", &catalog)
            return restapi.Request{Method: "POST", Path: "/summary",
                Body: map[string]any{"orders": len(orders), "products": len(catalog)}}, nil
        },
    },
}

results, err := api.Pipeline(ctx, steps,
    restapi.WithDefaultRetry(restapi.RetryPolicy{   // retry padrão para todos os steps
        MaxAttempts: 2,
        Delay:       100 * time.Millisecond,
        Backoff:     1.5,
    }),
    restapi.WithMaxConcurrency(5),   // máximo de goroutines por onda
    restapi.WithContinueOnError(),   // coleta todos os resultados mesmo com falhas
)
```

#### Retry com backoff exponencial

A política de retry pode ser definida em três níveis, em ordem de precedência:

```
step.Retry  >  WithDefaultRetry(policy)  >  sem retry (1 tentativa)
```

```go
// Apenas para um step específico
restapi.PipelineStep{
    Name:  "orders",
    Retry: &restapi.RetryPolicy{
        MaxAttempts: 4,                      // 1 original + 3 retries
        Delay:       200 * time.Millisecond, // espera antes do 2º attempt
        Backoff:     2.0,                    // dobra a espera a cada retry: 200ms → 400ms → 800ms
    },
}

// Como padrão para todo o pipeline ou FanOut
restapi.WithDefaultRetry(restapi.RetryPolicy{
    MaxAttempts: 3,
    Delay:       100 * time.Millisecond,
    Backoff:     1.5,  // 100ms → 150ms → done
})
```

Erros que disparam retry: erros de rede, HTTP 429, 500, 502, 503, 504. Erros de cliente (4xx exceto 429) **não** são retried — retry não resolve um erro do chamador.

#### Inspecionando resultados do Pipeline

```go
// Por step
sr := results.Get("orders")
sr.OK()           // true se HTTP 2xx sem erro
sr.Skipped()      // true se abortado em cascade (errors.Is(sr.Err, restapi.ErrSkipped))
sr.Attempts       // número de chamadas HTTP feitas (0 = skipped, 1 = sem retry, 2+ = houve retry)
sr.Latency        // tempo total incluindo todos os retries

// Deserializar body diretamente
var orders []Order
results.JSON("orders", &orders)

// Listar todos os steps executados
results.Names()   // []string
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
result.Passed("is-pj")    // bool
result.Failed("is-pf")    // bool
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

// restapi — verificáveis via errors.Is
restapi.ErrSkipped        // step abortado em cascade por falha de dependência
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
| `samples/restapi-pipeline/` | FanOut e Pipeline DAG — execução concorrente em ondas |
| `samples/restapi-resilience/` | Cascade abort + retry com backoff em cenários reais |
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
│   └── restapi/                 # HTTP REST com OAuth2, Pipeline, Retry
│       ├── auth.go              # TokenProvider, OAuth2, Basic, API Key
│       ├── block.go             # Lifecycle + Token() para chaining
│       ├── concurrent.go        # FanOut, Pipeline DAG, RetryPolicy, cascade abort
│       ├── operations.go        # Get, Post, Put, Patch, Delete, Do
│       ├── options.go           # WithBaseURL, WithOAuth2..., WithTokenProvider
│       └── types.go             # Block, Request, Response
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