# Independent Session Keys Review

## Current targeted review (2026-10-05)

**Outcome: no confirmed Session Keys security defect in the reviewed native implementation.** The review covered the requested authorization and execution boundaries on the frozen snapshot below. This is a focused review; it does not establish a full-repository test pass.

- Snapshot: `ed45c62ec6c3cf4827f650378a5911a3d22a398a` on `master`, parent `83e121aeaab80bf358641b54946c7f196cba9899`.
- Production-source manifest: `docs/session-keys-production-manifest.txt`, SHA-256 **`75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`**. I recomputed the digest from its 17 sorted paths as `path || NUL || file bytes || NUL`; it matches the supplied value. The digest binds production Go sources, not tests or docs.

| Review question | Source assessment |
|---|---|
| Key representation and authorization | The session is an ordinary secp256k1 transaction signer with its own account nonce. Owner registration is a direct, zero-value EOA transaction; it also requires a low-S, 65-byte key-possession proof with v=0/1. The proof binds registry/version, effective chain ID, owner, key, target, ordered selectors, APLO and GAplo budgets, and expiry. EIP-155 signatures are required for session use and direct registry transactions. The replay domain is chain ID, not unique genesis identity. |
| EVM identity and nested execution | For the top-level target, the signed session remains `msg.sender` and the owner is `tx.origin`; only the top-level native-value payer changes. Nested CALL sees the calling contract as sender; DELEGATECALL keeps ordinary inherited caller context. Constructor execution retains ordinary EVM context. There is no identity rewrite in CALLCODE, DELEGATECALL, or STATICCALL paths. |
| Registry boundary and delegation authority | Registry create/revoke requires depth zero, immediate caller equal to origin, no caller code, an unused owner address, and zero value. Registry STATICCALL permits only `getSession`; CALLCODE and DELEGATECALL to the registry fail. Registry is rejected as owner, key, and target. `reserved` also rejects the 0x1–0x9 precompile range and protocol addresses 0x1234–0x1236 as owner/key. Contract targets remain allowed, with the protocol checking only the exact top-level target and selector. Applications that authorize solely by `tx.origin` can expose owner authority to an allowed session; the protocol cannot repair that application policy. |
| Consensus/import versus pool admission | Imported transactions are recovered by the block signer and processed through `applyTransaction`/`ApplyMessage`; protection, active delegation, nonce, target, selector, expiry, allowances, owner funds, and native state are checked in transaction execution. Txpool checks are admission/revalidation only. A precheck-invalid transaction invalidates its block and rolls back that transaction's state and GasPool changes. EVM execution reverts—including a rejected registry proof/configuration—may be included with a failed receipt, incremented signer nonce, and actual gas charge. |
| Lifecycle and resource bounds | Revoke removes authorization immediately; expiry is inclusive and cleanup follows transaction/reward processing. Permanent used-key ownership prevents reuse and continues routing later credits to the owner. APLO value and GAplo fees use separate remaining budgets; on an included execution revert, value transfers and APLO spending roll back while the actual GAplo fee remains charged to the owner and its gas budget. Existing caps bound selector count, owner sessions, expiry bucket size, and lifetime. |

Fresh focused verification on this exact commit:

```text
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go test ./builtin/sessionkeys ./core ./params -run 'SessionKeys|RegistrySessionKeys' -count=1
```

**PASS** for `builtin/sessionkeys`, `core`, and `params`. The selected tests include proof-of-possession and reserved-address checks; signed caller/origin and constructor-context checks; CALL/CALLCODE/DELEGATECALL/STATICCALL registry-context rejection; protected-signature import checks; direct processing, actual `InsertChain` reorg/restart, and registry lifecycle cases. The frozen production and test paths were unchanged during the run; `git diff --check` passed after this report edit. No full-suite command was rerun on this snapshot; the earlier broad-suite evidence below is historical.

## Historical result and verification (prior snapshots)

The broader commands below ran on `68515adb5023625f1238f09bd623debeccd23c52` with production manifest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`. The production source was later changed by a comment-only edit at `9b62907f381f1991bff2b87183635c047fd715a7`, producing manifest `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`, which matches the current `ed45c62ec6c3cf4827f650378a5911a3d22a398a` manifest. These records are historical evidence, not reruns on the current snapshot.

### Historical snapshot identity and scope

- Base commit: `0416fd355f53f0d09b256e194d4f44f9bb91663e`.
- Final source commit: `9b62907f381f1991bff2b87183635c047fd715a7` (`core: clarify native session reward qualification`).
- Final production-source manifest SHA-256: **`75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`**. I recomputed it over the 17 sorted paths in `docs/session-keys-production-manifest.txt`, hashing each `path || NUL || file bytes || NUL`.
- The digest identifies production Go sources only; it does not bind tests or docs. The verification commands below ran against `68515adb5023625f1238f09bd623debeccd23c52`, manifest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`. I compared every manifest file at both commits: the only source delta is a comment in `core/state_transition.go` clarifying that session mining rewards use the payer/owner stake. No executable bytes changed, so the test results apply to the final source behavior.
- I reviewed the 3-page design PDF as controlling over conflicting caller semantics in the 17-page context PDF, and read the current ADR/user docs, test additions, and production code paths for genesis, configuration, registry, EVM, StateDB, transition, consensus finalizers, txpool, miner, and RPC.

