# Session Keys native implementation record

## Reviewer-question follow-up (2026-10-05)

The frozen source/test snapshot for this follow-up is `ed45c62ec6c3cf4827f650378a5911a3d22a398a`. No production Go file changed from `83e121aeaab80bf358641b54946c7f196cba9899`; the 17-file production manifest remains `75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`. Independent review and guard found no confirmed production defect in the requested authorization, EVM-context and consensus paths. The change closes specific coverage and documentation gaps.

`session-keys.md` now explains the account-address representation, owner transaction plus key possession proof, subsequent session signatures/nonces, top-level and nested caller semantics, block processing, registry address restrictions and EOA ownership. Contract targets remain permitted; their own authorization must account for session caller identity and owner origin.

New tests cover:

- Public ABI registration rejects registry owner/target and code-bearing owners with valid possession proofs; an ordinary contract target is accepted. A separate trusted `Create` test exercises the registry-as-key guard, since a matching private-key proof for that fixed address is computationally unavailable.
- A genuinely signed transaction from an account with code fails `ApplyTransaction` with `ErrSenderNoEOA`, preserving the full state root, owner funds/nonces and registry state.
- `StateProcessor.Process` rejects unprotected session-sender and direct-registry transactions without state, budget or gas changes. An ordinary unprotected EOA control succeeds with the optional EthPoW rule disabled, isolating the native guard independently of txpool.
- At the EthPoW boundary, ALT-signed owner registration and session use with an ALT-domain proof succeed through block processing. An ALT-signed registration with a primary-domain proof yields a failed receipt without registering or reserving a key.

The root focused sweep passed on this snapshot using Go 1.20.14 and task-local caches:

```sh
go test ./builtin/... ./core ./params ./internal/ethapi \
  ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner \
  -run SessionKeys -count=1
```

The consensus and miner packages compile in this sweep but have no tests selected by that pattern; core tests execute the processor/finalizer parity checks. Fresh independent normal, race and real `InsertChain` reorg/restart results are recorded in the role reports. The full suite, fuzz, builds and CLI smoke below are historical evidence and were not rerun for this tests/documentation-only follow-up. Their recorded limits still apply.

## Native implementation snapshot (historical)

Session Keys are a permanent part of Aplo on `master`, based on `0416fd355f53f0d09b256e194d4f44f9bb91663e`. The native code/tests commit is `68515adb5023625f1238f09bd623debeccd23c52`; the final source commit is `9b62907f381f1991bff2b87183635c047fd715a7`. The latter changes one plain comment only; independent byte/diff checks verified that executable code and tests are unchanged. Test and CLI evidence were recorded on the former commit (production digest `bf7cae691649430094bddf1fd829f4d0235016e588387a111c97500847418086`), and final source identity/hygiene were checked after the comment clarification. This record replaces the earlier fork-gated implementation evidence. The new protocol has no activation option, StateDB enable flag, initialization marker or first-transaction migration. It requires a fresh native genesis; no existing user data directory was deleted or migrated.

The production-source SHA-256 is:

`75b6ec800698eb081bbcdade46be026fa9f170d955e55b19b90570b44835ece9`

It identifies the 17 sorted changed/new non-test Go files in `session-keys-production-manifest.txt`, including the example and historical fixture constants. Reproduce it from the repository root:

```python
import hashlib, pathlib
h = hashlib.sha256()
for p in pathlib.Path("docs/session-keys-production-manifest.txt").read_text().splitlines():
    h.update(p.encode() + b"\0" + pathlib.Path(p).read_bytes() + b"\0")
print(h.hexdigest())
```

## Implemented behavior

Genesis root derivation and persistence normalize the same copied allocation. The registry at 0x1237 is reserved without code, with nonce one and empty genesis storage; canonical GAplo is installed before the first block. `ToBlock`, `Commit`, `SetupGenesisBlock`, the example and `--dev` use this path. Explicit reserved-account conflicts are rejected. The developer faucet has separate APLO and GAplo funds, and reserved faucet addresses are rejected. EIP155 must be active at genesis and signing domains must fit uint256.

State checks during execution, import and mining are read-only. Startup rejects a database whose genesis lacks the native accounts. Genesis recovery rejects old/tampered allocations and checks the original header root before writing. The actual default native genesis is recognized for missing-spec recovery. Existing chain identities are intentionally incompatible; this implementation supplies no migration. Historical Ropsten/Rinkeby presets postpone EIP155 and are explicitly rejected; unrelated fork schedules and generic fork-ID logic are retained. `params.AploGenesisHash` remains the pre-existing zero placeholder; this local work does not establish a published network genesis pin.

