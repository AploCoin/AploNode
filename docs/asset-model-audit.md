# APLO / GAplo model and wallet RPC audit — 2026-10-18

## Outcome and scope decision

The two-asset model predates session keys. APLO is native EVM value and the
staking asset; GAplo pays gas. `eth_getBalance` deliberately exposes GAplo,
while account proofs and EVM BALANCE/SELFBALANCE expose APLO. This is a historical
compatibility convention, not a new regression from #10. Changing balances or
economics to make this look like single-asset Ethereum is outside this RPC fix.

The zero-fee simulation guard in PR #12 remains the minimal correction: an
empty fee purchase must not invoke ERC20 burn validation on the default zero
sender. It is not an exception for a wallet or token address. Actual target
calls and every session check still run; signed transactions retain the merged
version's fee path. Additional generic-contract tests preserve zero CALLER and
ORIGIN and prove that an explicit zero-sender target `takeFee(0)` still reverts.

Comparison also proved independent signed-execution changes in #10. On the same
genesis and signed input, ordinary GAplo reads now cost 21993 rather than 23993
gas, and a pre-session block is rejected with a gas-used mismatch. The user
explicitly chose to keep #12 RPC-only and record the consensus question
separately: [decision boundary](decisions/session-fee-consensus.md).

## Authority and historical evidence

The original Solidity source of canonical GAplo was not found in the checked
repository or the official organization's indexed code search. No formal
complete protocol specification is claimed. Executable evidence comes from the
unchanged canonical runtime in `params.GAPLO`, native builtins, genesis and the
following primary historical sources:

