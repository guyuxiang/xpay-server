# xPay server

xPay server 是一个面向 AI API 的 x402 支付网关。它对外提供 OpenAI/Anthropic 兼容接口，先代理请求到上游模型服务，根据真实 token usage 计算本次调用费用，再通过 x402 exact EVM 方案收取 USDC。支付完成后，服务返回已经生成好的模型响应。

本项目适合放在 `new-api`、OpenAI 兼容网关或其他 LLM 上游服务前面，为模型调用增加按次链上支付能力。

## 核心能力

- OpenAI 兼容接口代理：
  - `POST /v1/chat/completions`
  - `POST /v1/responses`
  - `POST /v1/messages`
  - `POST /v1/completions`
- x402 `exact` EVM 支付流程；可通过 `PAYMENT_CHAIN=solana` 切换到 Solana SPL Token 支付。
- USDC EIP-3009 `transferWithAuthorization` 链上结算。
- 支持 Base Mainnet 和 Base Sepolia 的默认 USDC/RPC 配置。
- 根据模型返回的 token usage 自动计费。
- 模型价格表存储在 SQLite，可通过 Web 管理后台修改，保存后立即生效。
- 管理后台：
  - 概览看板
  - 模型价格维护
  - `MARKUP` 加价倍率配置
  - 最近支付记录
- SQLite 持久化支付流水、模型价格和运行时配置。
- Docker 多阶段构建，运行阶段使用非 root 用户。

## 工作流程

1. 客户端第一次请求 `/v1/...` 接口，不携带 `X-PAYMENT`。
2. 服务使用 `UPSTREAM_TOKEN` 请求 `UPSTREAM_URL` 上游模型服务。
3. 上游返回成功后，服务读取 `usage`，按模型价格和 `MARKUP` 计算 USDC 金额。
4. 服务缓存上游响应，并返回 `402 Payment Required`，其中包含 x402 付款要求。
5. 客户端按 x402 要求签名 USDC 授权，并携带：
   - `X-PAYMENT`
   - `X-PAYMENT-REQUEST-ID`
6. 服务校验签名、金额、收款地址、网络和授权有效期。
7. 服务提交 USDC `transferWithAuthorization` 交易。
8. 结算成功后，服务记录支付流水并返回缓存的模型响应。

注意：当前不支持 streaming。服务必须等上游完整响应后才能知道 token usage 并计算价格，请在客户端设置 `stream=false`。

## 目录结构

```text
cmd/server/              服务入口
cmd/testpay/             本地端到端测试客户端示例
internal/cache/          待支付响应缓存
internal/config/         环境变量配置加载与校验
internal/handler/        HTTP handler、relay、info、admin
internal/pricing/        模型价格表和计费逻辑
internal/settler/        EIP-712 验签和 USDC 链上结算
internal/store/          SQLite 存储、迁移、支付记录、价格和设置
internal/types/          x402 wire format 类型
Dockerfile               Docker 镜像构建文件
.env.example             环境变量模板
```

## 环境变量

复制模板：

```bash
cp .env.example .env
```

生产环境至少需要配置：