### Historical correctness assessment

The implementation matches the requested native execution model in the inspected paths. The transaction signer and nonce lane remain the session key; top-level `msg.sender` is the session and `tx.origin` is its owner. The owner funds native APLO value and GAplo gas fees through separate remaining budgets. The top-level call must match one target and an exact four-byte selector. Invalid prechecks use the transaction snapshot and restore gas-pool state; included reverts retain nonce and actual gas costs while rolling back value and budget changes.

Registry mutation requires a direct, zero-value owner EOA call; possession of the owner as `tx.origin` alone does not authorize nested calls. Registration requires a low-S proof from the new key binding the owner, effective chain ID, registry/version, target, ordered selectors, budgets, and expiry. The registry rejects absent/invalid chain-ID domains rather than silently using zero. Used-key owner tombstones remain after revoke/expiry and route later native credits to the owner. Owner and expiry packed indexes are journaled in StateDB; expiry is inclusive and cleanup follows transaction and reward processing. The inspected bounds remain finite for selectors, owner sessions, per-height expiry entries, and lifetime.

Genesis setup normalizes registry nonce one and canonical GAplo through the same allocation path for state-root derivation and persistence, without mutating the caller's map. Reserved-address conflicts are rejected. Startup validates persisted native state read-only, and recovery checks the stored allocation against the original header state root before writing it. The developer faucet rejects reserved/precompile addresses and is separately funded in native APLO and GAplo. Configuration and signer-domain checks make EIP-155 active at genesis, constrain signing domains to uint256, and lock primary/alternate chain IDs at the relevant boundary. GAplo recipient rewriting preserves malformed ABI address words so the canonical contract decoder rejects them. Session Keys no longer have an activation height or fork-ID entry.

I found no production-path issue in the reviewed pool reservation/replacement logic, Beacon finalization ordering, incoming APLO/GAplo routing, or the real-chain branch/restart scenario. The target/selector rule remains top-level only: a whitelisted contract may use its normal nested calls or delegatecalls and observes the owner as `tx.origin`. Applications must not use `tx.origin` alone as their owner check. The proof replay domain is chain ID, not genesis identity; networks intentionally sharing an ID also share proof replay exposure.

### Historical verification performed

Commands used Go 1.20.14 at `/private/tmp/aplo-toolchain/go/bin/go`, `GOPATH=/private/tmp/aplo-go-work`, and `GOCACHE=/private/tmp/aplo-go-cache`.

- `go test ./builtin/... ./core ./params ./internal/ethapi ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner -run 'SessionKeys' -count=1` — **PASS**. Session Keys cases passed in the registry, core, params, and RPC packages; consensus/miner packages compiled and had no matching named tests.
- `go test ./core -run 'TestSessionKeysNativeGenesis|TestSessionKeysSetupRejectsLegacyGenesisWithoutMigration|TestSessionKeysCommitGenesisState|TestDeveloperGenesisRejectsReservedFaucetAddresses|TestGenesis|TestRestartWithNewSnapshot|TestShortRepair' -count=1` — **PASS**, including non-mutating legacy rejection, root-verified recovery, conflicting allocation rejection, EIP-155 genesis requirements, faucet collision cases, supported native genesis identities, and normalized repair/snapshot fixtures.
- `go test ./params/... -count=1` — **PASS**.
- `go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params -run 'SessionKeys' -count=1` — **PASS**.
- `go test ./console -count=1` — **PASS**.
- `go build ./cmd/geth ./examples/sessionkeys` — **PASS**.
- `git diff --check` — **PASS**. `gofmt -l` over changed production and test Go files — no output.
- I independently recomputed the final production manifest digest; it is `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`.

### Historical failures and compatibility boundaries

`go test ./core -count=1` remains **not green**. After the genesis and snapshot fixtures were updated, my full-package run failed at `TestFastVsFullChains` with an insufficient-funds panic. I ran the same named test on `/private/tmp/aplo-baseline`; it fails at the same `AddTxWithChain` path for the same sender with the same insufficient-funds condition. This comparison does not identify a Session Keys regression. The focused genesis/snapshot command above passes on the updated tree.

No full-repository pass was established in this independent review. The review conclusion is limited to the listed source snapshot, verification results, and compatibility boundaries.

The permanent-genesis architecture intentionally has no migration for an old database whose genesis lacks native registry state. Startup rejects it without writing or deleting data; Genesis-state recovery rejects legacy allocations and root mismatches. The implementation therefore does not preserve compatibility with an existing chain's old genesis identity. The current ADR/docs narrow use to a freshly initialized disposable devnet and instruct operators to retain old data; any broader network rollout would require a separately reviewed migration/network plan.
