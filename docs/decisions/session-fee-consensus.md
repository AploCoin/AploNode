# Decision boundary: ordinary transactions after session keys

Status: RPC-only scope approved by the user on 2026-10-18. Retaining or reversing
the consensus differences below is deferred to a separate explicit protocol
change. PR #12 does not activate, reverse or conceal them.

## Proven differences

The controls use the same canonical GAplo runtime, balances, signer, signed
transactions, genesis/config and an inert reserved registry account in all three
versions. This avoids confusing PR #10's different genesis requirements with an
execution comparison. A real signed block is generated and imported by its own
version, then submitted to the other version.

| Ordinary operation | Pre-session `0416fd355` | Merged `4d3d3f8c6` and RPC fix `934f90946` |
|---|---|---|
| GAplo `balanceOf`, signed, price 2, base fee 1 | 23993 gas | 21993 gas |
| GAplo `transfer(10)`, same 100000 gas limit | 57256 gas | 55256 gas |
| GAplo transfer exhausting post-upfront balance | 45824 gas | 44224 gas (refund cap also differs) |
| APLO value 7, APLO 123, GAplo exactly 200000, gas limit 100000, price 2 | upfront rejects: wants 200007 GAplo | accepts: reserves 200000 GAplo; value is APLO |
| APLO value 7, APLO 123, GAplo 0, price/base fee 0 | rejects: wants 7 GAplo | accepts |
| Zero-price/base-fee-zero, zero-value ordinary transfer | 3 GAplo logs | 1 GAplo log (different receipt bloom/root) |
| Old signed GAplo read block, same genesis | imports | rejects `invalid gas used (remote: 23993 local: 21993)` |

Moving `PrepareAccessList` ahead of fee-contract execution preserves warmed
GAplo balance/supply slots into the target call. In the baseline it ran after gas
purchase and cleared this warmth. The change originated in
[`698ec1b72`, ApplyMessage](https://github.com/AploCoin/AploNode/blob/698ec1b7204f28a73a95a0ac1175345c527febe7/core/state_transition.go#L191),
initially under activation gating, and became unconditional in
[`68515adb5`](https://github.com/AploCoin/AploNode/commit/68515adb5023625f1238f09bd623debeccd23c52).
Skipping zero-amount refund/tip calls also removes their zero-valued ERC20 logs
from real receipts. Removing `balanceCheck.Add(balanceCheck, st.value)` likewise became unconditional
in that native-from-genesis change. Both are part of merged #10, not introduced
by the zero-fee RPC guard in #12. Comparing post-session and RPC-fix probes gives
identical signed receipts, roots, fees, balances, errors and pool decisions.

The old extra value check mixed APLO value into GAplo reservation, despite the
documented two-asset model. Its removal agrees with that model but changes which
signed transactions are valid. A behavioral improvement is still a consensus
change; the evidence does not authorize silently reverting or retaining it as
though both versions were chain-compatible.

## Separate protocol choices

1. Retain post-session gas/reservation rules: preserves the merged version's
   validity rules, but does not make it compatible with pre-session blocks.
2. Restore historical access warmth/gas rules: changes used gas, base-fee burn,
   tips, remaining session fee allowances and block roots relative to the merged
   version. Snapshot rollback must remain correct; blindly moving access-list
   preparation back inside a snapshot can invalidate journal rollback.
3. Restore the historical extra GAplo value reservation as well: narrows accepted
   ordinary transactions and reproduces the old APLO/GAplo coupling. This is a
   separate economic choice, not implied by restoring gas accounting.

Any chosen restoration needs its own signed-block vectors, activation or new
network/genesis policy, deployment authority and chain-state assessment. None of
those deployment facts was established here. No existing chain was rewritten.
Restoring ignored fee errors or dropping session validation is not an acceptable
way to recover old RPC behavior.

Invalid standalone ApplyMessage calls in the baseline can leave fee debits/gas
reservation behind before returning a consensus error. New snapshot rollback
prevents this. These probe observations are not proof that a valid old canonical
block committed those failed calls; failed block execution is discarded. Keep
the rollback/security guarantees when considering any separate protocol patch.
