
# Wager Wallet Service — Processamento de Apostas e Carteiras em Go

Um serviço em **Go** construído com **Uber Fx**, **pgx/v5** e **PostgreSQL**, concebido para movimentação financeira de carteiras de jogadores e processamento de apostas (`BET`, abertura com `OPENING`) com rigor contabilístico e garantias estritas de concorrência.

A aplicação elimina a utilização de ponto flutuante (`float`), assegura a integridade de um livro-razão (*ledger append-only*) e protege o saldo de jogadores contra *race conditions* e *lost updates* através de bloqueios transacionais no PostgreSQL (`SELECT ... FOR UPDATE`).

* Enunciado do desafio: [README.md original / CHALLENGE.md](https://www.google.com/search?q=docs/CHALLENGE.md)
* Decisões de design, modelo de concorrência e arquitetura futura: [ARCHITECTURE.md](https://www.google.com/search?q=ARCHITECTURE.md)

```
                     ┌────────────────────────┐
                     │      Cliente HTTP      │
                     └───────────┬────────────┘
                                 │ HTTP (JSON)
                                 ▼
                     ┌────────────────────────┐
                     │     Uber Fx App        │
                     │  (Ciclo de Vida/HTTP)  │
                     └───────────┬────────────┘
                                 │ pgxpool (Transação SQL atómica)
                                 ▼
         ┌──────────────────────────────────────────────────┐
         │             PostgreSQL 16 (Contentor)            │
         │  - wallets (SELECT ... FOR UPDATE)               │
         │  - wager_transactions (Idempotência / Status)    │
         │  - wallet_ledger (Append-only / Imutável)        │
         └──────────────────────────────────────────────────┘

```

## Índice

1. [Visão Geral e Destaques](https://www.google.com/search?q=%231-vis%C3%A3o-geral-e-destaques)
2. [Pré-requisitos](https://www.google.com/search?q=%232-pr%C3%A9-requisitos)
3. [Início Rápido](https://www.google.com/search?q=%233-in%C3%ADcio-r%C3%A1pido)
4. [Variáveis de Ambiente](https://www.google.com/search?q=%234-vari%C3%A1veis-de-ambiente)
5. [Migrações e Base de Dados](https://www.google.com/search?q=%235-migra%C3%A7%C3%B5es-e-base-de-dados)
6. [Executar a Aplicação](https://www.google.com/search?q=%236-executar-a-aplica%C3%A7%C3%A3o)
7. [API HTTP e Exemplos](https://www.google.com/search?q=%237-api-http-e-exemplos)
8. [Auditoria e Reconciliação](https://www.google.com/search?q=%238-auditoria-e-reconcilia%C3%A7%C3%A3o)
9. [Testes](https://www.google.com/search?q=%239-testes)
10. [Estrutura do Projeto](https://www.google.com/search?q=%2310-estrutura-do-projeto)
11. [Decisões de Escopo e Arquitetura Futura](https://www.google.com/search?q=%2311-decis%C3%B5es-de-escopo-e-arquitetura-futura)

---

## 1. Visão Geral e Destaques

* **Precisão Decimal sem Ponto Flutuante (`float`)**: Implementação de Value Object `Money` a operar internamente em cêntimos (`int64`), com validação de escala fixa de 2 casas, proteção contra overflow e serialização JSON personalizada.
* **Prevenção Concorrencial de Saldo Negativo**: Coordenação por carteira através de `SELECT ... FOR UPDATE` no PostgreSQL. Garante que duas apostas concorrentes não originem *lost updates* e que débitos superiores ao saldo disponível sejam rejeitados com código 422.
* **Livro-Razão Imutável (*Ledger Append-Only*)**: Qualquer alteração de saldo persiste os estados anterior e posterior (`balance_before` e `balance_after`), viabilizando auditoria contabilística integral.
* **Composição com Uber Fx**: Injeção de dependências modular (`fx.Provide`) e gestão de ciclo de vida (`fx.Lifecycle`), garantindo encerramento gracioso (*graceful shutdown*) do servidor HTTP e da ligação à base de dados.

---

## 2. Pré-requisitos

| Ferramenta | Versão recomendada | Finalidade |
| --- | --- | --- |
| Docker + Docker Compose | v2+ | Inicialização do PostgreSQL 16 isolado |
| Go (Golang) | 1.22+ | Compilação e execução da aplicação e testes |
| cURL / PowerShell / Postman | Qualquer | Execução de pedidos HTTP nos endpoints |

Portas utilizadas:

* `5432`: PostgreSQL
* `8080`: Servidor HTTP da API

---

## 3. Início Rápido

### 1. Iniciar a Base de Dados (PostgreSQL)

```sh
docker compose up -d postgres

```

O PostgreSQL arranca aplicando automaticamente o script de migração DDL em `migrations/001_init.sql`.

Para verificar o estado do contentor:

```sh
docker compose ps

```

### 2. Iniciar a API HTTP

Na raiz do projeto:

```sh
go run cmd/api/main.go

```

A aplicação arranca via **Uber Fx** e fica disponível na porta `8080`:

```text
[Fx] RUNNING
Servidor HTTP rodando na porta 8080...

```

---

## 4. Variáveis de Ambiente

A aplicação aceita configuração por variáveis de ambiente com valor padrão local:

| Variável | Valor Padrão Local | Descrição |
| --- | --- | --- |
| `DATABASE_URL` | `postgres://postgres:password@localhost:5432/betting_db?sslmode=disable` | String de ligação ao PostgreSQL (`pgxpool`) |
| `PORT` | `8080` | Porta TCP do servidor HTTP |

---

## 5. Migrações e Base de Dados

O esquema relacional encontra-se versionado em `migrations/001_init.sql`:

| Tabela | Responsabilidade e Restrições |
| --- | --- |
| `wallets` | Agregado raiz da carteira. Chave única em `(player_id, currency)` e restrição `CHECK (balance >= 0)`. |
| `wager_transactions` | Registo de operações com chave única em `idempotency_key` e persistência do saldo no instante da execução. |
| `wallet_ledger` | Livro-razão *append-only*. Restrição de unicidade em `(wallet_id, transaction_id)` e validação de direção (`DEBIT`/`CREDIT`). |

---

## 6. Executar a Aplicação

Para compilar e executar o binário da API:

```sh
go build -o api cmd/api/main.go
./api

```

Para interromper de forma controlada, envie `Ctrl + C` (`SIGINT`/`SIGTERM`). O Uber Fx aguarda a conclusão dos pedidos ativos e fecha a pool de ligações com segurança.

---

## 7. API HTTP e Exemplos

### 7.1. Health Check

```sh
curl http://localhost:8080/health/live

```

**Resposta:**

```json
{"status":"UP"}

```

---

### 7.2. Abertura de Carteira (`POST /wallets`)

Cria uma carteira para o jogador com saldo inicial. Quando o saldo é positivo, gera de forma atómica a operação `OPENING` e o respetivo crédito no ledger.

**Pedido:**

```sh
curl -X POST http://localhost:8080/wallets \
  -H "Content-Type: application/json" \
  -d '{
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "initialBalance": { "amount": "100.00", "currency": "BRL" }
  }'

```

**Resposta (`201 Created`):**

```json
{
  "id": "bd62bef0-f525-4cd5-aeef-3176886a7d5c",
  "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "balance": { "amount": "100.00", "currency": "BRL" },
  "version": 1
}

```

---

### 7.3. Envio de Operação de Aposta (`POST /wagering/transactions`)

Exige o cabeçalho `Idempotency-Key`. Efetua o débito do saldo dentro de uma transação com bloqueio pessimista (`SELECT FOR UPDATE`).

**Pedido:**

```sh
curl -X POST http://localhost:8080/wagering/transactions \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-001" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-001",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "bd62bef0-f525-4cd5-aeef-3176886a7d5c",
    "roundId": "round-1",
    "gameId": "game-1",
    "kind": "BET",
    "money": { "amount": "80.00", "currency": "BRL" }
  }'

```

**Resposta (`200 OK`):**

```json
{
  "transactionId": "tx-provider-a:tx-001",
  "status": "PROCESSED",
  "balance": { "amount": "20.00", "currency": "BRL" },
  "idempotentReplay": false
}

```

**Em caso de Saldo Insuficiente (`422 Unprocessable Entity`):**

```json
{
  "error": "saldo insuficiente"
}

```

---

## 8. Auditoria e Reconciliação

### `POST /wallets/:walletId/reconciliation`

Calcula o somatório de todos os créditos e débitos registados no livro-razão (`wallet_ledger`) e compara com o saldo armazenado na carteira.

**Pedido:**

```sh
curl -X POST http://localhost:8080/wallets/bd62bef0-f525-4cd5-aeef-3176886a7d5c/reconciliation

```

**Exemplo de Resposta Consistente (`200 OK`):**

```json
{
  "walletId": "bd62bef0-f525-4cd5-aeef-3176886a7d5c",
  "storedBalance": { "amount": "20.00", "currency": "BRL" },
  "calculatedBalance": { "amount": "20.00", "currency": "BRL" },
  "difference": { "amount": "0.00", "currency": "BRL" },
  "consistent": true,
  "checkedEntries": 2
}

```

---

## 9. Testes

### Executar Testes Unitários

Executa os testes de precisão monetária, conversão de strings, operações aritméticas e proteção contra overflow no Value Object `Money`:

```sh
go test ./internal/domain/... -v

```

### Executar com Verificação de Condições de Corrida (*Race Detector*)

```sh
go test -race ./...

```

---

## 10. Estrutura do Projeto

A disposição de diretórios segue as convenções do Standard Go Layout e Clean Architecture:

```text
├── cmd/
│   └── api/
│       └── main.go                 # Ponto de entrada, Uber Fx e Graceful Shutdown
├── internal/
│   ├── domain/
│   │   ├── money.go                # Value Object imutável de dinheiro (sem floats)
│   │   └── money_test.go           # Testes unitários do Money
│   ├── repository/
│   │   └── wallet_repository.go    # Acesso a dados com pgx, SELECT FOR UPDATE e Ledger
│   └── handler/
│       └── wallet_handler.go       # Handlers HTTP REST, validações e DTOs
├── migrations/
│   └── 001_init.sql                # DDL do PostgreSQL (constraints, PKs e tabelas)
├── docker-compose.yml              # Configuração do PostgreSQL 16
├── ARCHITECTURE.md                 # Documentação técnica e modelação concetual
└── README.md                       # Documentação da aplicação e API

```

---

## 11. Decisões de Escopo e Arquitetura Futura

O desenvolvimento focou-se na **integridade matemática, consistência contabilística e controlo de concorrência na base de dados**:

1. **Implementado e Validado**:
* Value Object `Money` com aritmética em cêntimos (`int64`).
* Concorrência via lock pessimista transacional (`SELECT ... FOR UPDATE`).
* Ledger imutável *append-only* com rota de reconciliação matemática.
* Gestão de ciclo de vida com Uber Fx.


2. **Mapeado em Arquitetura (Trabalho Futuro)**:
* **Consumo SQS FIFO e DLQ**: Estruturado concetualmente no `ARCHITECTURE.md` através dos padrões Transactional Inbox/Outbox.
* **Autenticação OIDC/Keycloak**: Desenho do fluxo `client_credentials` com validação de tokens JWT/JWKS em middleware HTTP.
* **Reversões Fora de Ordem (`PENDING_REFERENCE`)**: Modelação da máquina de estados com retries e workers assíncronos.



Consulte o ficheiro [ARCHITECTURE.md](https://www.google.com/search?q=ARCHITECTURE.md) para a análise aprofundada de cada decisão.