| 变量 | 必填 | 示例 | 说明 |
| --- | --- | --- | --- |
| `PORT` | 否 | `3402` | HTTP 服务端口 |
| `BASE_URL` | 否 | `/payapi` | URL 路径前缀；如果通过 `https://www.openshort.cloud/payapi` 访问服务，请设置为 `/payapi` |
| `UPSTREAM_URL` | 是 | `https://your-new-api.example.com` | 上游 OpenAI 兼容服务地址，不要带末尾 `/` |
| `UPSTREAM_TOKEN` | 是 | `sk-...` | 服务端访问上游模型服务的系统 token |
| `PAYMENT_CHAIN` | 否 | `evm` | 支付链类型：`evm` 或 `solana` |
| `PAY_TO_ADDRESS` | 是 | `0x...` / Solana pubkey | 收取 USDC 的地址；Solana 模式填写商户钱包地址，服务端自动派生商户 USDC ATA |
| `GAS_PRIVATE_KEY` | EVM 生产必填 | `0x...` | EVM 代付 gas 私钥；Solana 模式不需要；`DRY_RUN=true` 时可不填 |
| `CHAIN_ID` | EVM 否 | `84532` | EVM 链 ID，`84532` 为 Base Sepolia，`8453` 为 Base Mainnet |
| `SOLANA_CLUSTER` | Solana 否 | `devnet` | Solana 网络：`devnet` 或 `mainnet-beta` |
| `SOLANA_CONFIRMATION` | Solana 否 | `confirmed` | 服务端广播交易后等待的确认级别：`confirmed` 或 `finalized` |
| `USDC_ADDRESS` | EVM 否 / Solana 是 | `0x...` / mint pubkey | EVM USDC 合约地址；Solana 模式填写 USDC mint 地址 |
| `RPC_URL` | 否 | `https://...` | EVM 或 Solana RPC 地址；支持链会自动使用默认 RPC |
| `DB_PATH` | 否 | `./xpay.db` | SQLite 数据库路径 |
| `ADMIN_TOKEN` | 生产必填 | `change-this-admin-token` | 管理 API 鉴权 token |
| `CACHE_TTL_SECS` | 否 | `300` | 待支付响应缓存时间 |
| `SIG_TIMEOUT_SECS` | 否 | `120` | 返回给客户端的签名有效期 |
| `UPSTREAM_TIMEOUT_SECS` | 否 | `300` | 上游模型请求超时时间 |
| `SHUTDOWN_TIMEOUT_SECS` | 否 | `10` | 优雅关闭超时时间 |
| `MAX_BODY_BYTES` | 否 | `4194304` | 最大请求体大小 |
| `MARKUP` | 否 | `1.0` | 加价倍率，可在管理后台修改 |
| `ALLOWED_ORIGINS` | 否 | `https://app.example.com` | CORS 白名单，逗号分隔；生产不要使用 `*` |
| `DRY_RUN` | 否 | `false` | `true` 时不提交链上交易，仅用于开发测试 |

Solana 客户端对接见 [docs/solana-client.md](docs/solana-client.md)。

## 本地运行

```bash
go run -buildvcs=false ./cmd/server
```

健康检查：

```bash
curl http://127.0.0.1:3402/health
```

返回：

```json
{"ok":true}
```

管理后台：

```text
http://127.0.0.1:3402/admin
```

如果服务部署在路径前缀下，例如 `BASE_URL=/payapi`，则管理后台地址为：

```text
https://www.openshort.cloud/payapi/admin
```

如果设置了 `ADMIN_TOKEN`，页面调用 `/admin/api/*` 时会要求输入 token。API 也支持：

```http
X-Admin-Token: your-admin-token
```

或：

```http
Authorization: Bearer your-admin-token
```

## Docker 部署

### 1. 构建镜像

```bash
docker build -t xpay-server:latest .
```

### 2. 准备数据目录

SQLite 数据库需要持久化，建议挂载到宿主机目录：

```bash
mkdir -p ./data
```

### 3. 使用 `.env` 启动

确保 `.env` 已正确配置，然后运行：

```bash
docker run -d \
  --name xpay-server \
  --restart unless-stopped \
  --env-file .env \
  -p 3402:3402 \
  -v "$(pwd)/data:/data" \
  -e DB_PATH=/data/xpay.db \
  xpay-server:latest
```

查看日志：

```bash
docker logs -f xpay-server
```

停止：

```bash
docker stop xpay-server
```

升级镜像后重启：

```bash
docker rm -f xpay-server
docker build -t xpay-server:latest .
docker run -d \
  --name xpay-server \
  --restart unless-stopped \
  --env-file .env \
  -p 3402:3402 \
  -v "$(pwd)/data:/data" \
  -e DB_PATH=/data/xpay.db \
  xpay-server:latest
```

### 4. docker compose 示例

创建 `docker-compose.yml`：

```yaml
services:
  xpay-server:
    image: xpay-server:latest
    build:
      context: .
      dockerfile: Dockerfile
    container_name: xpay-server
    restart: unless-stopped
    env_file:
      - .env
    environment:
      DB_PATH: /data/xpay.db
    ports:
      - "3402:3402"
    volumes:
      - ./data:/data
```

启动：

```bash
docker compose up -d --build
```

查看日志：

```bash
docker compose logs -f
```

停止：

```bash
docker compose down
```

## 管理后台

访问：

```text
http://your-server:3402/admin
```

功能：

- 查看总收入、支付笔数、付款地址数、token 累计。
- 查看链、收款地址、USDC 合约、上游地址等运行信息。
- 修改 `MARKUP` 加价倍率。
- 新增、编辑、删除模型价格。
- 恢复内置默认模型价格。
- 查看最近支付记录。

模型价格单位是：

```text
USD / 1M tokens
```

每个模型可配置：

