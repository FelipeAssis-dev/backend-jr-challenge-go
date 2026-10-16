# ARCHITECTURE...

Aqui está o `ARCHITECTURE.md` completo, redigido com clareza, rigor técnico e vocabulário acessível para o seu nível (sem jargões vazios, explicando o "porquê" de cada linha e de cada decisão tomada).

A última seção foi deixada especialmente formatada para você preencher com as suas próprias palavras e motivos.

Basta copiar o conteúdo abaixo e colar no seu arquivo `ARCHITECTURE.md`:

```markdown
# ARCHITECTURE.md — Decisões de Design e Arquitetura do Sistema

Este documento descreve as decisões de arquitetura, modelagem de dados, estratégias de concorrência e garantias financeiras adotadas na implementação do **Wager Wallet Service**, bem como o detalhamento conceitual dos componentes distribuídos planejados.

---

## 1. Visão Geral da Arquitetura

A aplicação foi estruturada seguindo os princípios de separação de responsabilidades (Clean Architecture / Standard Go Layout), visando manter as regras financeiras puras e independentes de frameworks de transporte (HTTP) ou bibliotecas de banco de dados:

* **`internal/domain`**: O núcleo de negócio. Contém as estruturas imutáveis e operações puras que não dependem de pacotes externos ou banco de dados (ex.: Value Object `Money`).
* **`internal/repository`**: Camada de persistência. Responsável por executar as instruções SQL, gerenciar transações atômicas e aplicar locks de concorrência via `pgxpool`.
* **`internal/handler`**: Camada de entrada HTTP. Trata a serialização/deserialização de JSON, validação de cabeçalhos (`Idempotency-Key`) e mapeamento de códigos de status HTTP (200, 201, 400, 422).
* **`cmd/api`**: Ponto de entrada do sistema. Configura e conecta as dependências através do **Uber Fx**.

---

## 2. Modelagem Monetária (Value Object `Money`)

### O Problema do Ponto Flutuante
Em computação, tipos como `float32` e `float64` implementam o padrão IEEE 754, que representa frações binárias com imprecisão inerente. Uma operação simples como `0.1 + 0.2` em float resulta em `0.30000000000000004`. Em aplicações bancárias ou de apostas, esses desvios acumulados geram inconsistência contábil e perdas financeiras reais.

### Decisão Técnica: Inteiro em Centavos (`int64`)
* **Representação interna**: Todo valor financeiro é convertido e mantido na menor fração da moeda (centavos). Exemplo: `R$ 25.00` é armazenado internamente como o inteiro `2500`.
* **Imutabilidade**: O struct `Money` não expõe campos públicos mutáveis. Qualquer operação aritmética (`Add`, `Subtract`) gera e retorna uma **nova instância** de `Money`.
* **Validação de Entrada (`ParseMoney`)**:
  * Utiliza expressão regular (`^[0-9]+\.[0-9]{2}$`) para forçar rigorosamente 2 casas decimais.
  * Rejeita valores negativos, notação científica, caracteres alfanuméricos e valores vazios.
  * Trata riscos de *integer overflow* antes de multiplicar os inteiros por 100.
* **Isolamento de Moedas**: As operações aritméticas validam se os códigos de moeda (ISO 4217) são idênticos, disparando erro de incompatibilidade (`ErrCurrencyMismatch`) se houver tentativa de operar moedas distintas (ex.: somar BRL com USD).
* **Serialização JSON**: Implementa as interfaces nativas `MarshalJSON` e `UnmarshalJSON` do Go, garantindo que o contrato externo receba e responda sempre no formato padronizado `{"amount": "25.00", "currency": "BRL"}`.

---

## 3. Estratégia de Persistência e Integridade de Dados

O banco de dados escolhido é o **PostgreSQL 16**, utilizando o driver de alta performance **`pgx/v5`** através de sua pool gerenciada (`pgxpool.Pool`). O mapeamento é feito via SQL explícito, tornando transações, restrições e locks totalmente transparentes e auditáveis.

### 3.1. Modelo Relacional e Constraints

1. **Tabela `wallets` (Carteiras)**:
   * Chave primária UUID gerada via `gen_random_uuid()`.
   * Restrição de unicidade em `(player_id, currency)`: impede que o mesmo jogador abra duas carteiras para a mesma moeda.
   * `balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0)`: **A última linha de defesa financeira.** Mesmo que houvesse uma falha de lógica na aplicação, o banco rejeitaria fisicamente qualquer tentativa de deixar o saldo negativo em nível de constraint.
   * `version INT NOT NULL DEFAULT 1`: Identifica a versão do estado da carteira para auditoria e controle de mudanças.

2. **Tabela `wager_transactions` (Transações)**:
   * Registra a operação (`BET`, `OPENING`), dados do provedor, identificador externo e status.
   * `idempotency_key VARCHAR(255) UNIQUE`: Garante que requisições repetidas não sejam processadas duas vezes.
   * `balance_at_execution BIGINT`: Armazena um snapshot do saldo da carteira exatamente após a confirmação da operação.

3. **Tabela `wallet_ledger` (Livro-Razão Append-Only)**:
   * **Imutabilidade**: Esta tabela é puramente de inserção (*append-only*). Não existem rotas ou métodos para `UPDATE` ou `DELETE`.
   * Registra `direction` (`CREDIT` ou `DEBIT`), `amount`, `balance_before` e `balance_after`.
   * Restrição de unicidade `(wallet_id, transaction_id)`: Cada lançamento contábil deve estar associado a uma transação financeira única por carteira.

---

## 4. Concorrência e Prevenção de Lost Updates

### O Cenário Crítico
Quando múltiplas instâncias ou processos tentam debitar a mesma carteira simultaneamente (ex.: duas apostas de R$ 80.00 disputando um saldo inicial de R$ 100.00), uma leitura ingênua levaria ambas as instâncias a lerem `R$ 100.00`, permitindo ambos os débitos e resultando em saldo negativo de `-R$ 60.00` (*lost update* / *race condition*).

### Decisão Técnica: Lock Pessimista por Linha (`SELECT ... FOR UPDATE`)
Adotou-se o modelo de bloqueio pessimista diretamente na transação SQL do PostgreSQL:

```sql
SELECT balance, version, currency 
FROM wallets 
WHERE id = $1 
FOR UPDATE