The session key retains transaction signing, its nonce lane and top caller identity. Its owner supplies `tx.origin`, native APLO value and GAplo fees, with separate remaining allowances. Direct owner registration requires a mandatory low-S session proof bound to effective chain ID and every creation field. Registry state, owner indexes, absolute expiry buckets and permanent used-key ownership persist through StateDB journaling, trie commits, snapshots, rollback and reorgs. Revoke is immediate; expiry is inclusive and cleanup follows transactions and rewards. Invalid transactions restore state and GasPool; included reverts retain nonce and actual fee while rolling back value.

Native APLO and canonical GAplo credit paths route used-key recipients to the permanent owner. Malformed ABI address padding is left to the canonical GAplo decoder to reject. Arbitrary token contracts retain their own storage semantics. Target/selector scope constrains the top call; contracts retain ordinary nested/delegatecall behavior and must not authorize owner operations using `tx.origin` alone. The signing domain is chain ID, not genesis identity. The standard uint64 wire/account nonce remains in use.

## Verification

Go 1.20.14 was used with task-local toolchain/caches. Independent roles refreshed their checks for this native architecture; the old source-manifest approvals were not carried forward.

```sh
go test ./builtin/... ./core ./params ./internal/ethapi \
  ./consensus/ethash ./consensus/clique ./consensus/beacon ./miner \
  -run SessionKeys -count=1
go test -race ./builtin/sessionkeys ./core ./internal/ethapi ./params \
  -run SessionKeys -count=1
go test ./params/... -count=1
go test ./core -run 'TestSetupGenesis|TestGenesis|TestReadWriteGenesisAlloc|TestInvalidCliqueConfig' -count=1
go test ./core -run 'TestRestartWithNewSnapshot|TestShortRepair|TestIssue23496|TestShortSetHead' -count=1
go build ./cmd/geth
go build ./examples/sessionkeys
```

Signed execution, actual `BlockChain.InsertChain` reorg/restart, processor/import versus `FinalizeAndAssemble` root parity, pool owner reservations, RPC simulation, genesis paths, recovery and rejection without writes are covered. Consensus/miner focused commands compile those packages; core tests exercise actual finalizers. The final 10-second registry ABI fuzz run completed 167,730 executions without failure. Exact final commands/results are recorded in `session-keys-testing.md`.

Executable-source local smoke evidence was recorded in `/private/tmp/aplo-native-smoke-5oi7osyc/summary.json`. The CLI example generates a fresh session proof and protected raw transactions offline without printing private keys. Local geth initialized its generated genesis in a new temporary directory. A separate actual `--dev` node included an ordinary transaction in block one with successful receipt (`status=0x1`, 21,000 gas); the registry already had nonce one and canonical GAplo was installed. Networking peers were disabled, auth RPC bound only to loopback, and the node exited after the check. This is local devnet evidence, not an existing-network deployment or cross-client conformance test.

## Wider repository limits

The broad affected-package suite and full repository build are not green. Native genesis makes previously empty state roots nonempty; repair, sethead and snapshot generators were fixed to seed their isolated databases with the same genesis. Native genesis/hash/setup/serialization fixtures were updated and their targeted regressions pass.

After those corrections, `core.TestFastVsFullChains` still fails for missing GAplo funds; independent review reproduced the same insufficient-funds failure on the base. Other broad legacy fork-ID, runtime, clique, Ethash and miner failures are described in the independent test record; not every failure has been compared individually against base. Minimal existing compile-fixture repairs are retained. A fresh `go build ./...` still fails at the pre-existing NewEVM argument mismatch in `tests/state_test_util.go`, missing bootnode declarations in devp2p/mobile, and an unused faucet import. The full console suite passes after its faucet/network fixture correction. No broad failure is counted as a passing gate.

| Role | Scope and result | Record |
|---|---|---|
| Review | Native protocol source and targeted regressions; full-suite limitations recorded | session-keys-review.md |
| Guard | Native genesis, authority, consensus/state and ABI checks; compatibility boundaries recorded | session-keys-guard.md |
| Testing | Focused/race/reorg/config/fuzz/build verification; broad failures recorded | session-keys-testing.md |
