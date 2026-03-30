# go-code-blocks

[![Go Reference](https://pkg.go.dev/badge/github.com/raywall/go-code-blocks.svg)](https://pkg.go.dev/github.com/raywall/go-code-blocks)
[![Go Report Card](https://goreportcard.com/badge/github.com/raywall/go-code-blocks)](https://goreportcard.com/report/github.com/raywall/go-code-blocks)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**go-code-blocks** é uma biblioteca Go que permite montar integrações complexas a partir de blocos independentes e reutilizáveis. Cada bloco encapsula um recurso externo — AWS DynamoDB, S3, Redis, SSM, Secrets Manager, qualquer REST API — e expõe uma API tipada e idiomática. Um bloco de decisão baseado em CEL ([go-decision-engine](https://github.com/raywall/go-decision-engine)) conecta os blocos com lógica de negócio declarativa, sem if/else espalhados pelo código. Blocos de servidor recebem chamadas de API Gateway, ALB ou diretamente via HTTP usando o mesmo Handler, independente do transporte.

```
                        ┌────────────────────────────────────────────────────┐
  API Gateway ──────────►                                                    │
  ALB         ──────────►              server.Block                          │
  HTTP :8080  ──────────►         (Handler agnóstico)                        │
                        └────────────────┬───────────────────────────────────┘
                                         │  Router / Handler / Middleware
                        ┌────────────────▼───────────────────────────────────┐
                        │                  Container                         │
                        │                                                    │
                        │    ┌──────────┐ ┌──────────┐ ┌──────────────────┐  │
                        │    │ decision │ │ dynamodb │ │     restapi      │  │
                        │    │  (CEL)   │ │  redis   │ │  Pipeline / DAG  │  │
                        │    └──────────┘ └──────────┘ └──────────────────┘  │
                        │            InitAll / ShutdownAll                   │
                        └────────────────────────────────────────────────────┘
```

## Funcionalidades

- **Blocos de entrada** — recebe chamadas de API Gateway v1/v2, ALB, servidor HTTP standalone com roteamento e middleware, ou conexões TCP raw para protocolos binários e dispositivos IoT/GPS
- **Blocos de integração** — DynamoDB, S3, Redis, SSM Parameter Store, Secrets Manager e REST APIs
- **Bloco de decisão** com regras CEL compiladas — lógica de negócio declarativa, sem if/else
- **Encadeamento de autenticação** — um bloco OAuth2 pode autorizar outro (`WithTokenProvider`)
- **Execução concorrente** — `FanOut` para requests independentes em paralelo; `Pipeline` com DAG que maximiza paralelismo em ondas
- **Cascade abort** — quando um step essencial falha, todos os dependentes são abortados sem chamadas desnecessárias
- **Retry com backoff exponencial** — política configurável por step ou por pipeline, com detecção automática de erros transientes
- **Handler agnóstico ao transporte** — o mesmo `Handler` funciona em HTTP server, API Gateway v1/v2 e ALB sem alteração
- **Options pattern** consistente em todos os blocos — sem structs de configuração expostas
- **Container com lifecycle** — `InitAll` em ordem, `ShutdownAll` em ordem reversa
- **Erros tipados** — `core.ErrItemNotFound`, `core.ErrNotInitialized`, `restapi.ErrSkipped`, etc.
- **Middleware pronto** — Logging, Recovery, CORS, RequestID prontos para usar
- **Paginação encapsulada** no S3, SSM e DynamoDB; prefixos automáticos de chave no Redis e S3

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
    "net/http"

    "github.com/raywall/go-code-blocks/blocks/dynamodb"
    "github.com/raywall/go-code-blocks/blocks/decision"
    "github.com/raywall/go-code-blocks/blocks/server"
    "github.com/raywall/go-code-blocks/core"
)

type Customer struct {
    ID   string `dynamodbav:"id"   json:"id"   decision:"-"`
    Type string `dynamodbav:"type" json:"type" decision:"customer_type"`
}

func main() {
    ctx := context.Background()

    db := dynamodb.New[Customer]("customers",
        dynamodb.WithRegion("us-east-1"),
        dynamodb.WithTable("customers-prod"),
        dynamodb.WithPartitionKey("id"),
    )

    validator := decision.New("validator",
        decision.WithRule("pj-only", `customer_type == "PJ"`,
            decision.Schema{"customer_type": decision.String}),
    )

    router := server.NewRouter()
    router.POST("/customers", func(ctx context.Context, req *server.Request) (*server.Response, error) {
        var c Customer
        if err := req.BindJSON(&c); err != nil {
            return server.Error(http.StatusBadRequest, err.Error()), nil
        }
        ok, _ := validator.EvaluateFrom(ctx, "pj-only", c)
        if !ok {
            return server.Error(http.StatusForbidden, "pessoa física não atendida"), nil
        }
        db.PutItem(ctx, c)
        return server.JSON(http.StatusCreated, c), nil
    })

    api := server.NewHTTP("api",
        server.WithPort(8080),
        server.WithRouter(router),
        server.WithMiddleware(server.Logging(), server.Recovery()),
    )

    app := core.NewContainer()
    app.MustRegister(db)
    app.MustRegister(validator)
    app.MustRegister(api)

    if err := app.InitAll(ctx); err != nil {
        log.Fatal(err)
    }
    defer app.ShutdownAll(ctx)

    api.Wait() // bloqueia até SIGINT/SIGTERM
}
```

## Blocos disponíveis

### Server (entrada de requisições)

O bloco `server` recebe chamadas de qualquer origem e as normaliza para o mesmo `*Request`, permitindo que um único Handler funcione em todos os transportes.

#### HTTP standalone

```go
import "github.com/raywall/go-code-blocks/blocks/server"

httpBlock := server.NewHTTP(name,
    server.WithPort(8080),
    server.WithRouter(router),                         // ou WithHandler(h)
    server.WithMiddleware(
        server.RequestID(),
        server.Logging(),
        server.Recovery(),
        server.CORS(server.CORSConfig{}),
    ),
    server.WithReadTimeout(15*time.Second),
    server.WithWriteTimeout(15*time.Second),
    server.WithShutdownTimeout(10*time.Second),
    server.WithTLS("cert.pem", "key.pem"),             // opcional
)
// httpBlock.Wait() bloqueia até o servidor parar
```

#### Lambda (API Gateway v1/v2 e ALB)

```go
lambdaBlock := server.NewLambda(name,
    server.WithSource(server.SourceAPIGatewayV2),  // ou V1 / ALB
    server.WithRouter(router),                      // mesmo router do HTTP
    server.WithMiddleware(server.Logging(), server.Recovery()),
)
// lambdaBlock.Start() transfere controle ao runtime Lambda
```

#### TCP raw (IoT, GPS, protocolos binários)

`TCPBlock` aceita conexões TCP brutas — ideal para rastreadores veiculares OBD/GPS, dispositivos IoT, leitores RFID e qualquer protocolo que não seja HTTP. Cada conexão recebe uma goroutine própria e é encerrada graciosamente via `Shutdown`.

```go
handler := func(ctx context.Context, conn *server.Conn) {
    defer conn.Close()
    for {
        data, err := conn.ReadMessage() // lê até BufSize bytes por vez
        if err != nil { return }

        fmt.Printf("[%s] hex: %x\n", conn.RemoteAddr(), data)
        fmt.Printf("[%s] str: %s\n", conn.RemoteAddr(), data)

        // Responde um ACK se o protocolo exigir:
        // conn.Write([]byte("LOAD"))
    }
}

tcp := server.NewTCP("obd-tracker",
    server.WithTCPPort(5001),
    server.WithConnHandler(handler),
    server.WithBufSize(2048),
    server.WithConnReadTimeout(5*time.Minute),   // timeout por leitura
    server.WithConnWriteTimeout(30*time.Second),
    server.WithTCPShutdownTimeout(10*time.Second),
)

app.MustRegister(tcp)
app.InitAll(ctx)
tcp.Wait() // bloqueia até SIGINT/SIGTERM
```

Métodos disponíveis em `*server.Conn`:

```go
conn.ReadMessage()         // lê próximo pacote (até BufSize bytes)
conn.ReadFull(p []byte)    // lê exatamente len(p) bytes
conn.Read(p) / conn.Write(p) // acesso direto ao net.Conn
conn.RemoteAddr()          // "192.168.1.10:45231"
conn.Close()
conn.Raw()                 // net.Conn subjacente para casos avançados
```

#### Router

```go
router := server.NewRouter()
router.Use(authMiddleware)               // middleware global
router.GET("/users/:id",  getUser)
router.POST("/users",     createUser)
router.PUT("/users/:id",  updateUser, adminOnly)  // middleware por rota
router.DELETE("/users/:id", deleteUser)
router.NotFound(customNotFoundHandler)
```

#### Handler e Response

```go
// Handler — assinatura única para todos os transportes
type Handler func(ctx context.Context, req *Request) (*Response, error)

// Acessores do Request
req.PathParam("id")      // parâmetro de rota :id
req.QueryParam("limit")  // query string
req.Header("Authorization")
req.BindJSON(&payload)   // deserializa o body

// Construtores de Response
server.JSON(200, data)           // application/json
server.Text(200, "ok")           // text/plain
server.Error(404, "not found")   // {"error": "not found"}
server.NoContent()               // 204
server.Redirect(301, "/new-url")
```

#### Middleware embutido

```go
server.Logging()     // slog: method, path, status, latency, request_id
server.Recovery()    // captura panic → HTTP 500, sem derrubar o processo
server.CORS(server.CORSConfig{
    AllowOrigins: []string{"https://app.example.com"},
    AllowMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
})
server.RequestID()   // propaga/gera X-Request-Id no request e na resposta
```

#### Middleware personalizado

```go
type Middleware func(next Handler) Handler

authMiddleware := func(next server.Handler) server.Handler {
    return func(ctx context.Context, req *server.Request) (*server.Response, error) {
        if req.Header("Authorization") == "" {
            return server.Error(http.StatusUnauthorized, "missing token"), nil
        }
        return next(ctx, req)
    }
}
```

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

```go
auth := restapi.New("auth",
    restapi.WithOAuth2ClientCredentials(tokenURL, clientID, clientSecret),
)
api := restapi.New("api",
    restapi.WithBaseURL("https://api.example.com"),
    restapi.WithTokenProvider(auth),  // auth busca/renova o token automaticamente
)
app.MustRegister(auth)
app.MustRegister(api)
```

#### FanOut — requests independentes em paralelo

```go
results, err := api.FanOut(ctx, map[string]restapi.Request{
    "user":    {Path: "/users/123"},
    "catalog": {Path: "/products?limit=50"},
    "rates":   {Path: "/shipping/rates"},
}, restapi.WithDefaultRetry(restapi.RetryPolicy{
    MaxAttempts: 3, Delay: 100*time.Millisecond, Backoff: 2.0,
}))

var user User
results.JSON("user", &user)
```

#### Pipeline com DAG e cascade abort

```go
//  Wave 0: [user, catalog]    → paralelos, sem dependências
//  Wave 1: [orders, payments] → paralelos, dependem de "user"
//  Wave 2: [summary]          → depende de "orders" + "catalog"
//
//  Se "user" falhar → orders, payments e summary são abortados em cascata.

steps := []restapi.PipelineStep{
    {Name: "user",    Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
        return restapi.Request{Path: "/users/123"}, nil
    }},
    {Name: "catalog", Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
        return restapi.Request{Path: "/products"}, nil
    }},
    {
        Name:      "orders",
        DependsOn: []string{"user"},
        Retry:     &restapi.RetryPolicy{MaxAttempts: 3, Delay: 200*time.Millisecond, Backoff: 2.0},
        Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
            var u User
            prev.JSON("user", &u)
            return restapi.Request{Path: "/orders?user_id=" + u.ID}, nil
        },
    },
    {
        Name:      "summary",
        DependsOn: []string{"orders", "catalog"},
        Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
            // usa dados de ambas as waves anteriores
            return restapi.Request{Method: "POST", Path: "/summary"}, nil
        },
    },
}