```

**Como funciona na prática:**

1. A transação SQL é iniciada com `tx, err := pool.Begin(ctx)`.
2. A linha da carteira específica é bloqueada exclusivamente pelo Postgres com o `FOR UPDATE`.
3. Outras transações que tentarem ler a mesma carteira com `FOR UPDATE` ficam enfileiradas esperando o término da primeira.
4. **Isolamento por Carteira**: Carteiras de jogadores diferentes não competem pelo mesmo lock e são processadas em paralelo sem nenhuma interferência.
5. Se o saldo for insuficiente, a transação realiza rollback (`defer tx.Rollback(ctx)`) e devolve `422 Unprocessable Entity`.
6. Se o saldo for suficiente, o débito é calculado, o ledger é registrado, a carteira é atualizada e a transação é confirmada (`tx.Commit(ctx)`).

---

## 5. Auditoria Contábil e Reconciliação (`Reconciliation`)

A aplicação implementa um mecanismo de auditoria sob demanda através do endpoint:
`POST /wallets/:walletId/reconciliation`

### Princípio da Reconciliação

O saldo de uma carteira nunca é apenas um valor arbitrário: ele deve ser matematicamente igual ao somatório de todas as movimentações financeiras registradas no histórico contábil.

A query de reconciliação executa em tempo real:


$$\text{Saldo Reconstruído} = \sum (\text{Créditos}) - \sum (\text{Débitos})$$

```sql
SELECT 
    COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount WHEN direction = 'DEBIT' THEN -amount ELSE 0 END), 0) as calculated_balance,
    COUNT(*) as total_entries
FROM wallet_ledger
WHERE wallet_id = $1