- The official site's December 2024 text states the two-token distinction:
  [mainsite `messages/en.json`, AboutAplo and MiningAplo](https://github.com/AploCoin/mainsite/blob/f1852edb8bc4983099ae50a0b6c193bc46e5bee2/messages/en.json).
  The separate `AploCoin/docs` and old `Node` README files contain only headings.
- [`cacb71caf` — GetBalance(Gaplo), November 2024](https://github.com/AploCoin/AploNode/commit/cacb71caff115a548281139e15df7b8ef396ea45)
  intentionally replaces the native account read with GAplo. This is direct
  historical intent for that RPC; it is not inferred merely from nonstandardness.
- The official site's original network-add metadata is
  [`nativeCurrency: GAPLO`](https://github.com/AploCoin/mainsite/blob/f1852edb8bc4983099ae50a0b6c193bc46e5bee2/src/components/navigation/DesktopNavigation.tsx#L28).
- The mandatory pre-session execution control is
  [`0416fd355`](https://github.com/AploCoin/AploNode/tree/0416fd355f53f0d09b256e194d4f44f9bb91663e),
  not an unrelated Ethereum release. Merged control is `4d3d3f8c6`; RPC-fix
  candidate is `934f90946`. Exact full revisions are in the evidence JSON.

| Surface / operation | Established model and implementation | Classification |
|---|---|---|
| APLO | Trie account balance; native `0x1235 balanceOf` reads StateDB.GetBalance | Original asset rule |
| GAplo | Canonical ERC20 runtime at `0x1234`; balance mapping slot 0, total supply slot 2 | Original gas asset rule |
| `eth_getBalance` | `internal/ethapi/api.go:GetBalance` → GetGaploBalance → direct GAplo balanceOf | Deliberate historical wallet gas convention |
| `eth_getProof.balance` | Account balance proven by account trie: APLO | Original proof semantics; cannot relabel the committed proof as GAplo |
| BALANCE / SELFBALANCE | `core/vm/instructions.go:opBalance`, `core/vm/eips.go:opSelfBalance`: APLO | Original EVM semantics |
| `transaction.value`, CALL value, APLO transfer | Debit APLO; session top-level funding redirects to owner | Original ordinary rule; delegation extension |
| Upfront gas | Burn `gasLimit * effectivePrice` GAplo through takeFee; maximum reservation uses fee cap | Original fee asset; value-inclusive old check differs after #10 |
| Unused gas refund | Root-only `refund(address,uint256)` credits GAplo | Original ordinary rule; owner is session payer |
| London base fee | Net GAplo burn `usedGas * baseFee` for ordinary transactions; tip refunded to beneficiary | Original fee rule, subject to existing EthPoW-fork DAO handling |
| Tip | `usedGas * effectiveTip` GAplo; zero beneficiary receives no mint | Original economic outcome; #10 now propagates genuine credit errors |
| EthPoW DAO handling | Existing fork branch additionally credits native APLO by nonnegative `usedGas * (gasPrice-effectiveTip)` | Historical fork-specific rule; not generalized away |
| Stake / unstake | Native builtin locks/returns APLO and records owner stake | Original rule, unchanged ordinary smoke results |
| GAplo mining | GAplo `mine(bytes32)` plus transition mining refund gated by payer's APLO stake | Original ordinary rule; session signer mines, owner stake/refund used |
| Mining refund | `usedGas * min(price,baseFee) / 66`, then actual multiplier/10; when beneficiary differs, also refund the base-fee component | Existing code formula retained; nil baseFee follows price branch |
| Stake multiplier | Actual pre/post code: 1000 APLO → 11/10; caps at 17/10 by 7000 APLO | Historical executable rule; adjacent comments claiming 10/10 at 1000 and max at 8000 are stale |
| PoW block rewards | `consensus/ethash/consensus.go:accumulateRewards` calls native AddBalance | APLO; distinct from GAplo share/refund rewards |
| Owner/session | Caller=session, origin=owner; nonce belongs to signer, APLO and GAplo budgets separate; target/selector/expiry validated | Authorized delegation semantics; no fake-message bypass |

Owning pre-session sources:
[`core/state_transition.go`](https://github.com/AploCoin/AploNode/blob/0416fd355f53f0d09b256e194d4f44f9bb91663e/core/state_transition.go),
[`core/genesis.go`](https://github.com/AploCoin/AploNode/blob/0416fd355f53f0d09b256e194d4f44f9bb91663e/core/genesis.go),
[`builtin/aplo/aplo.go`](https://github.com/AploCoin/AploNode/blob/0416fd355f53f0d09b256e194d4f44f9bb91663e/builtin/aplo/aplo.go),
[`internal/ethapi/api.go`](https://github.com/AploCoin/AploNode/blob/0416fd355f53f0d09b256e194d4f44f9bb91663e/internal/ethapi/api.go).

## Equivalent-state comparison

[Reproducer](../examples/asset-audit/README.md) exports exact revisions into new
temporary archives and leaves the checkout and all existing chain data alone.
The same canonical code, explicit registry reservation, configuration, nonce,
limited independent asset balances and signed transaction bytes are used. It
asserts initial state roots and transaction hashes, not merely similarly named
`--dev` chains. Native HTTP runs use exactly the same custom Clique genesis.

The old root `core` tests cannot compile due to removed legacy genesis constants.
Unrelated `_test.go` files are excluded from **all three archives** and replaced
by the same dedicated probe, without changing production files. These are
controlled probes, not evidence that the old or full suite is green.

Core controls cover 18 ordinary cases per revision: transfer, independent
insufficiency of each asset, GAplo-only zero-value calls, exact balances, token
reads/transfers, stake, insufficient stake, zero price, target revert and short
intrinsic gas, zero-priced zero-value receipts, and the active EthPoW DAO-credit
branch. The DAO native credit remains 21000 in all three corresponding controls;
zero-priced real receipts have three old GAplo logs versus one after #10. Pool admission, ApplyTransaction, receipts, nonce, balances,
supply, gas pool and roots are recorded. Each version imports its own real
signed block. The new versions reject the pre-session block despite identical
genesis, proving an execution-rule difference rather than a genesis artifact.
All ordinary post-session and RPC-fix results match exactly in this matrix.

Native controls include legacy and dynamic-fee signed transactions, staking,
GAplo transfer and separate limited assets. A hash returned for a zero-price
transaction that remains unmined on a London chain is recorded as unmined,
not as successful execution. Pool admission and consensus acceptance differ:
the old exact-GAplo/value transaction can enter the pool but cannot execute.

The 72-call HTTP matrix per version covers two tokens, three sender shapes
(omitted/zero/funded), four fee shapes (omitted/zero/legacy/dynamic), and
actual genesis/latest/pending selection. Pre-session and RPC-fix responses
match exactly: 48 successful, 24 correctly refused paid zero-sender calls.
Merged #10 instead gives 24 successes and 48 failures, due to the extra
zero-fee/zero-sender failure. No live balances or nonces change under these
calls/estimates. Post-transfer historical proofs and BALANCE/SELFBALANCE
confirm the two different asset ledgers across all three versions.

Old standalone invalid ApplyMessage probes can leave a fee reservation/debit
before returning a consensus error; #10 rolls it back. This does not prove
failed calls were committed to the old canonical chain. The new rollback and
fee-error checks must be retained; recovering the RPC convention does not
justify restoring blanket ignored errors.

See [machine-readable summary](verification/asset-model-comparison.json) and the
raw paths listed there. A fresh synthetic `--dev` lifecycle also repeated
registration → owner stake → session-signed mine → exact reward/base-fee/budget
checks → revoke. Replay, wrong target, ordinary missing GAplo and post-revoke
transactions were rejected. Separate session/security/race checks pass,
including expiry, owner/signer, nonce and both allowance rules.

## Why the zero-fee boundary is correct

`ToMessage` represents simulation using IsFake; missing fee fields produce zero
purchased fee and missing sender means the actual zero address. An empty fee
purchase has no token amount to debit. Calling burn(0) adds an irrelevant ERC20
sender restriction before the requested contract runs. Skip this only for fake
zero-fee messages. All fee-cap/funding, value, intrinsic gas and session policy
checks still run; explicit nonzero simulated fees still use takeFee and return
its errors. Default no-fee reads need no funded artificial sender.

Skipping zero burns for real messages as well could change logs, warmed slots,
gas use and roots, even when token balances do not change. `Transaction.AsMessage`
sets IsFake=false. The current guard therefore preserves the merged chain's
signed rules. Default zero-fee estimation can conservatively include cold-access
costs that a real paid transaction warms during fee purchase; this is not a
reason to change signed execution here.

The new arbitrary-target test returns zero CALLER, zero ORIGIN and the target's
APLO SELFBALANCE without using a substitute sender. An explicit target call to
GAplo takeFee(0) still returns its genuine zero-address burn revert. This
separates an empty **fee purchase** from the contract operation requested by the
caller, and rules out a target-address or blanket simulation bypass.

## Wallet compatibility and remaining limits

[Ethereum JSON-RPC](https://ethereum.org/developers/docs/apis/json-rpc/) describes
one native ledger for eth_getBalance/value, and
[EIP-3085](https://eips.ethereum.org/EIPS/eip-3085) provides one nativeCurrency
metadata object. Aplo's gas/value split is a historical compatibility compromise.
The official site's GAPLO metadata matches the gas balance/fee display. Current
WebMiner [`src/features/webminer/config.ts:4` at `52328f2fb`](https://github.com/AploCoin/WebMiner/blob/52328f2fb97d48e3e5ce3b66ed58c4a29598f820/src/features/webminer/config.ts#L4) says nativeCurrency APLO, which
mislabels the GAplo balance and gas amounts exposed through this convention.
WebMiner was read only and was not changed by this audit.

Changing the label to GAPLO would not by itself establish universal wallet
compatibility: a generic wallet's native-send form can still interpret `value`
as the displayed gas asset, whereas this node transfers APLO value. This is an
inference from the proved RPC/transaction semantics, not an observed extension
transaction. APLO's token interface at 0x1235 provides a separate explicit asset
surface, but wallet/native-send UX still needs real implementation testing.
Changing eth_getBalance to APLO would instead misrepresent gas funding and break
the historical convention. No such silent redesign is included.

In the available Chrome profile, a new local page at http://127.0.0.1:19880
observed `window.ethereum` absent, no EIP-6963 announcements and an empty public
provider probe. This proves no usable provider was exposed on that origin,
not that no extension is installed. A privileged extension inventory URL was
blocked by browser policy; no privileged workaround was used. No accounts,
signatures, network changes, extension installation or public transactions were
requested. No EIP-1193 adapter is presented as an actual extension test.

Remaining extension verification: use an actual enabled wallet with a fresh
synthetic account on a disposable local chain; check token discovery with omitted
from, gas and asset labels, independent APLO/GAplo insufficiency, contract stake,
fee estimation/signing, receipts, session registration/mining/revoke and the
native-send value display. This audit proves the node request/execution behavior
only. It does not claim all Ethereum wallets support the two-asset model.