- `input`：普通输入 token 价格。
- `cached_input`：缓存命中输入 token 价格；为空时按 `input` 价格计费。
- `output`：输出 token 价格。

服务会读取上游 `usage`：

- OpenAI：`prompt_tokens`、`completion_tokens`，以及 `prompt_tokens_details.cached_tokens`。
- Anthropic：`input_tokens`、`output_tokens`，以及 `cache_read_input_tokens`。

计费公式：

```text
uncached_input = input_tokens - cached_input_tokens
amount = ceil((uncached_input * input_price + cached_input_tokens * cached_input_price + output_tokens * output_price) / 1_000_000)
amount = ceil(amount * MARKUP)
```

模型匹配规则：

- 优先精确匹配。
- 如果没有精确匹配，使用最长前缀匹配。

例如配置：

```text
claude-sonnet-4
```

可以匹配：

```text
claude-sonnet-4-5-20250929
```

价格保存到 SQLite 的 `model_prices` 表中，保存后立即更新内存价格表，不需要重启服务。

## API 说明

### 支付保护的模型接口

```text
POST /v1/chat/completions
POST /v1/responses
POST /v1/messages
POST /v1/completions
```

这些接口会转发到：

```text
${UPSTREAM_URL}${原始路径}
```

### 信息接口

```text
GET /health
GET /v1/info/address
GET /v1/info/balance?address=0x...
```

`/v1/info/address` 返回收款地址、链、USDC 合约和 x402 scheme。

`/v1/info/balance` 返回某个付款地址的历史消费记录。

### 管理接口

```text
GET    /admin/api/overview
PUT    /admin/api/settings
POST   /admin/api/prices
DELETE /admin/api/prices/:model
POST   /admin/api/prices/reset-defaults
```

生产环境必须设置 `ADMIN_TOKEN` 保护这些接口。

## 数据库

默认 SQLite 文件：

```text
./xpay.db
```

Docker 部署建议使用：

```text
/data/xpay.db
```

主要表：

- `payments`：已完成支付记录。
- `settings`：运行时配置，例如 `markup`。
- `model_prices`：模型价格表。

备份：

```bash
cp ./data/xpay.db ./data/xpay.db.bak.$(date +%Y%m%d%H%M%S)
```

恢复时停止容器，替换数据库文件，再启动容器。

## 安全建议

- 不要提交 `.env`。
- `ADMIN_TOKEN` 必须使用高强度随机值。
- 生产环境不要使用 `ALLOWED_ORIGINS=*`。
- `GAS_PRIVATE_KEY` 只应放在服务器或容器环境变量中。
- 建议通过 Nginx/Caddy/Traefik 提供 HTTPS。
- 建议限制 `/admin` 访问来源，例如内网、VPN、反向代理 basic auth 或 IP 白名单。
- `PAY_TO_ADDRESS` 和 `CHAIN_ID` 上线前必须确认无误。
- 主网部署前先在 Base Sepolia 使用小额测试。

## 验证与测试

运行单元测试：

```bash
GOCACHE=/tmp/gocache go test ./...
```

静态检查：

```bash
GOCACHE=/tmp/gocache go vet ./...
```

构建：

```bash
GOCACHE=/tmp/gocache go build -buildvcs=false -o bin/xpay-server ./cmd/server
```

## 常见问题

### 1. `/admin` 可以打开，但管理 API 返回 401

说明设置了 `ADMIN_TOKEN`。页面会弹窗要求输入 token，也可以用请求头：

```http
X-Admin-Token: your-admin-token
```

### 2. 返回 `x402: could not determine token usage`

上游响应里没有 token usage，常见原因是开启了 streaming。请设置：

```json
{"stream": false}
```

### 3. 结算失败

检查：

- `GAS_PRIVATE_KEY` 是否有效。
- gas 钱包是否有链上原生币支付 gas。
- `RPC_URL` 是否可用。
- `CHAIN_ID`、`USDC_ADDRESS` 是否匹配。
- 用户签名的 `payTo`、`value`、`network` 是否与服务返回的 402 要求一致。

### 4. Docker 重启后价格或支付记录丢失

说明没有持久化 SQLite 数据库。请挂载数据目录，并设置：

```bash
-v "$(pwd)/data:/data" -e DB_PATH=/data/xpay.db
```

### 5. 端口被占用

查看占用进程：

```bash
ss -ltnp | grep 3402
```

换端口可修改：

```env
PORT=3403
```

并同步调整 Docker 端口映射：

```bash
-p 3403:3403
```
