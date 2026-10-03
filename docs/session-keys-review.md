# Independent Session Keys Review

## Result

**No unresolved Session Keys consensus or security defect found in the reviewed production snapshot.** The review is conditional for repository acceptance: the Session Keys-specific and updated native-genesis checks pass, but the ordinary `core` regression suite is not green. Treat this as a review of the production manifest below, not as certification of the full client suite or an existing-network upgrade.

## Snapshot identity and scope

- Base commit: `0416fd355f53f0d09b256e194d4f44f9bb91663e`.
- Final source commit: `9b62907f381f1991bff2b87183635c047fd715a7` (`core: clarify native session reward qualification`).
- Final production-source manifest SHA-256: **`75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`**. I recomputed it over the 17 sorted paths in `docs/session-keys-production-manifest.txt`, hashing each `path || NUL || file bytes || NUL`.
- The digest identifies production Go sources only; it does not bind tests or docs. The verification commands below ran against `68515adb5023625f1238f09bd623debeccd23c52`, manifest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`. I compared every manifest file at both commits: the only source delta is a comment in `core/state_transition.go` clarifying that session mining rewards use the payer/owner stake. No executable bytes changed, so the test results apply to the final source behavior.
- I reviewed the 3-page design PDF as controlling over conflicting caller semantics in the 17-page context PDF, and read the current ADR/user docs, test additions, and production code paths for genesis, configuration, registry, EVM, StateDB, transition, consensus finalizers, txpool, miner, and RPC.

## Correctness assessment

The implementation matches the requested native execution model in the inspected paths. The transaction signer and nonce lane remain the session key; top-level `msg.sender` is the session and `tx.origin` is its owner. The owner funds native APLO value and GAplo gas fees through separate remaining budgets. The top-level call must match one target and an exact four-byte selector. Invalid prechecks use the transaction snapshot and restore gas-pool state; included reverts retain nonce and actual gas costs while rolling back value and budget changes.

Registry mutation requires a direct, zero-value owner EOA call; possession of the owner as `tx.origin` alone does not authorize nested calls. Registration requires a low-S proof from the new key binding the owner, effective chain ID, registry/version, target, ordered selectors, budgets, and expiry. The registry rejects absent/invalid chain-ID domains rather than silently using zero. Used-key owner tombstones remain after revoke/expiry and route later native credits to the owner. Owner and expiry packed indexes are journaled in StateDB; expiry is inclusive and cleanup follows transaction and reward processing. The inspected bounds remain finite for selectors, owner sessions, per-height expiry entries, and lifetime.

Genesis setup normalizes registry nonce one and canonical GAplo through the same allocation path for state-root derivation and persistence, without mutating the caller's map. Reserved-address conflicts are rejected. Startup validates persisted native state read-only, and recovery checks the stored allocation against the original header state root before writing it. The developer faucet rejects reserved/precompile addresses and is separately funded in native APLO and GAplo. Configuration and signer-domain checks make EIP-155 active at genesis, constrain signing domains to uint256, and lock primary/alternate chain IDs at the relevant boundary. GAplo recipient rewriting preserves malformed ABI address words so the canonical contract decoder rejects them. Session Keys no longer have an activation height or fork-ID entry.

I found no production-path issue in the reviewed pool reservation/replacement logic, Beacon finalization ordering, incoming APLO/GAplo routing, or the real-chain branch/restart scenario. The target/selector rule remains top-level only: a whitelisted contract may use its normal nested calls or delegatecalls and observes the owner as `tx.origin`. Applications must not use `tx.origin` alone as their owner check. The proof replay domain is chain ID, not genesis identity; networks intentionally sharing an ID also share proof replay exposure.

## Verification performed

Commands used Go 1.20.14 at `/private/tmp/aplo-toolchain/go/bin/go`, `GOPATH=/private/tmp/aplo-go-work`, and `GOCACHE=/private/tmp/aplo-go-cache`.

- `go test ./builtin/... ./core ./params ./internal/ethapi ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner -run 'SessionKeys' -count=1` — **PASS**. Session Keys cases passed in the registry, core, params, and RPC packages; consensus/miner packages compiled and had no matching named tests.
- `go test ./core -run 'TestSessionKeysNativeGenesis|TestSessionKeysSetupRejectsLegacyGenesisWithoutMigration|TestSessionKeysCommitGenesisState|TestDeveloperGenesisRejectsReservedFaucetAddresses|TestGenesis|TestRestartWithNewSnapshot|TestShortRepair' -count=1` — **PASS**, including non-mutating legacy rejection, root-verified recovery, conflicting allocation rejection, EIP-155 genesis requirements, faucet collision cases, supported native genesis identities, and normalized repair/snapshot fixtures.
- `go test ./params/... -count=1` — **PASS**.
- `go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params -run 'SessionKeys' -count=1` — **PASS**.
- `go test ./console -count=1` — **PASS**.
- `go build ./cmd/geth ./examples/sessionkeys` — **PASS**.
- `git diff --check` — **PASS**. `gofmt -l` over changed production and test Go files — no output.
- I independently recomputed the final production manifest digest; it is `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`.

## Remaining failures and compatibility boundaries

`go test ./core -count=1` remains **not green**. After the genesis and snapshot fixtures were updated, my full-package run failed at `TestFastVsFullChains` with an insufficient-funds panic. I ran the same named test on `/private/tmp/aplo-baseline`; it fails at the same `AddTxWithChain` path for the same sender with the same insufficient-funds condition. This comparison does not identify a Session Keys regression. The focused genesis/snapshot command above passes on the updated tree.

No full-repository pass was established in this independent review. The review conclusion is limited to the listed source snapshot, verification results, and compatibility boundaries.

The permanent-genesis architecture intentionally has no migration for an old database whose genesis lacks native registry state. Startup rejects it without writing or deleting data; Genesis-state recovery rejects legacy allocations and root mismatches. The implementation therefore does not preserve compatibility with an existing chain's old genesis identity. The current ADR/docs narrow use to a freshly initialized disposable devnet and instruct operators to retain old data; any broader network rollout would require a separately reviewed migration/network plan.
