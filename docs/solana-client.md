# Solana x402 Client Integration

This document describes the Solana payment path for `PAYMENT_CHAIN=solana`.

The server keeps the same two-step x402 flow:

1. Client calls an LLM endpoint without `X-PAYMENT`.
2. Server calls the upstream model, prices the completed response, caches it, and returns `402 Payment Required`.
3. Client signs a Solana SPL Token `transferChecked` transaction.
4. Client retries with `X-PAYMENT` and `X-PAYMENT-REQUEST-ID`.
5. Server validates, broadcasts, waits for confirmation, records payment, and returns the cached LLM response.

Streaming is still unsupported because the server needs final token usage before pricing.

## Server Configuration

```env
PAYMENT_CHAIN=solana
SOLANA_CLUSTER=devnet
SOLANA_CONFIRMATION=confirmed
PAY_TO_ADDRESS=MerchantWalletPubkey
USDC_ADDRESS=USDCMintPubkey
RPC_URL=https://api.devnet.solana.com
```

`PAY_TO_ADDRESS` is the merchant wallet address, not the token account. The server derives the merchant USDC associated token account and returns it in the 402 challenge as `extra.payToTokenAccount`.

## 402 Challenge

The first request returns:

```json
{
  "x402Version": 1,
  "accepts": [
    {
      "scheme": "exact",
      "network": "solana:devnet",
      "maxAmountRequired": "12500",
      "resource": "/v1/chat/completions",
      "description": "model: 100 prompt + 200 completion tokens",
      "payTo": "MerchantWalletPubkey",
      "maxTimeoutSeconds": 120,
      "asset": "USDCMintPubkey",
      "extra": {
        "requestId": "req_xxx",
        "decimals": "6",
        "tokenProgram": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
        "payToTokenAccount": "MerchantUSDCAssociatedTokenAccount",
        "settlement": "signed_transaction",
        "memo": "x402:req_xxx",
        "confirmation": "confirmed",
        "addressLookupTables": "unsupported",
        "clientSubmitSupported": "false"
      }
    }
  ]
}
```

Response headers:

```http
X-PAYMENT-REQUEST-ID: req_xxx
X-Cost: 0.012500
```

`maxAmountRequired` is in USDC base units with 6 decimals.

## Required Transaction

The client must build a signed legacy Solana transaction with:

- One Memo instruction with exact text from `accepts[0].extra.memo`, for example `x402:req_xxx`.
- One SPL Token `transferChecked` instruction.
- `transferChecked.mint == accepts[0].asset`.
- `transferChecked.destination == accepts[0].extra.payToTokenAccount`.
- `transferChecked.amount == accepts[0].maxAmountRequired`.
- `transferChecked.decimals == 6`.
- `transferChecked.owner` must sign the transaction.
- Address lookup tables are not supported.

The server allows ComputeBudget instructions. Other programs are rejected.

## JavaScript Client Sketch

```ts
import {
  Connection,
  PublicKey,
  Transaction,
  TransactionInstruction
} from "@solana/web3.js";
import {
  TOKEN_PROGRAM_ID,
  createTransferCheckedInstruction,
  getAssociatedTokenAddress
} from "@solana/spl-token";

async function buildPaymentHeader({
  connection,
  payer,
  signTransaction,
  requirement
}: {
  connection: Connection;
  payer: PublicKey;
  signTransaction: (tx: Transaction) => Promise<Transaction>;
  requirement: any;
}) {
  const mint = new PublicKey(requirement.asset);
  const destination = new PublicKey(requirement.extra.payToTokenAccount);
  const source = await getAssociatedTokenAddress(mint, payer);
  const amount = BigInt(requirement.maxAmountRequired);

  const tx = new Transaction();
  tx.feePayer = payer;
  tx.recentBlockhash = (await connection.getLatestBlockhash()).blockhash;
  tx.add(
    new TransactionInstruction({
      programId: new PublicKey("MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"),
      keys: [],
      data: Buffer.from(requirement.extra.memo, "utf8")
    }),
    createTransferCheckedInstruction(
      source,
      mint,
      destination,
      payer,
      amount,
      Number(requirement.extra.decimals),
      [],
      TOKEN_PROGRAM_ID
    )
  );

  const signed = await signTransaction(tx);
  const payment = {
    x402Version: 1,
    scheme: "exact",
    network: requirement.network,
    payload: {
      type: "signed_transaction",
      transaction: Buffer.from(signed.serialize()).toString("base64"),
      requestId: requirement.extra.requestId
    }
  };

  return Buffer.from(JSON.stringify(payment), "utf8").toString("base64");
}
```

Retry the original LLM request with:

```http
X-PAYMENT-REQUEST-ID: req_xxx
X-PAYMENT: base64_json_payment
```

## Payment Response

On success, the LLM response is returned with:

```http
X-PAYMENT-RESPONSE: base64_json
X-Tx: solana_transaction_signature
X-Cost: 0.012500
```

Decoded `X-PAYMENT-RESPONSE`:

```json
{
  "success": true,
  "transaction": "solana_transaction_signature",
  "network": "solana:devnet",
  "payer": "PayerWalletPubkey"
}
```

## Common Errors

- `destination token account mismatch`: use `extra.payToTokenAccount`, not `payTo`.
- `missing required memo`: memo text must exactly equal `extra.memo`.
- `address lookup tables are not supported`: build a legacy transaction.
- `unsupported Solana program`: remove unrelated instructions; only Memo, ComputeBudget, and SPL Token `transferChecked` are accepted.
