# Ordinary consensus restoration candidate — 2026-10-18

This is a separate consensus candidate stacked on RPC-only PR #12, exact base
`0af90c73dbe403e9263b2d2d7d76317ad16144d0`. It restores the three ordinary signed
execution changes identified in the [asset audit](asset-model-audit.md), using
pre-session `0416fd355f53f0d09b256e194d4f44f9bb91663e` as the mandatory oracle.
PR #12's branch and RPC-only scope are not modified.

## Behavior and boundary

| Area | Ordinary signer | Active session signer |
|---|---|---|
| Fee / target access warmth | Fresh declared access list after fee purchase, as pre-session | Existing post-session prepared list retained |
| GAplo affordability | `gasLimit * feeCap + transaction.value` when fee cap is present | Owner GAplo covers fees; owner APLO and native allowance cover value independently |
| Value actually transferred | APLO; the additional GAplo value amount is a reservation, not a value debit | Owner APLO, with signer/nonce belonging to session |
| Zero refund/tip credit to nonzero recipient | Call canonical GAplo, retain historical zero Transfer logs | Existing zero-credit suppression retained |
| Rejected transition | Return real error and roll back state/logs/refund/gas reservation | Same safe rollback and all existing policy checks |
| RPC simulation | Same ordinary target gas and value reservation; no empty fee burn for fake zero-fee messages | Same delegated owner/signer/allowance semantics; no fake bypass |

An owner signing directly remains ordinary even if it has registered sessions.
Inactive/used keys remain rejected. Recipient/tombstone routing, reserved accounts,
signing domains, registration authorization, nonce/expiry/whitelist/allowance
and native genesis validation remain intact. No assets are unified:
`eth_getBalance` still exposes GAplo; native value, proof balances and staking
remain APLO. Zero-beneficiary tips keep their established burn behavior rather
than invoking an impossible canonical mint-to-zero.

`DoEstimateGas` subtracts the ordinary additional GAplo value reservation from its
search ceiling; sessions do not subtract APLO value from GAplo. Target access
cost is no longer underestimated using fee-warmed storage. Omitted/zero sender
calls retain their actual identities, and the target's own takeFee(0) rule still
reverts when explicitly requested from zero.

## Safe access-list reset

Blindly replacing the access list inside the transition snapshot leaves earlier
slot-add journal entries pointing at a different list during rollback. The fix
adds a journal entry for replacement itself in StateDB.PrepareAccessList. Reverse
journal traversal restores the old list before undoing fee-era slot additions.
This changes no trie account/storage representation. Nested reset rollback is
covered independently.

The transition-wide rollback snapshot remains. A fault test explicitly installs
a root-authority proxy at address zero in fixture state, not as a claimed reachable
CREATE attack: the target fills canonical GAplo totalSupply through an authorized
credit, then the transition's final unused-gas credit overflows. The real credit
error propagates, and state root, nonce, logs, refund counter, gas reservation and
outer-snapshot warmth are restored across the target-list reset. Genuine debit
and credit errors are not ignored to reproduce the old implementation's bugs.

## Evidence

The [reproducer](../examples/asset-audit/README.md), invoked with `--expect pre`,
exports exact revision archives, installs the same dedicated probe and checks
byte-identical fixture roots, config, header context, signatures/transaction
bytes and genesis. Production sources of controls are untouched. The old root
core test suite cannot compile because of removed legacy genesis constants;
unrelated root-package tests are isolated in every archive. A passing dedicated
probe is not a full-suite pass.

Forty ordinary vectors cover limited independent APLO/GAplo funds, zero/nonzero
value, exact/insufficient reservations, price/base-fee/tip variants, legacy,
dynamic and access-list transactions, APLO/GAplo reads/transfers, creation,
SELFBALANCE, stake/unstake, owner mining with/without stake, revert/OOG,
intrinsic/nonce/EOA refusal and the active EthPoW DAO-credit branch. Accepted
execution compares gas, actual fee, asset balances, locked stake, supply, nonce,
complete logs/bloom, receipt root and final state root. Pool admission is recorded
separately; native HTTP additionally verifies the BALANCE opcode and historical
proof/account ledgers.

