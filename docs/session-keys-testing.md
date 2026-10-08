# Session Keys test record

This record covers the permanent, native Session Keys protocol. The latest frozen audit source is `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118`, based on parent `2bc7d7af820b9706d242ad6240ce11ff18182742`. Its 17-file production-source manifest hashes to `b6ed703257c7124c86de6cdb273b48767e1d0b0c952af1637b65a5dbb383e3d0`, using `path || NUL || file bytes || NUL` over the sorted paths in `docs/session-keys-production-manifest.txt`. Fresh checks for this source snapshot are recorded first. The earlier snapshot `ed45c62ec6c3cf4827f650378a5911a3d22a398a` and its verification remain below with their original provenance; older fuzz, CLI smoke, and broad-suite results are not presented as fresh checks on `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118`.

## Fresh frozen audit verification (2026-10-05)

The 17-file production manifest was recomputed and matched `b6ed703257c7124c86de6cdb273b48767e1d0b0c952af1637b65a5dbb383e3d0`. The fresh normal and race-enabled Session Keys sweeps passed, including the CREATE2 audit regressions and transaction-pool signing-domain/reset tests.

```sh
go test ./builtin/... ./core ./params ./internal/ethapi ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner -run 'SessionKeys|RegistrySessionKeys' -count=1
go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params -run 'SessionKeys|RegistrySessionKeys' -count=1
go test ./core -run '^TestSessionKeysInsertChainReorgAndRestart$' -count=1 -v
go build -o /private/tmp/aplo-sessionkeys-example-b76e10e9 ./examples/sessionkeys
go build -o /private/tmp/aplo-create2-geth ./cmd/geth
```

All five commands passed on `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118`. Consensus and miner packages compiled and reported no matching tests; the core sweep exercised the consensus finalizer parity checks and the new signed-block CREATE2 tests. The actual `BlockChain.InsertChain` reorg/restart test passed separately. The example and `cmd/geth` builds wrote outputs under `/private/tmp`.

The bounded fuzz run and CLI smoke below were not rerun for this snapshot. The earlier broad-suite results below also remain historical; the separate current ordinary-pool probe and its limits are recorded next.

## Current ordinary-pool probe

The focused ordinary-pool probe is not green:

```sh
go test ./core -run 'TestStateChangeDuringTransactionPoolReset|TestTransaction|TestInvalidTransactions|TestDualHeapEviction' -count=1 -timeout=60s
```

It exits 1. `TestTransactionIndices` reports the existing missing-GAplo-funds panic in both the frozen source and exact parent logs (`/private/tmp/aplo-create2-ordinary-pool-current.log` and `/private/tmp/aplo-create2-ordinary-pool-parent.log`). Eight separately compared pool tests also fail at the same assertion or panic sites on the frozen source and parent: `TestStateChangeDuringTransactionPoolReset`, `TestTransactionDropping`, `TestTransactionGapFilling`, `TestTransactionQueueAccountLimiting`, `TestTransactionPendingLimiting`, `TestTransactionQueueTimeLimiting`, `TestTransactionQueueTimeLimitingNoLocals`, and `TestTransactionMissingNonce`. The per-test comparison is recorded in `/private/tmp/aplo-create2-ordinary-individual.json`. This comparison covers those listed tests only; it does not establish that every failure in a broader run is a parent failure. A complete affected-package/full-repository suite was not run for this snapshot.

## Earlier frozen snapshot verification (2026-10-05; `ed45c62ec6c3cf4827f650378a5911a3d22a398a`)

The earlier 17-file production manifest recomputed to `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`. These three commands passed on `ed45c62ec6c3cf4827f650378a5911a3d22a398a`:

```sh
go test ./builtin/... ./core ./params ./internal/ethapi ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner -run 'SessionKeys|RegistrySessionKeys' -count=1
go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params -run 'SessionKeys|RegistrySessionKeys' -count=1
go test ./core -run '^TestSessionKeysInsertChainReorgAndRestart$' -count=1 -v
```

Consensus and miner packages compiled with no matching tests; the core sweep exercised consensus finalizer parity, and the reorg/restart case used actual `BlockChain.InsertChain`. The bounded fuzz and CLI smoke records below were not rerun on that snapshot. The later `cmd/geth` build on `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118` is recorded at the top.

## Environment

