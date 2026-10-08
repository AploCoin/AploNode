# Independent Session Keys Review

## Current targeted review (2026-10-05)

**Outcome: no confirmed owner CREATE2 bypass.** The frozen full-node TxPool also fixes a confirmed fork-domain admission bug. This is a focused audit and does not establish a full-repository test pass or LES parity.

- Snapshot: `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118` on `master`, parent `2bc7d7af820b9706d242ad6240ce11ff18182742`.
- Production-source manifest: `docs/session-keys-production-manifest.txt`, SHA-256 **`b6ed703257c7124c86de6cdb273b48767e1d0b0c952af1637b65a5dbb383e3d0`**. I independently recomputed this over the manifest's 17 sorted paths as `path || NUL || file bytes || NUL`. It binds production Go sources, not tests or docs.
- Scope: owner/key/target CREATE2 lifecycle, EOA signer and delegated-code boundaries, and primary/alternate signer behavior in full-node pool admission.

| Review question | Source assessment |
|---|---|
| Owner and CREATE2 lifecycle | Registration requires a direct, zero-value call at depth zero where caller equals origin and has no code; the owner must also be unused. For an ordinary signed call, transaction precheck rejects nonce overflow and code-bearing senders, then increments the owner's nonce before the registry executes. A successful registration therefore leaves the owner nonce nonzero. CREATE and CREATE2 share `EVM.create`, whose common collision check rejects a destination with nonzero nonce before running init code. Any CREATE2 preimage that resolves to the owner consequently collides; this conclusion does not depend on the cost of finding a 160-bit preimage. It follows from the signed transaction nonce transition and EVM collision rule; no test claims to find an owner-address preimage. SELFDESTRUCT acts on the executing account and cannot bootstrap owner code. Constructor attempts to mutate the registry with owner as origin remain nested calls and fail the direct-caller/depth guard. |
| Key authority | The key must be nonreserved, distinct from owner, unused, and absent as an account. Active, revoked, and expired keys remain permanently marked used; CREATE/CREATE2 also treats used keys as collisions. The nonce/used-key collision controls seed state at a normally derived destination in synthetic fixtures. They verify the EVM guard, not an attack preimage or proof-backed registration at that destination. The real signed factory deployment, constructor, and target-replacement cases are separate tests. |
| Target authority and EVM identity | A target must be nonzero and cannot be the registry; contract targets remain allowed and authorization checks the exact top-level target and selector. A signed test deploys a whitelisted target with CREATE2, SELFDESTRUCTs it under the repository's legacy rules, and redeploys different code at the same address. The session remains authorized, the replacement sees `msg.sender=session` and `tx.origin=owner`, and ordinary nested execution remains intact. This address-stable target behavior follows from allowing contract targets; it does not enable owner-code installation. Applications that trust `tx.origin` alone still need their own protection. |
| EOA and delegated-code mechanisms | Native genesis requires EIP-155 at block zero. This transaction implementation supports legacy, access-list, and dynamic-fee transactions; its typed decoder has no authorization-list/delegated-code transaction path. Block import also rejects a sender with code. No EOA delegated-code mechanism that bypasses the owner restriction was found in this source tree. |
| Full-node TxPool domain | The prior `LatestSigner(chainconfig)` selected `ChainID_ALT` whenever an EthPoW fork was configured, even before its height, while block processing and session proofs use the active block signer. The frozen code derives `MakeSigner` for `head+1` during reset, purges transactions invalid under the new domain before switching account-lane recovery, updates local signer and fork flags, and rejects unprotected transactions at EthPoW. This was a pool admission/gossip mismatch, not a consensus import defect. LES parity was outside this audit and is not claimed. |
| Required execution semantics | Native genesis remains required. The session signs and pays its own nonce lane; top-level `msg.sender` remains the session and `tx.origin` remains the owner. Only top-level native-value payment is redirected. Nested CALL/DELEGATECALL semantics and contract targets remain ordinary EVM behavior. The proof replay domain is the configured chain ID, not a unique genesis identity. |

Fresh independent verification on the frozen source snapshot:

```text
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go test ./builtin/sessionkeys ./core ./params -run 'SessionKeys|RegistrySessionKeys' -count=1
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go test -race ./core -run 'TestSessionKeysPool' -count=1
PATH=/private/tmp/aplo-toolchain/go/bin:$PATH GOPATH=/private/tmp/aplo-go-work GOCACHE=/private/tmp/aplo-go-cache go build ./cmd/geth
```

**PASS** for all three commands. The focused tests cover signed constructor context, code-bearing owner rejection, allowed contract-target redeployment, CREATE2 collision controls, active-domain admission across the fork and reorg, pending/queued purge, unprotected transaction rejection, and concurrent ingestion/status/reset. `git diff --check -- docs/session-keys-review.md` also passes. No full-suite command was run for this refresh.

## Historical targeted review (ed45 snapshot, superseded)

The previous targeted section reviewed `ed45c62ec6c3cf4827f650378a5911a3d22a398a` with production manifest `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`. That review is historical: its manifest digest does not match the frozen `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118` source snapshot above, and its test results are not evidence for the current source.

## Historical result and verification (prior snapshots)

The broader commands below ran on `68515adb5023625f1238f09bd623debeccd23c52` with production manifest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`. The production source was later changed by a comment-only edit at `9b62907f381f1991bff2b87183635c047fd715a7`, producing manifest `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`, which matched the historical `ed45c62ec6c3cf4827f650378a5911a3d22a398a` manifest. These records are historical evidence, not reruns on the current snapshot.

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