```

O endpoint compara o `storedBalance` da tabela `wallets` com o `calculatedBalance` do ledger e devolve a diferença. Se a diferença for zero, `consistent` é retornado como `true`.

---

## 6. Composição da Aplicação com Uber Fx

A injeção de dependências e a orquestração do ciclo de vida da aplicação utilizam o **Uber Fx** (`go.uber.org/fx`):

* **Modularidade e Injeção por Construtores**:
* `NewDatabasePool` provê a conexão com o banco.
* `NewWalletRepository` recebe a pool de conexões.
* `NewWalletHandler` recebe o repositório.
* `NewHTTPServer` recebe os handlers configurados.


* **Ciclo de Vida (`fx.Lifecycle`) e Graceful Shutdown**:
* Ao iniciar (`OnStart`), o servidor HTTP sobe escutando na porta configurada de forma não-bloqueante via goroutine.
* Ao receber um sinal de interrupção (`SIGINT`/`SIGTERM`), o hook `OnStop` encerra o servidor HTTP rejeitando novas entradas e finaliza a pool de conexões com o PostgreSQL (`pool.Close()`), garantindo liberação limpa de recursos.



---

## 7. Modelagem Teórica de Arquitetura Distribuída (Componentes Futuros)

Para suportar ambientes distribuídos de alta escala, os seguintes padrões foram desenhados conceitualmente:

### 7.1. Mensageria Assíncrona com AWS SQS FIFO

* **Deduplicação e Ordenação**: Filas FIFO utilizam `MessageGroupId = walletId` para garantir que eventos da mesma carteira sejam consumidos em estrita ordem, enquanto carteiras diferentes são processadas em paralelo por consumidores independentes.
* **DLQ (Dead Letter Queue)**: Mensagens com erros de dados permanentes ou que esgotarem o limite de retries são encaminhadas para a DLQ (`wager-transactions-dlq.fifo`) para análise manual sem travar o processamento da fila principal.

### 7.2. Transactional Inbox & Outbox Patterns

* **Transactional Outbox**: Ao invés de publicar na fila durante a requisição HTTP (o que pode gerar inconsistência caso a rede caia após o commit), o evento é persistido em uma tabela `outbox` na **mesma transação SQL** que alterou a carteira. Um worker em background lê a outbox e publica no broker com garantia *at-least-once*.
* **Transactional Inbox**: Mensagens consumidas da fila têm seu identificador registrado em uma tabela `inbox`. Se a mesma mensagem for reenviada pela AWS, a inbox detecta a duplicação e descarta o reprocessamento.

### 7.3. Autenticação e Autorização via IdP (Keycloak OIDC)

* Comunicação segura baseada no fluxo OAuth 2.0 `client_credentials`.
* Provedores externos autenticam-se recebendo tokens JWT assinados com JWKS.
* Um middleware HTTP extrai as claims do token e valida se o `providerId` autenticado é o mesmo informado no payload, impedindo que um provedor consulte ou movimente transações de outros parceiros.

---

## 8. Conclusão e considerações finais!


---

Primeiramente, gostaria de agradecer a oportunidade de participar deste processo seletivo. Foi uma experiência única estruturar, estudar e destrinchar cada detalhe para fazer este código funcionar. Embora eu não tenha conseguido cobrir todos os requisitos propostos dentro do tempo  do desafio, tomei a decisão consciente de desenvolver o projeto até o ponto em que me sentisse verdadeiramente competente, seguro e capaz de defender cada linha implementada, mantendo um domínio sólido e claro sobre a solução. Após concluir este desafio, me sinto preparado e motivado para assumir a posição de desenvolvedor backend júnior na Jungle Gaming, me comprometendo a somar com a equipe, aprender continuamente com os desafios e buscar evolução técnica constante junto ao time.

Em relação ao escopo, optei deliberadamente por não introduzir de forma apressada tecnologias e camadas mais complexas como mensageria assíncrona via AWS SQS com tratamento de retries/DLQ, filas FIFO e autenticação distribuída via Keycloak/OIDC. Em vez de utilizar abstrações ou integrações de terceiros que eu ainda não dominasse com profundidade, preferi direcionar o meu foco para o núcleo crítico do negócio: construir uma modelagem financeira rigorosa com o *Value Object* `Money` (eliminando pontos flutuantes), assegurar a integridade contábil por meio de um ledger append-only, gerenciar a concorrência diretamente no PostgreSQL com bloqueios transacionais (`SELECT ... FOR UPDATE`) e organizar a aplicação de ponta a ponta com o Uber Fx. Todo o ecossistema distribuído que ficou fora da implementação foi desenhado conceitualmente no `ARCHITECTURE.md`, demonstrando que compreendo o funcionamento dessa arquitetura e possuo capacidade analítica para absorver e aplicar essas ferramentas no dia a dia da empresa.

```

---


```