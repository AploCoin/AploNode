# Session Keys test record

This record covers the permanent, native Session Keys protocol. The final source commit is `9b62907f381f1991bff2b87183635c047fd715a7`; its 17-file production-source manifest hashes to `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`, using `path || NUL || file bytes || NUL` over the sorted paths in `docs/session-keys-production-manifest.txt`. The final commit changes only an inline comment in `core/state_transition.go`; it does not alter executable code or tests. The Session Keys race, fuzz, CLI smoke, and broader checks below were run against code commit `68515adb5023625f1238f09bd623debeccd23c52` and production manifest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`, before that comment-only follow-up. The focused suite was rerun against the final source commit.

## Environment

The checks used Go 1.20.14 at `/private/tmp/aplo-toolchain/go/bin/go`, with `GOPATH=/private/tmp/aplo-go-work` and `GOCACHE=/private/tmp/aplo-go-cache`.

## Coverage

- Native genesis handling installs the reserved registry account and canonical GAplo runtime through `ToBlock`, `Commit`, and `SetupGenesisBlock`, without mutating caller allocations. Tests reject account/runtime collisions, invalid protected signing domains, historical presets without block-zero EIP155, and developer faucet addresses reserved for protocol or precompile use.
- Existing data handling rejects legacy genesis without native accounts and proves no database writes occur. `CommitGenesisState` recovery is checked after dropping trie state, including the known default-genesis missing-spec fallback. Tampered normalized and legacy allocation specifications are rejected without migration or writes.
- Registry authorization and lifecycle cover proof-of-possession binding to owner, session key, target, selectors, APLO and GAplo budgets, expiry, and chain ID. Forged, empty, truncated, high-`s`, wrong-owner, wrong-chain, and mutated-payload proofs are rejected. Tests also cover nil, negative, and over-uint256 chain domains in direct registry dispatch, proof verification, and EVM dispatch, with unchanged state on rejection.
- Registry accounting and lifecycle cover exact selectors, short calldata, nonce separation, budget accounting, inclusive expiry, overflow boundaries, selector/session/bucket caps, revoke cleanup, permanent no-reuse markers, and owner/expiry indexes. Public ABI registration at the 32-selector cap is exercised.
- Signed execution verifies owner `tx.origin` and session-key top-level caller, separate native APLO value and GAplo fee balances, invalid-state preservation, replay rejection, missing funds, and actual fee charging with value rollback on revert or out-of-gas. Maximum session nonce rejection preserves budgets and funds. Session-triggered `CREATE` verifies constructor context.
- Nested calls cannot use owner `tx.origin` to create or revoke; CALL, CALLCODE, DELEGATECALL, and STATICCALL contexts are exercised. Native APLO and canonical GAplo value through internal `CALL` and `SELFDESTRUCT` routes redirect to the owner, including after expiry or revocation.
- Malformed GAplo recipient ABI words with nonzero high address padding are tested on canonical `transfer`, approved `transferFrom`, and root-only `refund` calls. Each has a valid-address control that redirects to the owner; the malformed version must revert without changing state or crediting the owner or session key.
- Lifecycle parity covers same-block create/use/revoke ordering, cleanup after execution, processor/import versus consensus `FinalizeAndAssemble` state-root parity, branch isolation, trie commit/reopen, and transaction-pool owner funding, cumulative pending budgets, replacement, and reorg-prefix behavior.
- RPC coverage exercises signed `eth_call`/`estimateGas` simulation. `TestSessionKeysInsertChainReorgAndRestart` imports actual branches through `BlockChain.InsertChain`, reorgs between use and revoke branches, then stops and reopens the chain to verify canonical state and balances. A regular EOA identity regression is included.
- The registry fuzz property checks that rejected arbitrary ABI input does not mutate state.

## Focused verification

The Session Keys package sweep passed on both the tested code commit and final source commit:

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

## Broader regression results and limits

The full affected-package command was run against the tested commit:

```sh
go test ./builtin/... ./core/... ./params/... ./consensus/... ./miner/... ./internal/ethapi/...
```

It remains unsuccessful. `core.TestFastVsFullChains` and `consensus/clique.TestReimportMirroredState` fail because their signed test transactions have APLO but no GAplo fee balance. I ran each against the clean base archive at `/private/tmp/aplo-baseline` (base `0416fd355f53f0d09b256e194d4f44f9bb91663e`); both fail there with the same insufficient-GAplo-fixture cause. The repair, snapshot, and genesis tests that initially lacked a native genesis trie now pass after their generator fixtures were seeded with that genesis state.

Remaining current broad failures are `core/forkid.TestCreation` and `TestValidation` fork-ID mismatches; `core/vm/runtime.TestEVM` nil-pointer panic; `consensus/ethash.TestDifficultyCalculators` mismatch; `consensus/ethash.TestRemoteNotify` denied localhost bind in this sandbox; and six miner worker timeout/receipt failures: `TestGenerateBlockAndImportEthash`, `TestGenerateBlockAndImportClique`, `TestEmptyWorkEthash`, `TestEmptyWorkClique`, `TestRegenerateMiningBlockEthash`, and `TestRegenerateMiningBlockClique`. Those remaining failures were not individually compared against the base in this final run. Successful packages in the broad run included `builtin/sessionkeys`, `core/rawdb`, `core/state`, `core/state/snapshot`, `core/types`, `core/vm`, `params`, `consensus/beacon`, `consensus/misc`, and `internal/ethapi`.

The full broad command against the unmodified base archive cannot complete because its old miner test mock lacks `GetBlockByNumber`. The two GAplo funding failures were therefore compared as individual tests. Focused Session Keys, race, fuzz, RPC, consensus, and reorg checks passed, but the complete affected-package regression remains red for the failures listed above.
