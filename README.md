# Rinha de Backend 2026 — Detecção de Fraudes em Go

Submissão para a [Rinha de Backend 2026](https://github.com/zanfranceschi/rinha-de-backend-2026), cujo desafio é construir uma API de detecção de fraudes em transações de cartão usando busca vetorial k-NN sobre um dataset de 3 milhões de vetores de referência.

---

## O Desafio

A [Rinha de Backend 2026](https://github.com/zanfranceschi/rinha-de-backend-2026) pede que cada participante construa uma API que:

1. Recebe os dados de uma transação de cartão via `POST /fraud-score`
2. Converte a transação em um vetor de 14 dimensões
3. Busca os 5 vizinhos mais próximos (k-NN) num dataset de 3 milhões de vetores pré-rotulados
4. Retorna `fraud_score` (fração de fraudes entre os 5 vizinhos) e `approved` (score < 0.6)

**Restrições de recursos:** toda a solução (load balancer + APIs) deve usar no máximo **1 CPU e 350 MB de RAM**.

**Pontuação:** -6000 a +6000 pontos, combinando:
- **Latência** (p99): +3000 para p99 ≤ 1ms, -3000 para p99 > 2000ms
- **Detecção**: penalidade por falsos positivos (peso 1), falsos negativos (peso 3) e erros HTTP (peso 5)

---

## Solução: Índice IVF in-memory em Go

Em vez de usar um banco vetorial externo (Qdrant, pgvector etc.), construímos um **índice IVF (Inverted File Index) próprio**, compilado direto na imagem Docker.

### Por quê não usar banco vetorial?

Com apenas 350 MB de RAM total, um serviço extra consumiria ~100–200 MB adicionais, além de introduzir latência de rede por query. O índice in-process elimina ambos os problemas.

### Como funciona

```
Build time:
  references.json.gz (3M vetores) → k-means (256 clusters) → quantização int8 → index.bin (43 MB)

Query time:
  payload → vetor 14D float32
           → distância aos 256 centroides
           → busca bruta nos 6 clusters mais próximos (~70K vetores)
           → top-5 vizinhos → fraud_score → resposta
```

**Quantização int8:** cada vetor ocupa 14 bytes em vez de 56 bytes (float32), reduzindo 168 MB → 42 MB por instância.

### Arquitetura

```
Cliente → nginx:9999 (round-robin)
               ├─→ api1:8080  (Go + fasthttp)
               └─→ api2:8080  (Go + fasthttp)
```

| Serviço | CPU  | Memória |
|---------|------|---------|
| nginx   | 0.10 | 30 MB   |
| api1    | 0.45 | 160 MB  |
| api2    | 0.45 | 160 MB  |
| **Total** | **1.00** | **350 MB** |

### Stack

- **Go 1.23** — binário estático, sem dependências externas
- **fasthttp** — servidor HTTP zero-allocation para latência mínima
- **goccy/go-json** — unmarshal JSON 2-4× mais rápido que `encoding/json`
- **nginx** — load balancer round-robin com keepalive

---

## Estrutura do Projeto

```
rinha-fraud-go/
├── Dockerfile                   # 3 estágios: compilar → gerar índice → imagem final
├── docker-compose.yml           # nginx + 2 instâncias da API
├── nginx.conf                   # round-robin com keepalive
├── go.mod / go.sum
│
├── cmd/
│   ├── api/main.go              # servidor fasthttp na porta 8080
│   └── indexer/main.go          # CLI que gera o index.bin (roda no build)
│
├── internal/
│   ├── config/config.go         # carrega mcc_risk.json e normalization.json
│   ├── vectorize/
│   │   ├── vectorize.go         # transação → [14]float32 (todas as 14 dimensões)
│   │   └── vectorize_test.go    # testes contra os exemplos da documentação
│   ├── index/
│   │   ├── build.go             # k-means++ + serialização binária
│   │   ├── index.go             # carregamento e busca IVF (hot path)
│   │   └── quantize.go          # float32 ↔ int8
│   └── handler/
│       ├── fraud.go             # POST /fraud-score
│       └── ready.go             # GET /ready
│
└── resources/
    ├── references.json.gz       # 3M vetores rotulados (fonte de dados)
    ├── mcc_risk.json            # risco por categoria de merchant (MCC)
    └── normalization.json       # constantes de normalização
```

---

## Pré-requisitos

- **Docker** ≥ 24
- **Docker Compose** v2
- O arquivo `resources/references.json.gz` deve estar presente (copiado do repositório da Rinha)

Se estiver usando este repo junto com o [repositório da Rinha](https://github.com/zanfranceschi/rinha-de-backend-2026) (que fica em `../rinha-de-backend-2026`), copie os recursos:

```bash
cp ../rinha-de-backend-2026/resources/references.json.gz ./resources/
cp ../rinha-de-backend-2026/resources/mcc_risk.json ./resources/
cp ../rinha-de-backend-2026/resources/normalization.json ./resources/
```

---

## Como Rodar

### 1. Build da imagem

O build tem 3 estágios. O mais demorado é o **Stage 2**, que roda k-means sobre os 3M vetores (~18 minutos na primeira vez, cacheado nas seguintes).

```bash
docker build -t rinha-fraud-go .
```

Saída esperada do indexer durante o build:
```
loading vectors from /resources/references.json.gz
loaded 3000000 vectors in 9.7s
Running k-means: 3000000 vectors, 256 clusters
  k-means iter 1/25
  ...
index written to /data/index.bin in 18m32s
index size: 42.9 MB
```

### 2. Subir a stack

```bash
docker compose up
```

Aguarde os logs das duas instâncias indicarem que estão prontas:
```
api1  | index loaded — ready to serve on :8080
api2  | index loaded — ready to serve on :8080
```

### 3. Verificar saúde

```bash
curl http://localhost:9999/ready
# → ok
```

---

## Como Testar

### Endpoint `GET /ready`

```bash
curl http://localhost:9999/ready
# HTTP 200: ok
```

### Endpoint `POST /fraud-score`

**Transação legítima** (valor baixo, merchant conhecido, perto de casa):

```bash
curl -s -X POST http://localhost:9999/fraud-score \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "tx-1329056812",
    "transaction": { "amount": 41.12, "installments": 2, "requested_at": "2026-03-11T18:45:53Z" },
    "customer": { "avg_amount": 82.24, "tx_count_24h": 3, "known_merchants": ["MERC-003", "MERC-016"] },
    "merchant": { "id": "MERC-016", "mcc": "5411", "avg_amount": 60.25 },
    "terminal": { "is_online": false, "card_present": true, "km_from_home": 29.23 },
    "last_transaction": null
  }'
```

Resposta esperada:
```json
{"approved":true,"fraud_score":0}
```

---

**Transação fraudulenta** (valor alto, merchant desconhecido, longe de casa, sem histórico):

```bash
curl -s -X POST http://localhost:9999/fraud-score \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "tx-3330991687",
    "transaction": { "amount": 9505.97, "installments": 10, "requested_at": "2026-03-14T05:15:12Z" },
    "customer": { "avg_amount": 81.28, "tx_count_24h": 20, "known_merchants": ["MERC-008", "MERC-007", "MERC-005"] },
    "merchant": { "id": "MERC-068", "mcc": "7802", "avg_amount": 54.86 },
    "terminal": { "is_online": false, "card_present": true, "km_from_home": 952.27 },
    "last_transaction": null
  }'
```

Resposta esperada:
```json
{"approved":false,"fraud_score":1}
```

---

### Teste de carga com k6 (usando o repositório da Rinha)

O repositório da Rinha inclui o script de teste oficial. Rode a partir dele:

```bash
cd ../rinha-de-backend-2026
k6 run test/test.js
```

O teste sobe de 1 req/s até 900 req/s em 120 segundos (~5000 requisições no total). A saída inclui o score final:

```json
{
  "p99": "5.81ms",
  "scoring": {
    "p99_score": { "value": 2235.83 },
    "detection_score": { "value": 1189.20 },
    "final_score": 3425.03
  }
}
```

---

## Como Validar a Vetorização

Os testes unitários validam as 14 dimensões contra os exemplos da [documentação oficial](https://github.com/zanfranceschi/rinha-de-backend-2026/blob/main/docs/en/DETECTION_RULES.md):

```bash
# Requer Go 1.23+ instalado localmente
go test ./internal/vectorize/... -v
```

Ou via Docker (sem instalar Go):

```bash
docker run --rm -v $(pwd):/src -w /src golang:1.23-alpine go test ./internal/vectorize/... -v
```

---

## Formato do Índice Binário (`data/index.bin`)

O arquivo é gerado no build e embutido na imagem final. Estrutura:

```
Header (24 bytes):
  [4B] magic: "RIDX"
  [4B] version: 1
  [4B] num_clusters: 256
  [4B] total_vectors: 3000000
  [4B] dims: 14
  [4B] scale: 127  (fator de quantização int8)

Centroides (256 × 14 × 4 bytes = 14.336 bytes):
  float32 row-major

Por cluster (256 blocos):
  [4B]          count  (número de vetores no cluster)
  [count × 14B] vetores int8 (AoS layout)
  [count × 1B]  labels  (0=legit, 1=fraud)

Tamanho total: ~43 MB
```

---

## Dimensões do Vetor

| Índice | Dimensão              | Fórmula                                                      |
|--------|-----------------------|--------------------------------------------------------------|
| 0      | `amount`              | `clamp(amount / 10000)`                                      |
| 1      | `installments`        | `clamp(installments / 12)`                                   |
| 2      | `amount_vs_avg`       | `clamp((amount / avg_amount) / 10)`                          |
| 3      | `hour_of_day`         | `hour(requested_at) / 23` (UTC)                              |
| 4      | `day_of_week`         | `weekday(requested_at) / 6` (seg=0, dom=6)                   |
| 5      | `minutes_since_last`  | `clamp(minutos / 1440)` ou `-1` se `last_transaction: null`  |
| 6      | `km_from_last_tx`     | `clamp(km / 1000)` ou `-1` se `last_transaction: null`       |
| 7      | `km_from_home`        | `clamp(km_from_home / 1000)`                                 |
| 8      | `tx_count_24h`        | `clamp(count / 20)`                                          |
| 9      | `is_online`           | `1` ou `0`                                                   |
| 10     | `card_present`        | `1` ou `0`                                                   |
| 11     | `unknown_merchant`    | `1` se merchant NÃO está em `known_merchants`, senão `0`     |
| 12     | `mcc_risk`            | lookup em `mcc_risk.json` (default `0.5`)                    |
| 13     | `merchant_avg_amount` | `clamp(merchant.avg_amount / 10000)`                         |

O valor sentinela `-1` nos índices 5 e 6 (quando não há histórico) é preservado na quantização como `int8(-127)`.

---

## Decisões de Design

**Por que IVF e não HNSW?**
O HNSW tem overhead de grafo de ~60–120 MB por instância. Com o orçamento de 160 MB por instância, não sobraria espaço. O IVF com quantização int8 ocupa apenas ~43 MB.

**Por que `scratch` → `alpine` na imagem final?**
A imagem `scratch` não tem shell, impossibilitando o healthcheck do Docker Compose. Usamos `alpine` (~5 MB a mais) para ter `wget` disponível.

**Por que `GOMAXPROCS=1`?**
Com 0.45 CPU por instância, o Docker throttle o container. Forçar 1 goroutine de OS evita overhead de scheduler e context-switching entre CPUs virtuais.

**Por que nunca retornar 5xx?**
Cada erro HTTP tem peso 5 na pontuação (vs. peso 3 para falso negativo e 1 para falso positivo). Em caso de falha de parse, retornamos `approved: true, fraud_score: 0.0`.

---

## Relação com o Repositório da Rinha

```
~/
├── rinha-de-backend-2026/    ← repositório oficial (fork/clone)
│   ├── docs/                 ← regras, arquitetura, avaliação
│   ├── resources/            ← references.json.gz, mcc_risk.json, normalization.json
│   ├── test/                 ← script k6 para testes de carga
│   └── participants/         ← onde você registra sua submissão
│
└── rinha-fraud-go/           ← este repositório (submissão)
    ├── Dockerfile
    ├── docker-compose.yml
    └── ...
```

Para submeter, siga as instruções em [`docs/en/SUBMISSION.md`](https://github.com/zanfranceschi/rinha-de-backend-2026/blob/main/docs/en/SUBMISSION.md) no repositório oficial.
