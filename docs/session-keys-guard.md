# Session Keys independent security and consensus guard

**Verdict: PASS for the frozen implementation source manifest below.** This is a source-level protocol guard, not authorization to activate or deploy the fork. The parent requested the final guard verdict after the production freeze. No production source was edited by this reviewer.

## Reviewed snapshot

- Git base/HEAD: `0416fd355f53f0d09b256e194d4f44f9bb91663e` (the implementation remains an uncommitted working-tree diff).
- Production-source manifest SHA-256: `b122130481661ff7a6be1058eaca61281ff28b116593181b1c06cb2271c9302c`.
- Hash procedure: for each sorted path in `/private/tmp/aplo-session-production-manifest.txt`, hash `path || NUL || file bytes || NUL` with SHA-256. I independently recomputed the value above. The manifest contains only changed/new non-test Go sources; tests and docs are outside this hash.
- `git diff --check`: passed.

The reviewed production manifest covers `builtin/aplo/aplo.go`, `builtin/sessionkeys/registry.go`, the Clique/Ethash/Beacon finalizers, `core/genesis.go`, state DB/processor/transition/tx-list/tx-pool/EVM paths, `examples/sessionkeys/main.go`, `internal/ethapi/api.go`, `miner/worker.go`, `params/config.go`, and `params/legacy_genesis_hashes.go`.

## Guard findings

I found no remaining critical consensus or security defect in this frozen source snapshot. The checked invariants are:

- The session key remains the transaction signer and nonce account and the top-level `msg.sender`; the owner is `tx.origin` and the payer for top-level native value and GAplo fees. Nested CALLs retain ordinary caller and payer behavior. Registry mutation checks the immediate direct EOA caller against origin, rejects nested/CALLCODE/DELEGATECALL mutation, and cannot be authorized merely because a session transaction has the owner as origin.
- Registration requires a low-S secp256k1 proof of possession from the proposed key, bound to owner, effective chain ID, registry/version, target, ordered selectors, both allowances, and expiry. This closes fresh-address squatting. The selected authorization is one nonzero target and 1–32 unique exact four-byte selectors. Used keys cannot be reused, originate ordinary transactions, become owners, or be recreated through contract creation; owner/session equality and delegation chains are rejected.
- Registry state is journaled at the reserved `0x1237` address. Owner and absolute-expiry lists use indexed swap removal; live keys are capped at 64 per owner and 32 per expiry bucket. Expiry is inclusive through the last authorized block, bounded to 1000 blocks with checked `uint64` arithmetic, and cleaned after block transactions/rewards. Permanent tombstones preserve recipient routing after revoke/expiry. Snapshot/revert, state-copy, commit/reopen, canonical reorg, and restart behavior are covered.
- Session admission reserves `gasLimit * feeCap` against owner GAplo balance and the remaining GAplo allowance. Actual settlement charges used gas at effective gas price; refunds return unused gas. APLO value and explicit APLO builtin transfers use the independent native allowance and owner balance. A VM revert/OOG rolls back value and allowance changes while retaining the included transaction's nonce and actual gas charge; invalid prechecks revert StateDB and GasPool changes.
- Native credits route through StateDB.AddBalance, including internal CALL, SELFDESTRUCT, and consensus credits. For the exact canonical GAplo runtime, the supported `transfer`, `transferFrom`, and root `refund` recipient words are rewritten to the permanent owner. Arbitrary ERC20/NFT contract storage is not redirected. Incoming credits and mining/reward flows do not refill session allowances.
- Activation is opt-in (`sessionKeysBlock` is nil by default), requires a protected chain-ID domain and a uint64 fork height at/after EIP-155, and participates in fork-ID calculation and config compatibility. Genesis/fork-boundary guards, block import, miner finalization, txpool admission/revalidation, and nonce RPC paths were reviewed. The fork-off execution paths remain gated.

The 3-page design PDF controls the conflicting caller semantics: top-level `msg.sender=session`, `tx.origin=owner`. The 17-page context PDF says owner should be `msg.sender`; that conflict is resolved explicitly in the design docs and in code, consistent with the user-provided target. The longer document's wider permission/token model is outside this narrowed implementation.

## Verification evidence

Using Go 1.20.14 and the task-local caches:

```sh
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go test -race ./builtin/sessionkeys ./core -run 'SessionKeys' -count=1
```

Passed for both packages. This includes the Session Keys protocol/security tests and the actual `BlockChain.InsertChain` reorg/restart test.

```sh
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go test ./core -run '^TestSessionKeysInsertChainReorgAndRestart$' -count=1
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go test ./params -count=1
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go build -o /private/tmp/aplo-geth-guard ./cmd/geth
```

All three passed. The build emitted only platform dependency deprecation warnings. `git diff --check` also passed. The root task separately reports its broader focused Session Keys sweep, fork-ID boundary test, signing/init checks, and EOA baseline comparisons as passing/complete.

Seven existing ordinary transaction-pool tests remain red on both the baseline archive and this snapshot after test-only compile-fixture repairs; comparison logs are `/private/tmp/aplo-pool-{baseline,current}-<TestName>.log`. The identical failure signatures are:

- `TestTransactionQueueTimeLimiting`, `TestTransactionQueueTimeLimitingNoLocals`, `TestTransactionPendingLimiting`, and `TestTransactionReplacement`: existing fixture insufficient-funds failures.
- `TestTransactionGapFilling`: pending count `0`, expected `1`.
- `TestStateChangeDuringTransactionPoolReset`: nonce `0`, expected `2`.
- `TestTransactionMissingNonce`: the same insufficient-funds path and panic on both trees.

I inspected the paired logs; elapsed times differ as expected, while the failing assertions/messages match. These are pre-existing suite failures, not evidence of a Session Keys regression. They remain a limitation of the repository's broad EOA test gate, so the verdict does not claim the whole repository suite is green.

## Explicit boundaries and deployment prerequisites

- No production activation height or live chain state was selected or validated. At the chosen height, the parent state must have no code or storage at `0x1237` and must contain the exact canonical `params.GAPLO` runtime at `0x1234`. The implementation fails closed on conflicts; it does not migrate or overwrite an existing registry or a noncanonical GAplo deployment. Governance/operators must establish these facts before scheduling activation.
- The proof replay domain is the effective chain ID, switching to `ChainID_ALT` after the existing EthPoW fork. It is not a unique genesis/network identifier. Networks deliberately sharing a chain ID retain the same replay exposure as EIP-155 transactions; add a configured genesis/network salt if the product requires clone-network separation.
- Recipient rewriting is intentionally limited to native APLO and the enumerated canonical GAplo recipient paths. Other token balances remain at their original addresses and require application-level migration or support.
- The refreshed `docs/session-keys-review.md` and `docs/session-keys-testing.md` both identify the same `b122…` production source manifest and record the later Beacon finalizer fix. They are aligned with this final review; prior interim notes are superseded.

Within these boundaries, the frozen implementation meets the reviewed security and consensus invariants. The deployment prerequisites are deliberate and visible failure conditions, not silent migrations.