| Historical vector | Pre-session | Merged #10 / RPC-only fix | This candidate |
|---|---:|---:|---:|
| Ordinary GAplo read | 23993 gas | 21993 gas | 23993 gas |
| Ordinary GAplo transfer | 57256 gas | 55256 gas | 57256 gas |
| Value 7, GAplo exactly 200000, gas 100000 × price 2 | rejects (needs 200007) | accepts | rejects safely |
| Zero price/base fee, zero value | 3 GAplo logs | 1 GAplo log | 3 GAplo logs |
| Old 3-block signed chain | imports | rejects gas-used mismatch | imports |
| Old zero-price block | imports | rejects bloom mismatch | imports |
| Candidate vectors imported by pre-session | — | — | both accepted |

The ordinary 3-block vector contains a GAplo read, transfer and access-list value
transaction; its gas vector is `[23993, 57256, 25300]`. A separate zero-base-fee
chain proves receipt/bloom compatibility for zero-valued credits. Each version
imports its own vectors, then cross-version import checks the actual blocks.
Merged #10 remains a negative control. Relevant native session chain import,
reorg/restart and finalizer-parity tests are retained.

Two intentional safety distinctions are reported rather than hidden:

- Invalid standalone pre-session ApplyMessage calls could leave partial fees,
  logs or gas reservations. The candidate returns the same refusal but leaves
  the initial state and gas pool intact, with no surviving logs/refund counter.
- The session-era pool rejects a known underfunded APLO builtin transfer before
  admission; the old pool admitted it and consensus execution produced a failed
  receipt. This stricter pool reservation policy is retained. Direct execution
  of that valid-but-reverting transaction still matches the historical receipt
  and state exactly. Pool policy is not equated with block validity.

The compatibility domain is ordinary historical operations on equivalent native
states with an inert registry, excluding operations whose semantics deliberately
involve session identities/recipients/tombstones or the new registry ABI. Existing
incoming-fund routing for used keys is retained, not erased to mimic a client
that had no sessions. Actual canonical fee operations must succeed: old ignored
fee errors and malformed/fault-injected credit states are not compatibility
promises.

A new disposable native chain passes register → owner stake → session-signed
mine → exact fee/reward/budget → revoke, with replay, wrong target, missing GAplo
and revoked-key rejection. Security/RPC/race and state tests cover expiry, signer
lanes, separate allowances, recipient routing and zero-sender calls. The existing
TestTransactionIndices failure remains a GAplo-unfunded fixture failure on both
the RPC base and candidate; the requested GAplo amount changes by its 1000 value
reservation. Full-repository test health is not claimed.

## Existing-network adoption is not authorized or established

The candidate unconditionally restores ordinary rules within this native-session
codebase; it does not invent an activation height or silently select a fork.
It is incompatible with ordinary block gas/receipt results already produced
under the altered rules of #10. It also retains #10's native genesis/registry
validation, so successful equivalent-genesis historical block vectors do not
prove an arbitrary pre-session datadir can be opened.

Before adoption, operators must establish the actual genesis, canonical history,
and whether any altered-rule #10 blocks exist. If all relevant ordinary history
uses the restored rules and the native-state prerequisites are met, replay and
coordinated deployment still require network-specific validation. If altered-rule
blocks exist, a coordinated explicit fork/activation and history policy is needed;
this candidate is not an in-place upgrade plan. No rewind, migration, existing
chain-data mutation, merge or deployment is performed here.

Real wallet-extension signing remains outside the proved scope: the preceding
audit found no exposed injected provider on the available Chrome origin. Native
HTTP/synthetic signing is not described as an actual extension test. Absence of
that test does not prevent reviewing this consensus candidate.

## Recorded candidate verification

Implementation commit `8fa9399916fe28fa9adc24b582ab38b63723dd75` passed the
committed-source three-version runner with `--expect pre --native`.
[Compact evidence](verification/ordinary-consensus-restoration.json) includes
all 40 case identities/outcomes and both block-vector directions;
[native lifecycle evidence](verification/consensus-native-session.json) records
actual synthetic transactions. Full raw output is retained under
`/private/tmp/aplo-consensus-fix/verified-8fa939991/`.

Fresh checks passed: ordinary/RPC regressions, nested access-list and credit-error
rollback, session/security sweep, race sweep including core/state and RPC, full
core/state, full internal/ethapi and params, actual reorg/restart, node builds
and equivalent-genesis HTTP/native controls. Consensus/miner sweep packages
compile with no matching focused tests; finalizer parity is exercised in core.
The preserved known pool fixture failure is recorded separately, not counted
as a passing full core suite.
