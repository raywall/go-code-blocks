# go-code-blocks

[![Go Reference](https://pkg.go.dev/badge/github.com/raywall/go-code-blocks.svg)](https://pkg.go.dev/github.com/raywall/go-code-blocks)
[![Go Report Card](https://goreportcard.com/badge/github.com/raywall/go-code-blocks)](https://goreportcard.com/report/github.com/raywall/go-code-blocks)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**go-code-blocks** é uma biblioteca Go que permite montar micro e nano serviços a partir de blocos independentes e reutilizáveis. Cada bloco encapsula um recurso externo — AWS DynamoDB, RDS, S3, Redis, SSM, Secrets Manager, REST API — e expõe uma API tipada e idiomática. O bloco `flow` compõe esses blocos em pipelines declarativos de processamento de requisições, do recebimento até a resposta.

```
     ┌──────────────────────────────────────────────────────────────────┐
     │                         Container                                │
     │                                                                  │
     │  ┌─────────────┐  ┌──────────┐  ┌──────────┐  ┌─────────────┐    │
     │  │   server    │  │  flow    │  │ decision │  │  restapi    │    │
     │  │ HTTP/Lambda │→ │ pipeline │→ │  (CEL)   │  │  DynamoDB   │    │
     │  │    /TCP     │  │ Validate │  └──────────┘  │  RDS/Redis  │    │
     │  └─────────────┘  │ Enrich   │                │  S3/SSM/... │    │
     │                   │ Decide   │←───────────────┘             │    │
     │                   │ Transform│  transform / output          │    │
     │                   │ Respond  │                              │    │
     │                   └──────────┘                              │    │
     │                      InitAll / ShutdownAll                  │    │
     └──────────────────────────────────────────────────────────────────┘
```

## Funcionalidades

- **Blocos de entrada** — recebe chamadas de API Gateway v1/v2, ALB, servidor HTTP standalone, ou conexões TCP raw
- **Flow pipeline** — compõe steps declarativos (Validate → Enrich → Decide → Transform → Respond) num `server.Handler` plug-and-play
- **Transformação de dados** — `transform.Builder` combina campos de múltiplas fontes do state sem boilerplate
- **Output declarativo** — `output` constrói respostas HTTP e payloads REST a partir do state
- **Blocos de integração** — DynamoDB, RDS (PostgreSQL/MySQL), Redis, S3, SSM Parameter Store, Secrets Manager, REST API
- **Decisão com CEL** — regras de negócio compiladas, reutilizáveis em qualquer step do flow
- **Encadeamento OAuth2** — um bloco `restapi` pode autorizar outro via `WithTokenProvider`
- **Execução concorrente** — `FanOut` e `Pipeline` DAG com cascade abort e retry exponencial
- **Options pattern** consistente — sem structs de configuração expostas
- **Container com lifecycle** — `InitAll` em ordem, `ShutdownAll` em ordem reversa

## Instalação

```bash
go get github.com/raywall/go-code-blocks
```

Requer **Go 1.22** ou superior.

## Início rápido — nano serviço completo

```go
package main

import (
    "context"
    "net/http"

    "github.com/raywall/go-code-blocks/blocks/decision"
    "github.com/raywall/go-code-blocks/blocks/dynamodb"
    "github.com/raywall/go-code-blocks/blocks/flow"
    "github.com/raywall/go-code-blocks/blocks/output"
    "github.com/raywall/go-code-blocks/blocks/server"
    "github.com/raywall/go-code-blocks/blocks/transform"
    "github.com/raywall/go-code-blocks/core"
)

type Customer struct {
    ID   string `dynamodbav:"id"            json:"id"            decision:"-"`
    Type string `dynamodbav:"customer_type" json:"customer_type" decision:"customer_type"`
}

func main() {
    ctx := context.Background()

    // ── Blocos de integração ──────────────────────────────────────────────────
    customersDB := dynamodb.New[Customer]("customers",
        dynamodb.WithRegion("us-east-1"),
        dynamodb.WithTable("customers-prod"),
        dynamodb.WithPartitionKey("id"),
    )

    rules := decision.New("rules",
        decision.WithRule("is-pj", `customer_type == "PJ"`,
            decision.Schema{"customer_type": decision.String}),
    )

    // ── Pipeline declarativo ──────────────────────────────────────────────────
    orderFlow := flow.New("create-order",
        flow.NewStep("validate",
            flow.Validate(rules, "is-pj",
                func(req *server.Request, _ *flow.State) map[string]any {
                    return map[string]any{"customer_type": req.Header("X-Type")}
                },
                flow.WithFailStatus(http.StatusForbidden),
            )),

        flow.EnrichStep("load-customer",
            func(ctx context.Context, req *server.Request, _ *flow.State) (any, error) {
                return customersDB.GetItem(ctx, req.PathParam("id"), nil)
            }),

        flow.NewStep("build-response",
            flow.Transform(func(ctx context.Context, req *server.Request, s *flow.State) error {
                payload, err := transform.New(s, req).
                    Pick("load-customer", "id", "type").
                    Set("status", "approved").
                    Build()
                if err != nil { return err }
                s.Set("response", payload)
                return nil
            })),

        flow.NewStep("respond", output.Created("response")),
    )

    // ── Router e servidor ─────────────────────────────────────────────────────
    router := server.NewRouter()
    router.POST("/orders/:id", orderFlow.Handler())

    api := server.NewHTTP("api",
        server.WithPort(8080),
        server.WithRouter(router),
        server.WithMiddleware(server.Logging(), server.Recovery()),
    )

    app := core.NewContainer()
    app.MustRegister(customersDB)
    app.MustRegister(rules)
    app.MustRegister(orderFlow)
    app.MustRegister(api)

    app.InitAll(ctx)
    defer app.ShutdownAll(ctx)
    api.Wait()
}
```

## Blocos disponíveis

### Flow — pipeline de processamento de requisições

`flow.Block` transforma uma sequência de steps declarativos num `server.Handler`. Cada step recebe a requisição e o `State` acumulado, podendo ler dados anteriores, armazenar novos, ou encerrar o fluxo.

```go
f := flow.New("meu-flow",
    flow.NewStep("step-1", stepFn),
    flow.EnrichStep("load-data", enrichFn),
    flow.DecideStep("check-rule", rules, inputFn),
    flow.NewStep("respond", output.OK("result")),
)

// Registra no container e pluga no router
app.MustRegister(f)
router.POST("/path", f.Handler())
```

#### Steps disponíveis

| Construtor | Faz |
|---|---|
| `flow.NewStep(name, fn)` | Step genérico — qualquer `StepFn` |
| `flow.EnrichStep(name, fn)` | Busca dado externo, guarda em `state[name]` |
| `flow.DecideStep(name, d, inputFn)` | Avalia CEL, guarda `*decision.Result` em `state[name]` |
| `flow.DecideFromStep(name, d, inputFn)` | Igual, com struct ao invés de map |
| `flow.Validate(d, rule, inputFn, opts...)` | CEL → aborta com 422 se falhar |
| `flow.ValidateFrom(d, rule, inputFn, opts...)` | Igual, com struct |
| `flow.Transform(fn)` | Manipula state sem I/O externo |
| `flow.Respond(fn)` | Constrói a resposta final |
| `flow.AbortIf(condition, respFn)` | Short-circuit condicional |

```go
// Opções de Validate
flow.WithFailStatus(http.StatusForbidden)
flow.WithFailMessage(func(rule string) string { return "acesso negado" })
```

#### State — contexto compartilhado entre steps

```go
state.Set("key", value)           // armazena dado
state.Get("key")                  // recupera como any
state.Bind("key", &dest)          // deserializa em struct (JSON roundtrip)
state.Has("key")                  // verifica existência
state.Decision("step-name")       // *decision.Result do DecideStep
state.Passed("step-name", "rule") // atalho: regra passou?
state.Failed("step-name", "rule") // inverso de Passed
state.Abort(server.Error(422, "")) // short-circuit imediato
state.Respond(server.JSON(200, v)) // seta resposta final
```

### Transform — composição declarativa de dados

Utilitários puros para mapear, combinar e transformar dados do `State` sem boilerplate manual.

```go
// Funções standalone
transform.Merge(state, "load-customer", "load-credit")
transform.Pick(state, "load-customer", "id", "name")
transform.Omit(state, "load-customer", "password", "raw_data")
transform.Rename(state, "load-customer", map[string]string{"tax_id": "cnpj"})

// Builder fluente
payload, err := transform.New(state, req).
    AllFrom("load-customer").                      // spread todos os campos
    Pick("load-credit", "available").              // um campo específico
    Field("credit_left", "load-credit", "available"). // renomeia ao copiar
    PathParam("order_id", "id").                   // do :id da URL
    QueryParam("page", "page").                    // do ?page=
    Header("trace_id", "X-Request-Id").            // do header
    Set("status", "pending").                      // valor estático
    Compute("total_tax", func(s *flow.State, _ *server.Request) any {
        // lógica calculada
        return 1.12
    }).
    Build()

// Como step do flow (armazena resultado em state["payload"])
flow.NewStep("build-payload",
    transform.Step("payload", func(b *transform.Builder) *transform.Builder {
        return b.AllFrom("load-customer").Pick("load-credit", "available")
    }))
```

### Output — respostas e payloads REST

Construtores de `flow.StepFn` para respostas HTTP e requisições REST declarativas.

#### Respostas HTTP

```go
output.JSON(http.StatusOK, "state-key")        // lê state["state-key"] → JSON
output.JSONFrom(200, func(s *flow.State, req *server.Request) any { ... })
output.Created("state-key")                     // JSON 201
output.OK("state-key")                          // JSON 200
output.Text(200, "state-key")                   // text/plain
output.NoContent()                              // 204
output.Redirect(302, func(s, req) string { return "/new-path" })

// Uso em flow
flow.NewStep("respond", output.Created("order"))
flow.NewStep("respond", output.JSONFrom(200, func(s *flow.State, _ *server.Request) any {
    var u User
    s.Bind("load-user", &u)
    return u
}))
```

#### Payloads REST — output.REST

Constrói `restapi.Request` a partir do state atual, para uso em `flow.EnrichStep`.

```go
// GET com path param resolvido do state
flow.EnrichStep("load-credit",
    output.CallJSON(creditAPI, &CreditResponse{},
        output.REST("GET", "/credit/{tax_id}").
            PathParamFromState("tax_id", "load-customer", "tax_id").
            HeaderFromRequest("X-Trace-Id"),
    ))

// POST com body do state e header estático
flow.EnrichStep("create-invoice",
    output.Call(invoiceAPI,
        output.REST("POST", "/invoices").
            BodyFromState("payload").
            Header("X-Source", "go-code-blocks").
            HeaderFromRequest("X-Request-Id"),
    ))

// Body derivado dinamicamente
output.REST("POST", "/notify").
    BodyFrom(func(s *flow.State, req *server.Request) any {
        var customer Customer
        s.Bind("load-customer", &customer)
        return map[string]any{"email": customer.Email}
    })
```

### Server — blocos de entrada

#### HTTP standalone

```go
httpBlock := server.NewHTTP(name,
    server.WithPort(8080),
    server.WithRouter(router),
    server.WithMiddleware(
        server.RequestID(),
        server.Logging(),
        server.Recovery(),
        server.CORS(server.CORSConfig{}),
    ),
    server.WithReadTimeout(15*time.Second),
    server.WithWriteTimeout(15*time.Second),
    server.WithShutdownTimeout(10*time.Second),
    server.WithTLS("cert.pem", "key.pem"), // opcional
)
```

#### Lambda (API Gateway v1/v2 e ALB)

```go
lambdaBlock := server.NewLambda(name,
    server.WithSource(server.SourceAPIGatewayV2),
    server.WithRouter(router),
    server.WithMiddleware(server.Logging(), server.Recovery()),
)
lambdaBlock.Start() // bloqueia — entrega controle ao runtime Lambda
```

#### TCP raw (IoT, GPS, protocolos binários)

```go
tcp := server.NewTCP(name,
    server.WithTCPPort(5001),
    server.WithBufSize(2048),
    server.WithConnReadTimeout(5*time.Minute),
    server.WithConnHandler(func(ctx context.Context, conn *server.Conn) {
        defer conn.Close()
        for {
            data, err := conn.ReadMessage()
            if err != nil { return }
            // processa data...
        }
    }),
)
```

#### Router

```go
router := server.NewRouter()
router.Use(authMiddleware)
router.GET("/users/:id", getUser)
router.POST("/users", createUser, adminOnly)
router.NotFound(customNotFoundHandler)
```

#### Request e Response

```go
// Request
req.PathParam("id") / req.QueryParam("q") / req.Header("Authorization")
req.BindJSON(&payload)

// Response
server.JSON(200, data)
server.Error(404, "not found")
server.NoContent()
server.Redirect(301, "/new-url")
```

#### Middleware embutido

```go
server.Logging()   // slog: method, path, status, latency
server.Recovery()  // panic → HTTP 500
server.CORS(server.CORSConfig{AllowOrigins: []string{"https://app.example.com"}})
server.RequestID() // propaga X-Request-Id
```

### DynamoDB

```go
block := dynamodb.New[T](name,
    dynamodb.WithRegion("us-east-1"),
    dynamodb.WithTable("my-table"),
    dynamodb.WithPartitionKey("id"),
    dynamodb.WithSortKey("sk"),                     // opcional
    dynamodb.WithEndpoint("http://localhost:8000"),  // DynamoDB Local
)
```

| Método | Descrição |
|---|---|
| `PutItem(ctx, item T)` | Upsert completo |
| `GetItem(ctx, pk, sk)` | Busca por chave primária |
| `DeleteItem(ctx, pk, sk)` | Remoção por chave primária |
| `QueryItems(ctx, QueryInput)` | Query paginada |
| `ScanItems(ctx, limit, lastKey)` | Scan paginado |

### RDS / Aurora

```go
block := rds.New(name,
    rds.WithDriver(rds.DriverPostgres), // ou DriverMySQL
    rds.WithHost("mydb.cluster.us-east-1.rds.amazonaws.com"),
    rds.WithPort(5432),
    rds.WithDatabase("myapp"),
    rds.WithUsername("app"),
    rds.WithPassword("secret"),
    rds.WithSSLMode("require"),

    // ou DSN completo (ideal para Secrets Manager):
    rds.WithDSN("postgres://user:pass@host:5432/db?sslmode=require"),

    rds.WithMaxOpenConns(20),
    rds.WithQueryTimeout(10*time.Second),
)
```

| Método | Descrição |
|---|---|
| `QueryRows(ctx, sql, args...)` | `[]Row` dinâmico (map) |
| `QueryOne(ctx, &dest, sql, args...)` | Uma linha em struct |
| `QueryAll[T](ctx, block, sql, args...)` | Todas as linhas em `[]T` |
| `QueryPage[T](ctx, block, page, size, sql, args...)` | Paginação automática |
| `Exec(ctx, sql, args...)` | INSERT/UPDATE/DELETE |
| `Tx(ctx, func(*sql.Tx) error)` | Transação com commit/rollback |
| `Ping(ctx)` | Health check |

### Redis

```go
block := redis.New(name,
    redis.WithAddr("localhost:6379"),
    redis.WithKeyPrefix("myapp:"),
)
```

| Método | Descrição |
|---|---|
| `Set / Get` | String com TTL |
| `SetJSON / GetJSON` | Serialização JSON tipada |
| `Delete(ctx, keys...)` | Remoção em lote |
| `HSet / HGet / HGetAll` | Operações em hash |

### SSM Parameter Store

```go
block := parameterstore.New(name,
    parameterstore.WithRegion("us-east-1"),
    parameterstore.WithPathPrefix("/myapp/prod"),
    parameterstore.WithDecryption(),
)
```

### Secrets Manager

```go
block := secretsmanager.New(name,
    secretsmanager.WithRegion("us-east-1"),
)
// GetSecretJSON(ctx, name, &v), CreateSecret, UpdateSecret, RotateSecret...
```

### REST API

```go
block := restapi.New(name,
    restapi.WithBaseURL("https://api.example.com/v1"),
    restapi.WithTimeout(10*time.Second),
    restapi.WithBearerToken("eyJ..."),                                    // static
    restapi.WithOAuth2ClientCredentials(tokenURL, clientID, secret),      // OAuth2
    restapi.WithTokenProvider(authBlock),                                 // chaining
    restapi.WithBasicAuth("user", "pass"),
    restapi.WithAPIKeyHeader("X-API-Key", "abc123"),
)
```

#### FanOut — requests paralelos

```go
results, err := api.FanOut(ctx, map[string]restapi.Request{
    "user":    {Path: "/users/123"},
    "catalog": {Path: "/products?limit=50"},
}, restapi.WithDefaultRetry(restapi.RetryPolicy{MaxAttempts: 3, Delay: 100*time.Millisecond, Backoff: 2.0}))

var user User
results.JSON("user", &user)
```

#### Pipeline DAG com cascade abort e retry

```go
steps := []restapi.PipelineStep{
    {Name: "user", Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
        return restapi.Request{Path: "/users/123"}, nil
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
}
results, err := api.Pipeline(ctx, steps,
    restapi.WithDefaultRetry(restapi.RetryPolicy{MaxAttempts: 2, Delay: 100*time.Millisecond, Backoff: 1.5}),
    restapi.WithContinueOnError(),
)
sr := results.Get("orders")
sr.OK() / sr.Skipped() / sr.Attempts / sr.Latency
```

### Bloco de decisão (CEL)

```go
router := decision.New(name,
    decision.WithRule("is-pj", `customer_type == "PJ"`,
        decision.Schema{"customer_type": decision.String}),
    decision.WithRule("high-value", `amount > 10000.0`,
        decision.Schema{"amount": decision.Float}),
)
result, _ := router.EvaluateAllFrom(ctx, myStruct)
result.Passed("is-pj") / result.Any() / result.All() / result.None()
```

## Configuração AWS

```go
dynamodb.WithAWSConfig(myCfg)                        // aws.Config pré-construído
dynamodb.WithRegion("sa-east-1")                     // credential chain padrão
dynamodb.WithProfile("staging")                      // profile nomeado
dynamodb.WithEndpoint("http://localhost:8000")        // endpoint local
```

## Container

```go
app := core.NewContainer()
app.MustRegister(block)
if err := app.InitAll(ctx); err != nil { ... }
defer app.ShutdownAll(ctx)
users, err := core.Get[*dynamodb.Block[User]](app, "users")
```

## Erros sentinela

```go
core.ErrItemNotFound / core.ErrNotInitialized / core.ErrBlockNotFound
restapi.ErrSkipped   // step de pipeline abortado em cascade (errors.Is)
```

## Desenvolvimento local

```bash
cd samples/local-dev
docker compose up -d   # DynamoDB Local + LocalStack (S3 + SSM) + Redis
go run .
```

## Samples

| Diretório | Demonstra |
|---|---|
| `samples/database/` | DynamoDB — CRUD, Query, Scan |
| `samples/cache/` | Redis — string, JSON, hash, TTL |
| `samples/storage/` | S3 — upload, download, presign |
| `samples/config/` | SSM — hierarquia, SecureString |
| `samples/secrets/` | Secrets Manager — JSON, rotação |
| `samples/rds/` | RDS PostgreSQL — CRUD, transações, busca |
| `samples/decision/` | Decision + DynamoDB — roteamento PJ/PF |
| `samples/decision-pipeline/` | CEL + DynamoDB + Redis + SSM |
| `samples/restapi/` | REST API — verbos, auth, encadeamento |
| `samples/restapi-pipeline/` | FanOut, Pipeline DAG, WaveTimeout |
| `samples/restapi-resilience/` | Cascade abort, retry com backoff |
| `samples/flow-order/` | **Flow completo** — Validate → Enrich → Decide → Transform → Respond |
| `samples/server-http/` | HTTP server com DynamoDB + Redis + CEL |
| `samples/server-lambda/` | Lambda — API Gateway v2/v1/ALB |
| `samples/server-local/` | TCP — rastreador OBD/GPS com NMEA |
| `samples/full-stack/` | DynamoDB + Redis + S3 + SSM + Secrets Manager |
| `samples/local-dev/` | Docker Compose — ambiente local completo |

## Layout do projeto

```
go-code-blocks/
├── core/                    # Block interface, Container, erros, Get[T]
├── internal/awscfg/         # Resolução de aws.Config compartilhada
├── blocks/
│   ├── flow/                # Pipeline de processamento de requisições
│   │   ├── block.go         # Flow, New, NewStep, Handler(), execute()
│   │   ├── state.go         # State: Set/Get/Bind/Decision/Abort/Respond
│   │   └── steps.go         # Validate, ValidateFrom, Enrich/DecideStep, Transform, Respond, AbortIf
│   ├── transform/           # Utilitários de mapeamento de dados (sem lifecycle)
│   │   └── transform.go     # Merge, Pick, Omit, Rename, Builder fluente, Step
│   ├── output/              # Construtores de resposta e payload REST (sem lifecycle)
│   │   └── output.go        # JSON, Created, OK, NoContent, Redirect, REST builder, Call, CallJSON
│   ├── decision/            # Regras CEL via go-decision-engine
│   ├── dynamodb/            # DynamoDB tipado com generics
│   ├── rds/                 # PostgreSQL e MySQL/Aurora via database/sql
│   ├── s3/                  # Object storage
│   ├── redis/               # Cache e estruturas de dados
│   ├── parameterstore/      # SSM Parameter Store
│   ├── secretsmanager/      # AWS Secrets Manager
│   ├── restapi/             # HTTP REST — OAuth2, Pipeline DAG, Retry
│   └── server/              # Blocos de entrada — HTTP, Lambda, TCP
└── samples/                 # Exemplos executáveis
```

## Dependências

| Módulo | Uso |
|---|---|
| `github.com/aws/aws-sdk-go-v2` | SDK AWS (DynamoDB, S3, SSM, Secrets Manager) |
| `github.com/aws/aws-lambda-go` | Runtime Lambda e eventos (API Gateway, ALB) |
| `github.com/lib/pq` | Driver PostgreSQL |
| `github.com/go-sql-driver/mysql` | Driver MySQL |
| `github.com/redis/go-redis/v9` | Cliente Redis |
| `github.com/raywall/go-decision-engine` | Motor de regras CEL |

## Licença

MIT — veja [LICENSE](LICENSE).