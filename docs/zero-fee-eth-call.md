# Zero-fee eth_call regression (2026-10-18)

This historical record describes RPC-only PR #12 / `0af90c73d`. The later,
separate [ordinary consensus restoration candidate](ordinary-consensus-restoration.md)
does not change the scope of that PR.

## Trigger and fix

After PR #10, an ordinary `eth_call` with no `from` or an explicit zero
sender failed before executing the target: `err: execution reverted (supplied
gas 50000000)`. This affected both GAplo (`0x1234`) and native APLO (`0x1235`)
`balanceOf(address)` reads. Omitted gas is not the cause.

`TransactionArgs.ToMessage` defaults the sender to zero and the fee fields to
zero. `buyGas` nevertheless called canonical GAplo `takeFee(0)`, whose ERC20
burn implementation rejects the zero sender. PR #10 correctly began returning
fee-debit errors; the old implementation ignored them. An independent direct
VM probe confirmed that zero-sender `balanceOf` succeeds while `takeFee(0)`
reverts with `ERC20: burn from the zero address`.

`buyGas` now skips only that empty burn when `IsFake()` is true and the
purchased fee (`gas * effective gas price`) is zero. Balance/reservation checks,
gas-pool limits, native value checks and all session validation still execute.
Nonzero simulated fees and every real transaction retain the fee-contract call
and its errors. This does not reinstate blanket error swallowing.

The deeper pre/session/fix comparison and wallet metadata analysis are in
[APLO / GAplo model audit](asset-model-audit.md). Signed-consensus differences
introduced by #10 are recorded [separately](decisions/session-fee-consensus.md);
the user approved keeping #12 RPC-only.

## Consensus assessment

The only executable production change is the guard around `subGaplo` in
`core/state_transition.go`. `Transaction.AsMessage` always sets `isFake: false`,
so signed block processing, block assembly, real fee burning/refunds and session
budget accounting take exactly the prior branch, even for zero-priced real
messages. RPC `ToMessage` sets `isFake: true` and simulates on copied state.
Relative to the merged post-session base, there is no bytecode, genesis, storage-layout, activation-height or signing-domain
change and no chain-data migration. No consensus change is intended for real
transactions. The distinction is explicitly tested with both zero and nonzero
fees, including a funded zero payer that must still return the burn error.

## Fresh checks

Base: clean upstream master `4d3d3f8c63fb314ec23d7369f687033cdf26ac93`.
Go: 1.20.14. The focused regression fails on a temporary archive of that base
with the reported error for omitted/zero senders and passes with this fix.

```sh
go test ./core ./internal/ethapi -run ZeroFee -count=1
go test ./builtin/... ./core ./params ./internal/ethapi \
  ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner \
  -run 'SessionKeys|RegistrySessionKeys|ZeroFee' -count=1
go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params \
  -run 'SessionKeys|RegistrySessionKeys|ZeroFee' -count=1
go test ./internal/ethapi ./params -count=1
go build ./cmd/geth
go build ./examples/sessionkeys-smoke
```

These checks pass. Consensus/miner packages in the focused sweep compile but
have no matching tests; the core session suite covers signed execution,
insufficient owner funds, nonce, expiry, whitelist, accounting, consensus
finalizer parity, actual chain import, reorg and restart. The final RPC tests
also cover `estimateGas`, zero-fee session target/selector/value/expiry denial,
and compare the whole live StateDB root after each simulation or rejection.
Their copied-state backend does not supply distinct historical states; the
native HTTP smoke below verifies actual block selection.

The full repository suite is not claimed green. A fresh isolated
`go test ./core -run '^TestTransactionIndices$' -count=1` fails identically on the
base and fix: its ordinary EOA fixture has zero GAplo and needs 18375000000000.
Both panic at `core/chain_makers.go:108`. Existing broader limitations remain in
[the historical session test record](session-keys-testing.md).

## Reproducible native smoke

Use a fresh disposable localhost `--dev` chain only. This sends synthetic local
transactions. The smoke requires chain ID 1337, one unlocked faucet and no prior
owner stake. Session keys are generated in process memory and never printed or
persisted. It uses direct HTTP JSON-RPC and the unlocked synthetic faucet,
without a browser or actual wallet extension.

```sh
go build -o /tmp/aplo-geth-fixed ./cmd/geth
go build -o /tmp/aplo-session-smoke ./examples/sessionkeys-smoke
# In a separate terminal; keep the generated datadir if inspecting evidence.
/tmp/aplo-geth-fixed --dev --dev.period 1 \
  --datadir "$(mktemp -d /tmp/aplo-call-smoke-XXXXXX)" \
  --http --http.addr 127.0.0.1 --http.port 19647 \
  --http.api eth,net,web3 --http.vhosts localhost,127.0.0.1 \
  --ipcdisable --authrpc.port 19653 --maxpeers 0 --nodiscover
/tmp/aplo-session-smoke -rpc http://127.0.0.1:19647 -out /tmp/native-session.json
```

The accepted run is recorded in
[zero-fee-native-smoke.json](verification/zero-fee-native-smoke.json):

- Register a proof-backed session admitting only `mine(bytes32)` on GAplo;
  owner stakes 1000 APLO; the session signs and submits an actual mining share.
- Execute 64 `balanceOf` calls across both tokens and two queried accounts
  (funded faucet and the reported account), with omitted, zero, unfunded
  nonzero and faucet senders, at genesis, the stake block, latest and pending.
  Every result matches its nonzero-sender control; no gas field is supplied.
- Compare live owner/session balances, GAplo supply, owner/session nonces and
  both session budgets before/after calls; reject a forbidden zero-fee session
  read without changing these values.
- Verify mining receipt status 1, session nonce 1, actual GAplo budget debit
  exactly `gasUsed * gasPrice`, unchanged native allowance, one new share,
  owner mining reward and no session GAplo refund. Recover the Clique author
  from its seal: the header coinbase field itself is zero. The faucet receives
  the tip but pays the London base-fee burn, so its net GAplo delta is the
  stake-scaled reward minus `gasUsed * baseFee`, which is checked exactly.
- Reject an ordinary signed transaction without GAplo, session replay and
  wrong target; owner revokes, the registry returns an empty record, and a
  freshly signed post-revoke transaction is rejected.

No public-chain transactions, deployment or merge were performed. The reported
remote node and its particular historical block `0x210` were not accessed;
the original calldata and failure were reproduced locally. Real wallet
extension behavior and WebMiner UI integration require their own validation.