results, err := api.Pipeline(ctx, steps,
    restapi.WithDefaultRetry(restapi.RetryPolicy{MaxAttempts: 2, Delay: 100*time.Millisecond, Backoff: 1.5}),
    restapi.WithMaxConcurrency(5),
    restapi.WithContinueOnError(),
)

// Inspecionar resultados
sr := results.Get("orders")
sr.OK()        // true se HTTP 2xx
sr.Skipped()   // true se cascade-abortado
sr.Attempts    // quantas chamadas HTTP foram feitas (0 = skipped)
sr.Latency     // tempo total incluindo todos os retries
```

#### Retry com backoff — três níveis de precedência

```
step.Retry  >  WithDefaultRetry(policy)  >  sem retry (1 tentativa)
```

Erros que disparam retry: rede, HTTP 429/500/502/503/504. Erros 4xx (exceto 429) não são retried.

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

Tipos: `decision.String`, `decision.Int`, `decision.Float`, `decision.Bool`. Campos ignorados: `decision:"-"`.

```go
result.Passed("is-pj")  // bool
result.PassedNames()     // []string
result.Any() / result.All() / result.None()
result.Err("is-pj")      // error de avaliação
```

## Configuração AWS

Todos os blocos AWS compartilham as mesmas opções de credenciais:

```go
dynamodb.WithAWSConfig(myCfg)          // aws.Config pré-construído (maior prioridade)
dynamodb.WithRegion("sa-east-1")       // credential chain padrão (env, IAM role, ~/.aws)
dynamodb.WithProfile("staging")        // profile nomeado
dynamodb.WithEndpoint("http://localhost:8000")  // endpoint local
```

## Container

```go
app := core.NewContainer()
app.MustRegister(block)               // panic em nome duplicado
if err := app.Register(block); err != nil { ... }  // erro em nome duplicado
if err := app.InitAll(ctx); err != nil { ... }      // inicializa em ordem
defer app.ShutdownAll(ctx)            // desliga em ordem inversa