The checks used Go 1.20.14 at `/private/tmp/aplo-toolchain/go/bin/go`, with `GOPATH=/private/tmp/aplo-go-work` and `GOCACHE=/private/tmp/aplo-go-cache`.

## Coverage

- Native genesis handling installs the reserved registry account and canonical GAplo runtime through `ToBlock`, `Commit`, and `SetupGenesisBlock`, without mutating caller allocations. Tests reject account/runtime collisions, invalid protected signing domains, historical presets without block-zero EIP155, and developer faucet addresses reserved for protocol or precompile use.
- Existing data handling rejects legacy genesis without native accounts and proves no database writes occur. `CommitGenesisState` recovery is checked after dropping trie state, including the known default-genesis missing-spec fallback. Tampered normalized and legacy allocation specifications are rejected without migration or writes.
- Registry authorization and lifecycle cover proof-of-possession binding to owner, session key, target, selectors, APLO and GAplo budgets, expiry, and chain ID. Forged, empty, truncated, high-`s`, wrong-owner, wrong-chain, and mutated-payload proofs are rejected. Tests also cover nil, negative, and over-uint256 chain domains in direct registry dispatch, proof verification, and EVM dispatch, with unchanged state on rejection.
- Registry accounting and lifecycle cover exact selectors, short calldata, nonce separation, budget accounting, inclusive expiry, overflow boundaries, selector/session/bucket caps, revoke cleanup, permanent no-reuse markers, and owner/expiry indexes. Public ABI registration at the 32-selector cap is exercised.
- Signed execution verifies owner `tx.origin` and session-key top-level caller, separate native APLO value and GAplo fee balances, invalid-state preservation, replay rejection, missing funds, and actual fee charging with value rollback on revert or out-of-gas. Maximum session nonce rejection preserves budgets and funds. Session-triggered `CREATE` verifies constructor context.
- CREATE2 audit integration uses signed block processing and signed contract creation to deploy the factory. A directly signed registration consumes the owner nonce; a real session call makes the factory's CREATE2 constructor attempt registry registration with a valid key-possession proof and owner `tx.origin`, which the nested caller cannot use to gain owner authority. A signed block also rejects registration from a code-bearing recovered signer; that case explicitly seeds code in fixture state and is not a CREATE2 address construction.
- The allowed-target integration deploys a deterministic CREATE2 target, calls its whitelisted selector through a session, self-destructs it under legacy semantics, and redeploys different runtime at the same address. The replacement still receives `msg.sender=session key` and `tx.origin=owner`; this verifies that authorization follows target address and selector while contract code can change.
- Synthetic CREATE2 collision invariants submit a signed factory call after forcing a destination nonce or `Used` state. Active, revoked, and expired tombstones each prevent deployment, and an unforced signed-factory control deploys successfully. These forced states test the EVM guard only; they do not construct a chosen EOA address preimage or demonstrate a reachable registration attack.
- Nested calls cannot use owner `tx.origin` to create or revoke; CALL, CALLCODE, DELEGATECALL, and STATICCALL contexts are exercised. Native APLO and canonical GAplo value through internal `CALL` and `SELFDESTRUCT` routes redirect to the owner, including after expiry or revocation.
- Malformed GAplo recipient ABI words with nonzero high address padding are tested on canonical `transfer`, approved `transferFrom`, and root-only `refund` calls. Each has a valid-address control that redirects to the owner; the malformed version must revert without changing state or crediting the owner or session key.
- Lifecycle parity covers same-block create/use/revoke ordering, cleanup after execution, processor/import versus consensus `FinalizeAndAssemble` state-root parity, branch isolation, trie commit/reopen, and transaction-pool owner funding, cumulative pending budgets, replacement, and reorg-prefix behavior.
- RPC coverage exercises signed `eth_call`/`estimateGas` simulation. `TestSessionKeysInsertChainReorgAndRestart` imports actual branches through `BlockChain.InsertChain`, reorgs between use and revoke branches, then stops and reopens the chain to verify canonical state and balances. A regular EOA identity regression is included.
- The registry fuzz property checks that rejected arbitrary ABI input does not mutate state.

## Previously recorded focused verification

