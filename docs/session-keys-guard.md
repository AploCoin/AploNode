# Independent Session Keys guard review

## Current targeted audit — master snapshot

**PASS for the requested reviewer questions.** I found no unresolved registry authorization or session-use consensus defect in the frozen master snapshot.

- Reviewed commit: `ed45c62ec6c3cf4827f650378a5911a3d22a398a`.
- Production-source manifest SHA-256: **`75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`**, recomputed over the 17 sorted paths in [session-keys-production-manifest.txt](session-keys-production-manifest.txt) using `path UTF-8 || NUL || file bytes || NUL`.
- The production manifest is unchanged from the prior native protocol review. The current commit includes the additional reviewer-focused tests and documentation.

The registry address is rejected as owner, key, and target. The other reserved protocol and precompile addresses cannot be owners or keys. `Create` also rejects self-ownership, used owners/keys, contract-code owners, and already-existing keys; it permits ordinary contract targets. No generic selector or “admin method” blacklist is appropriate here because the protocol authorizes one explicit top-level target and selector while leaving that target's normal application behavior intact.

Creation requires both the owner's signed, replay-protected transaction and the new key's low-S possession signature. The proof binds the owner, key, effective chain ID, target, ordered selectors, allowances, and expiry. In EVM dispatch, mutations additionally require zero value, depth zero, immediate caller equal to origin, no caller code, and a caller that is not already a used session key. Those checks make the registry rely on the actual caller rather than `tx.origin` alone. `CALLCODE` and `DELEGATECALL` to the registry revert; `STATICCALL` can only use its read-only path. Contract creation at the registry address is rejected as a collision.

The session-use replay domain is enforced during block processing, independently of TxPool policy. `StateProcessor.applyTransaction` rejects an unprotected transaction from a used session key and an unprotected direct registry transaction before EVM execution. The configured block signer binds protected transactions to the active primary chain ID or `ChainID_ALT` at the EthPoW fork. Transaction nonce remains on the session key. The same state-transition path checks target, selector, inclusive expiry, owner APLO funds, owner GAplo fee funds, and remaining budgets; revoke/expiry clear active authorization but retain the used-key tombstone. Tests also exercise imported state across reorg/restart.

I confirmed the PDF's top-level semantics: the session is `msg.sender`, its owner is `tx.origin`, and nested EVM calls retain ordinary caller semantics. This means an allowed contract target that authorizes solely by `tx.origin` may expose owner authority to that session's permitted call. That is an application authorization boundary inherent in the requested model; the registry's own mutation path rejects nested origin-only authorization. Contract targets remain legal and are not filtered by speculative admin-selector rules.

Using Go 1.20.14 at `/private/tmp/aplo-toolchain/go/bin/go`, with `GOPATH=/private/tmp/aplo-go-work` and `GOCACHE=/private/tmp/aplo-go-cache`, I independently ran:

- `go test -race ./builtin/sessionkeys ./core ./params -run 'SessionKeys|RegistrySessionKeys|NativeGenesis|DeveloperGenesisRejects' -count=1` — **PASS**.
- `go test ./core -run '^TestSessionKeysStateProcessorRejectsUnprotectedSessionAndRegistryTransactionsIndependent$' -count=1 -v` — **PASS**. The test calls `StateProcessor.Process` without TxPool: unprotected transactions from a used session key and direct registry calls are rejected without state changes, while an ordinary unprotected EOA control succeeds.
- `go test ./core -run '^TestSessionKeysAlternateDomainRegistrationAndUseIndependent$' -count=1 -v` — **PASS**. Registration and session use accept the alternate protected domain after the fork; a primary-domain possession proof is rejected.
- The race sweep includes registry role restrictions, nested CALL/CALLCODE/DELEGATECALL/STATICCALL attempts, owner EOA and proof checks, nonce/budget/expiry/revoke lifecycle, and the real `InsertChain` reorg/restart test.
- `gofmt -d` over the 17 production manifest paths produced no output; `git diff --check` — **PASS**.

There is no remaining finding from this targeted audit. The app-level `tx.origin` behavior above is the principal usage caveat. The earlier full-snapshot verification and deployment limits remain below as historical evidence; they are not a fresh full-repository test run for this master commit.

## Earlier full-snapshot guard review (historical)

### Historical verdict

**PASS with verification limits.** I found no unresolved Session Keys consensus or security defect in the reviewed production snapshot. This verdict covers the native-from-genesis implementation and the checks listed here; it does not certify a complete repository test pass, a public chain genesis, or an upgrade path for an existing network.

### Historical snapshot

- Base: `0416fd355f53f0d09b256e194d4f44f9bb91663e`.
- Reviewed commit: `9b62907f381f1991bff2b87183635c047fd715a7` (`core: clarify native session reward qualification`).
- Production-source manifest SHA-256: **`75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`**.
- The digest was recomputed over the 17 sorted paths in [session-keys-production-manifest.txt](session-keys-production-manifest.txt), updating SHA-256 with `path UTF-8 || NUL || file bytes || NUL` for each path. It identifies production Go sources, not tests or documentation.

I reviewed the three-page Session Keys design as controlling for caller semantics where it conflicts with the 17-page context document, and inspected the current registry, genesis, transaction transition, EVM, StateDB, transaction pool, RPC, miner, and consensus-finalization paths.

### Earlier security and consensus findings

The session key remains the transaction signer and nonce lane. On the authorized top-level call, `msg.sender` is the session key and `tx.origin` is its owner; nested calls preserve ordinary EVM caller behavior. Registration and revocation require a direct, zero-value owner EOA call. An owner appearing only as `tx.origin` cannot authorize a nested registry mutation. The single top-level target and exact four-byte selector are checked before execution; a whitelisted contract can still make its normal nested calls, so target contracts must not rely on `tx.origin` alone for authorization.