// Recuperar bloco tipado
users, err := core.Get[*dynamodb.Block[User]](app, "users")
api, err   := core.Get[*server.HTTPBlock](app, "api")
```

## Erros sentinela

```go
core.ErrItemNotFound       // item não encontrado
core.ErrNotInitialized     // operação antes do Init
core.ErrBlockNotFound      // nome não registrado
core.ErrAlreadyRegistered  // nome duplicado

restapi.ErrSkipped         // step abortado em cascade (errors.Is)
```

## Desenvolvimento local

```bash
cd samples/local-dev
docker compose up -d   # DynamoDB Local + LocalStack + Redis
go run .
```

## Samples

| Diretório | Transporte / Bloco | Demonstra |
|---|---|---|
| `samples/database/` | DynamoDB | CRUD, Query paginada, Scan |
| `samples/cache/` | Redis | String, JSON, hash, TTL, Expire |
| `samples/storage/` | S3 | Upload, download, presign, listagem |
| `samples/config/` | SSM Parameter Store | Hierarquia, SecureString, lote |
| `samples/secrets/` | Secrets Manager | JSON estruturado, rotação, recovery window |
| `samples/decision/` | Decision + DynamoDB | Roteamento PJ/PF com CEL |
| `samples/decision-pipeline/` | Decision + DynamoDB + Redis + SSM | Pipeline de contratos com CEL |
| `samples/restapi/` | REST API | GET/POST/PUT/PATCH/DELETE, todas as auths |
| `samples/restapi-chained/` | REST API + Decision + DynamoDB | OAuth2 chaining + decisão + persistência |
| `samples/restapi-pipeline/` | REST API | FanOut, Pipeline DAG, WaveTimeout |
| `samples/restapi-resilience/` | REST API | Cascade abort, retry com backoff, FanOut com rate limit |
| `samples/server-http/` | HTTP server | Servidor standalone, roteamento, middleware, DynamoDB + Redis + CEL |
| `samples/server-lambda/` | Lambda | API Gateway v2 / ALB, warm start, mesmo router do HTTP |
| `samples/server-local/` | TCP raw | Rastreador OBD/GPS: recebe posições NMEA, detecta protocolo, persiste no DynamoDB |
| `samples/full-stack/` | Todos (AWS) | DynamoDB + Redis + S3 + SSM + Secrets Manager |
| `samples/local-dev/` | Docker Compose | DynamoDB Local + LocalStack + Redis |

## Layout do projeto

```
go-code-blocks/
├── core/
│   ├── block.go          # Interface Block (Name, Init, Shutdown)
│   ├── container.go      # Register, MustRegister, InitAll, ShutdownAll
│   ├── errors.go         # Erros sentinela
│   └── get.go            # Get[B](container, name) — type assertion segura
├── internal/
│   └── awscfg/
│       └── resolver.go   # Resolução de aws.Config compartilhada
├── blocks/
│   ├── decision/         # Regras CEL via go-decision-engine
│   │   ├── block.go      # New, Init (compila regras), Shutdown
│   │   ├── evaluate.go   # Evaluate, EvaluateAll, EvaluateFrom, EvaluateAllFrom
│   │   ├── options.go    # WithRule
│   │   └── types.go      # ArgType, Schema, Result
│   ├── dynamodb/         # DynamoDB tipado com generics
│   │   ├── block.go      # New[T], Init, Shutdown
│   │   ├── crud.go       # PutItem, GetItem, DeleteItem, QueryItems, ScanItems
│   │   ├── options.go    # WithRegion, WithTable, WithPartitionKey, WithSortKey...
│   │   └── types.go      # Block[T], QueryInput, Page[T]
│   ├── s3/               # Object storage
│   │   ├── block.go      # New, Init, Shutdown
│   │   ├── operations.go # PutObject, GetObject, DeleteObject, ListObjects, PresignGetURL
│   │   ├── options.go    # WithBucket, WithKeyPrefix, WithEndpoint...
│   │   └── types.go      # Block, ObjectMetadata, ObjectInfo, PutOption
│   ├── redis/            # Cache e estruturas de dados
│   │   ├── block.go      # New, Init (PING), Shutdown (Close pool)
│   │   ├── operations.go # Set/Get, SetJSON/GetJSON, Delete, HSet/HGet/HGetAll...
│   │   └── options.go    # WithAddr, WithPassword, WithDB, WithKeyPrefix...
│   ├── parameterstore/   # SSM Parameter Store
│   │   ├── block.go      # New, Init, Shutdown
│   │   ├── operations.go # GetParameter, GetParametersByPath, PutParameter, DeleteParameter
│   │   └── options.go    # WithPathPrefix, WithDecryption...
│   ├── secretsmanager/   # AWS Secrets Manager
│   │   ├── block.go      # New, Init, Shutdown
│   │   ├── operations.go # GetSecret, CreateSecret, UpdateSecret, DeleteSecret, RotateSecret...
│   │   ├── options.go    # WithVersionStage...
│   │   └── types.go      # Block, SecretMetadata, DeleteOptions
│   ├── restapi/          # HTTP REST com OAuth2, Pipeline, Retry
│   │   ├── auth.go       # TokenProvider, oauth2ClientCredentials, basicApplier, apiKeyApplier
│   │   ├── block.go      # New, Init, Shutdown, Token() (implementa TokenProvider)
│   │   ├── concurrent.go # FanOut, Pipeline DAG, RetryPolicy, ErrSkipped, cascade abort
│   │   ├── operations.go # Get, Post, Put, Patch, Delete, Head, Do, *JSON helpers
│   │   ├── options.go    # WithBaseURL, WithOAuth2..., WithTokenProvider, WithBearerToken...
│   │   └── types.go      # Block, Request, Response
│   └── server/           # Blocos de entrada de requisições
│       ├── types.go       # Request, Response, Handler, Middleware, Source
│       ├── router.go      # Router com :param, Use, GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS
│       ├── options.go     # WithPort, WithRouter, WithHandler, WithMiddleware, WithSource...
│       ├── errors.go      # errNoHandler
│       ├── middleware.go  # Logging, Recovery, CORS, RequestID
│       ├── http.go        # HTTPBlock — servidor standalone com graceful shutdown
│       ├── lambda.go      # LambdaBlock — API Gateway v1/v2 e ALB
│       └── tcp.go         # TCPBlock — servidor TCP raw para IoT, GPS, protocolos binários
└── samples/              # Exemplos executáveis por bloco e combinados
```

## Dependências

| Módulo | Uso |
|---|---|
| `github.com/aws/aws-sdk-go-v2` | SDK AWS (DynamoDB, S3, SSM, Secrets Manager) |
| `github.com/aws/aws-lambda-go` | Runtime Lambda e tipos de evento (API Gateway, ALB) |
| `github.com/redis/go-redis/v9` | Cliente Redis |
| `github.com/raywall/go-decision-engine` | Motor de regras CEL |

## Licença

MIT — veja [LICENSE](LICENSE).