The following records predate frozen snapshot `ed45c62ec6c3cf4827f650378a5911a3d22a398a`. They document checks on tested code commit `68515adb5023625f1238f09bd623debeccd23c52` with production manifest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`, followed by comment-only source commit `9b62907f381f1991bff2b87183635c047fd715a7` with manifest `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`. That follow-up changes only an inline comment in `core/state_transition.go`; it changes no executable code or tests. The fresh checks for `ed45c62ec6c3cf4827f650378a5911a3d22a398a` are recorded above; the current `b76e10e9d91abaac9feb89d7dfc6a14e2b82e118` checks are at the top.

The earlier Session Keys package sweep passed on those commits:

```sh
go test ./builtin/... ./core ./params ./internal/ethapi ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner -run 'SessionKeys' -count=1
```

The full `params` package suite passed with `go test ./params/... -count=1`. Race-enabled Session Keys tests passed across the registry, core, RPC, and params packages:

```sh
go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params \
  -run 'SessionKeys|RegistrySessionKeys' -count=1
```

The real canonical-chain reorg/restart test passed independently:

```sh
go test ./core -run '^TestSessionKeysInsertChainReorgAndRestart$' -count=1 -v
```

The bounded registry fuzz run on code commit `68515adb5023625f1238f09bd623debeccd23c52` completed 167,730 executions in about 10 seconds, found one additional interesting input, and reported no failure:

```sh
GOMAXPROCS=2 go test ./builtin/sessionkeys -run '^$' \
  -fuzz '^FuzzSessionKeysRunCanonicalABIIndependent$' -fuzztime=10s -parallel=2
```

`go build ./cmd/geth`, `gofmt` on the Session Keys test files, and `git diff --check` succeeded. The repository `./console` tests passed after the faucet fixture was changed to an ordinary EOA. A native CLI smoke test at `/private/tmp/aplo-native-smoke-5oi7osyc/summary.json` reports generated-genesis initialization, offline protected signing, and a `--dev` first ordinary transaction all passing; it observes registry nonce 1, receipt status `0x1`, block 1, and 21,000 gas.

The complete `go build ./...` still fails in unrelated or outdated repository code: `tests/state_test_util.go` uses the old five-argument `vm.NewEVM` call; `cmd/devp2p` and `mobile` refer to removed `params.RinkebyBootnodes`/`params.MainnetBootnodes`; and `cmd/faucet/faucet.go` has an unused `cmd/utils` import. The `cmd/geth` build and native smoke are separate successful checks.

## Previously recorded broader regression results and limits

The full affected-package command below was run against tested commit `68515adb5023625f1238f09bd623debeccd23c52`, not the frozen snapshot:

```sh
go test ./builtin/... ./core/... ./params/... ./consensus/... ./miner/... ./internal/ethapi/...
```

It remains unsuccessful. `core.TestFastVsFullChains` and `consensus/clique.TestReimportMirroredState` fail because their signed test transactions have APLO but no GAplo fee balance. I ran each against the clean base archive at `/private/tmp/aplo-baseline` (base `0416fd355f53f0d09b256e194d4f44f9bb91663e`); both fail there with the same insufficient-GAplo-fixture cause. The repair, snapshot, and genesis tests that initially lacked a native genesis trie now pass after their generator fixtures were seeded with that genesis state.

Other failures from that historical broad run were `core/forkid.TestCreation` and `TestValidation` fork-ID mismatches; `core/vm/runtime.TestEVM` nil-pointer panic; `consensus/ethash.TestDifficultyCalculators` mismatch; `consensus/ethash.TestRemoteNotify` denied localhost bind in that sandbox; and six miner worker timeout/receipt failures: `TestGenerateBlockAndImportEthash`, `TestGenerateBlockAndImportClique`, `TestEmptyWorkEthash`, `TestEmptyWorkClique`, `TestRegenerateMiningBlockEthash`, and `TestRegenerateMiningBlockClique`. Those failures were not individually compared against the base in that historical run. Successful packages in the broad run included `builtin/sessionkeys`, `core/rawdb`, `core/state`, `core/state/snapshot`, `core/types`, `core/vm`, `params`, `consensus/beacon`, `consensus/misc`, and `internal/ethapi`.

The full broad command against the unmodified base archive cannot complete because its old miner test mock lacks `GetBlockByNumber`. The two GAplo funding failures were therefore compared as individual tests. Focused Session Keys, race, fuzz, RPC, consensus, and reorg checks passed, but the complete affected-package regression remains red for the failures listed above.