Registration requires a low-S key-possession signature binding the owner, key, effective chain ID, target, ordered selectors, budgets, and expiry. Invalid or absent signing domains are rejected rather than treated as chain ID zero. This closes the fresh-address squatting issue: an owner cannot register somebody else's unproven key. Proofs use the chain ID replay domain, not a unique genesis identity; networks intentionally sharing a chain ID can replay the proof, just as they can replay EIP-155 transactions.

Session transactions reserve the maximum GAplo fee against the owner's separate remaining fee allowance and charge the actual fee. Top-level native value and APLO builtin transfers debit owner funds and the APLO allowance. EVM reverts restore value and allowance changes while retaining the signer nonce and actual gas charge; invalid transaction prechecks do not commit state. Pool reservations account for pending owner/key spending and are rechecked when the head changes. Expiry is inclusive through the last block, cleanup follows transaction and reward execution, and bounded owner/expiry indexes, finite lifetime, and permanent used-key tombstones prevent unbounded cleanup and address reuse. Revoke and expiry remove authorization but keep the native credit recipient mapped to the owner.

Genesis derivation, commit, and setup use the same allocation normalization: registry `0x1237` is code-free with nonce one and empty genesis storage, and GAplo `0x1234` has exactly the canonical runtime. Conflicting registry allocations and noncanonical GAplo runtime are rejected. Runtime state checks fail closed; they do not initialize protocol accounts during the first transaction. Recovery verifies the persisted allocation against the original header state root before writing. Native credits, including internal CALL, SELFDESTRUCT, fee/DAO credit, and consensus rewards, use the tombstone recipient. Canonical GAplo `transfer`, `transferFrom`, and root-only `refund` recipient routing preserves malformed ABI words so the contract decoder rejects them. Arbitrary token storage is outside this routing guarantee.

The source and tests cover expiry/revoke cleanup, snapshots, branch isolation, processor/miner state-root parity, transaction-pool reservations, and import/reorg/restart. Regular EOA identity is explicitly exercised, and a fresh `--dev` smoke sends its first ordinary transaction successfully. The GAplo mining reward tier intentionally reads the owner's stake, while `stake` and `unstake` still use the actual caller and do not spend owner stake. I found no focused test asserting a session-triggered `mine` payout against owner stake, so that specific reward interaction remains unverified.

### Earlier verification

The focused tests below ran on commit `68515adb5023625f1238f09bd623debeccd23c52` with production manifest SHA-256 `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`. The reviewed commit above changes only the `core/state_transition.go` inline comment describing session mining-reward qualification; I verified the exact two-line diff and confirmed no executable code changed. I did not rerun tests for this comment-only follow-up. Using Go 1.20.14 at `/private/tmp/aplo-toolchain/go/bin/go`, with `GOPATH=/private/tmp/aplo-go-work` and `GOCACHE=/private/tmp/aplo-go-cache`, I independently ran:

- `go test -race ./builtin/sessionkeys ./core ./params -run 'SessionKeys|NativeGenesis|Genesis.*Native|DeveloperGenesisRejects' -count=1` — **PASS**.
- `go test ./core -run '^TestSessionKeys(NativeGenesis|SetupRejectsLegacyGenesisWithoutMigration|CommitGenesisState)' -count=1 -v` — **PASS**, including legacy-genesis rejection without migration, conflicting allocation rejection, recovery after lost trie state, and rejection of tampered/legacy specs without writes.
- `go test ./core ./internal/ethapi ./miner ./params -run 'SessionKeys|NativeGenesis|Genesis.*Native|DeveloperGenesisRejects' -count=1` — **PASS**.
- `go test ./core -run '^TestSessionKeysInsertChainReorgAndRestart$' -count=1 -v` — **PASS**.
- `go build -o /private/tmp/aplo-geth-guard ./cmd/geth` — **PASS**; only existing macOS C dependency deprecation warnings appeared.
- On the reviewed commit, `gofmt -d` over the 17 production manifest files — no output. `git diff --check` — **PASS**.

The separate testing record also reports a passing bounded registry fuzz run (167,730 executions), `go test ./console -count=1`, the `./params/...` suite, offline protected signing, generated-genesis initialization, and a real `--dev` first transaction. Its smoke summary records registry nonce `1`, receipt status `0x1`, block `1`, and `21,000` gas.

### Earlier verification limits and deployment boundaries

The full affected-package regression command remains red. The testing record reports `core.TestFastVsFullChains` and `consensus/clique.TestReimportMirroredState` failing from insufficient GAplo fixture balances; both were run individually on base `0416fd3` and fail with the same cause. Other reported failures are `core/forkid.TestCreation`, `core/forkid.TestValidation`, `core/vm/runtime.TestEVM`, `consensus/ethash.TestDifficultyCalculators`, the sandbox-denied `consensus/ethash.TestRemoteNotify`, and six Ethash/Clique miner worker timeout or receipt tests. Those remaining failures were not individually compared against base. The full `go build ./...` is also reported blocked by unrelated/outdated call sites and imports; `./cmd/geth` builds successfully.

Native Session Keys intentionally change fresh genesis identity. An old database whose genesis lacks the registry and canonical GAplo is rejected at startup; there is no migration, and this work did not delete or modify user data directories. No production activation height, public-network transition, or final public genesis hash has been selected or verified. Registry funds explicitly allocated as native dust remain trapped at the reserved address. Operators must provide canonical GAplo in genesis and use a fresh disposable devnet for this native protocol; broader network rollout needs its own reviewed genesis and migration plan.

For the implementation contract and broader test record, see [session-keys.md](session-keys.md) and [session-keys-testing.md](session-keys-testing.md).
