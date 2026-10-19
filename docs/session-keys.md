# Native Session Keys

## Execution and funding

Registry `0x1237` delegates one target and exact four-byte selectors to a fresh secp256k1 key. At the top call, `CALLER` is the session signer and `ORIGIN` is its owner. Nested CALL/DELEGATECALL and constructors retain ordinary EVM context. The whitelist constrains the top call only; applications must not authorize owner actions solely through `tx.origin`.

Owner APLO funds top-level native value and explicit APLO builtin transfers; their successful amounts consume the APLO allowance. Owner GAplo funds transaction fees independently: validate `gasLimit * feeCap`, purchase at effective price, refund unused gas, and charge the remaining session budget by actual `usedGas * effectivePrice`. Revert/OOG undo target value and transfer spending while retaining signer nonce and actual fee. Rewards and incoming credits never replenish allowances. Session staking remains keyed to the session caller and cannot spend owner stake. GAplo mining metadata also belongs to the caller; staking multiplier and protocol mining reward use the owner.

Each session uses its own standard uint64 account nonce, exposed as uint256 by the ABI. Expiry is inclusive: finalizers pay rewards then clear that height's expiry bucket. Revoke removes authorization immediately. Permanent owner tombstones prevent reuse or ordinary transactions from inactive keys and redirect later native APLO and canonical GAplo transfer/transferFrom/refund credits to the owner, including CALL, SELFDESTRUCT and consensus rewards. Arbitrary ERC20/ERC721 storage is not redirected. `balanceOf(session)` reports actual balances; query the owner for spendable funds.

Registry mutation requires a direct, zero-value, replay-protected transaction from an ordinary EOA owner. The immediate caller must equal origin at depth zero and have no code or session tombstone. Nested calls, session callers, CALLCODE, DELEGATECALL and STATICCALL cannot mutate the registry; STATICCALL supports its view. Validation occurs during block execution as well as pool admission. A policy-invalid transaction invalidates its block; an application/registry execution revert may produce a failed receipt with actual gas charged.

CREATE/CREATE2 cannot deploy at the registry or a used-key address. A signed registration increments the owner's nonce, so a later CREATE2 cannot replace that owner with code; contracts cannot register through owner origin. Whitelists bind address/selector, not runtime code or proxy implementation. Counterfactual targets and later target upgrades/redeployment remain authorized until revocation; the owner must trust that target's administration.

## Native genesis and signing domains

Genesis normalization reserves the code-free registry with nonce one and empty storage and installs exactly canonical `params.GAPLO` at `0x1234`. It leaves caller-owned allocations unchanged. Registry code/storage or nonce above one is rejected; explicit GAplo storage requires explicit canonical runtime. Registry dust is preserved but is not spendable by an ordinary EOA. APLO `0x1235` and oracle `0x1236` keep their addresses. The default Aplo genesis includes their builtin stubs; `--dev` separately funds its ordinary faucet with APLO and GAplo.

A native genesis requires EIP155 at block zero, a nonnegative uint256 primary chain ID and a valid alternate ID if EthPoW is scheduled. Primary ID is immutable after imported blocks; the existing EthPoW fork selects and locks `ChainID_ALT`. Session keys introduce no new activation height or fork-ID field. Historical presets delaying EIP155 are unsupported native Aplo genesis configurations.

Execution, import and mining validate native protocol state without initializing it. Startup rejects incompatible old genesis state; genesis-state recovery verifies the persisted allocation against the original header root before writing. There is no old-chain migration. Use a fresh data directory for disposable native chains and retain existing data/keys. Supported fresh genesis identities differ from historical non-native identities.

The existing `eth_getBalance` API reports GAplo. APLO.balanceOf, account proofs and native EVM balance opcodes report APLO. Assets remain separate. Canonical fee errors invalidate transactions atomically; zero-beneficiary tips burn because GAplo prohibits mint-to-zero.

## ABI

Registry mutations require a direct zero-value EOA-signed transaction. Sessions, nested calls, CALLCODE, DELEGATECALL and STATICCALL cannot create/revoke. `getSession` supports read-only use. Canonical ABI encoding is mandatory.

```
CreateSessionKey(address key,address target,bytes4[] selectors,
                 uint256 aplo,uint256 gaplo,uint256 expiry,bytes proof)
RevokeSessionKey(address key)
getSession(address key) returns(address owner,address target,
                               uint256 aplo,uint256 gaplo,
                               uint256 nonce,uint256 expiry)
SessionKeyCreated(address indexed owner,address indexed key,
                  address target,uint256 aplo,uint256 gaplo,uint256 expiry)
SessionKeyRevoked(address indexed owner,address indexed key)
```

Creation requires proof of possession signed by the session key. This prevents an owner from registering somebody else's fresh address and redirecting its future credits. The 65-byte secp256k1 signature is `r || s || v`, with low-S and v=0/1, over:

`keccak256("APLO_SESSION_KEYS_ACCEPT_V1" || registry[20] || effectiveChainId[32] || owner[20] || abi.encode(key,target,selectors,aplo,gaplo,expiry))`

The ABI payload uses the first six creation arguments, including ordered selectors; every field is bound. The effective chain ID is ChainID, or ChainID_ALT after the existing EthPoW fork. This is a chain-ID replay domain, not a unique genesis identity: networks intentionally sharing an ID can replay the acceptance proof, just as they can replay EIP155 transactions. The owner must still sign the direct registration transaction. The proof is mandatory, cannot authorize revoke, and cannot make an indirect registry call valid. It extends the PDF's creation ABI to close address squatting; clients must use this seven-argument version. `sessionkeys.ProofHash` provides the exact signing bytes.

Nonce is the standard uint64 transaction/account nonce, exposed in a uint256 ABI word; exhaustion rejects, and never-used key registration begins at zero. Owner address is not itself a used session. Zero/self, used/funded/nonce-used/contract and reserved key addresses are rejected. Selectors must be unique (1..32); each owner has at most 64 live sessions and each expiry has at most 32 entries. Expiry must satisfy current<=expiry<=current+1000 without overflow. There is no extension, top-up or key reuse. CREATE transactions, short calldata and wrong target/selector are invalid for session signers.

Creation costs 400,000 + 25,000 per selector, revoke 300,000, view 100,000, plus intrinsic transaction/call costs. Registration prepays bounded future cleanup. Cleanup emits no expiry event; query current registry state. Successful registration/revocation emits the above events, reverted execution emits none.

## Storage layout

All registry storage lives in 0x1237 using:

`keccak256("aplo.sessionkeys.v1/" + kind || address[20] || index[32, big endian])`

| kind | address / index | value |
|---|---|---|
| used | key / 0 | permanent owner tombstone |
| owner,target | key / 0 | active owner/target |
| aplo,gaplo | key / 0 | remaining uint256 budgets |
| expiry,selectors | key / 0 | expiry / selector count |
| selector | key / ordinal | bytes4 left aligned |
| ownerCount,ownerList | owner / 0 or ordinal | count / key |
| ownerIndex | key / 0 | index in owner's packed list |
| bucketCount,bucketList | uint64 expiry encoded as address / ordinal | count / key |
| bucketIndex | key / 0 | index in expiry packed list |

Packed-list swap removal updates the moved key's index atomically. Expiry/revoke erase active fields and both list entries, retaining tombstone and account nonce. All writes use StateDB storage journaling, trie commit, Copy, Snapshot and Revert. Native recipient routing is unconditional and reads the permanent owner tombstone from trie state.

## Ordinary execution and compatibility

An owner signing directly remains an ordinary signer before and after registering sessions. Ordinary target execution starts with a fresh declared access list after fee purchase. Zero-valued canonical refunds/tips to nonzero recipients retain their historical Transfer events. APLO transfer preserves an unchanged recipient ABI word in its event; only actual used-key redirection rewrites it. Ordinary zero-value CALLs to absent legacy builtin accounts keep the historical EIP158 no-op. Delegated execution can dispatch native builtins without a deployed stub. A custom/dev genesis that needs ordinary builtin operations should include those builtin accounts, as the default Aplo genesis does.

A rejected transition restores state, logs, refunds and gas reservations, including failures in canonical fee debits or credits. Pool policy additionally reserves cumulative owner funds and rejects known underfunded APLO builtin transfers; this does not redefine block validity. Fee errors are never ignored. Zero-cost RPC simulations skip the protocol fee burn without changing the requested sender, CALLER or ORIGIN. Target calls and session policy still execute normally. `estimateGas` observes ordinary value-inclusive GAplo reservation and independent session budgets.

Restoring these ordinary rules is incompatible with ordinary blocks already produced under the changed gas/receipt rules of the original native-session release. Equivalent-state regression vectors do not establish old-datadir compatibility or an existing-network upgrade. Before adoption, establish the actual genesis and canonical history; if changed-rule blocks exist, a separately coordinated activation/history policy is required. This code supplies no migration, rewind or automatic activation.

## Local verification

Permanent regressions live in normal Go test packages. A focused check is:

```sh
GOMAXPROCS=2 go test -p 2 ./core/state ./builtin/sessionkeys ./core ./internal/ethapi ./params \
  -run 'Ordinary|PrepareAccessListResetRollback|SessionKeys|RegistrySessionKeys|ZeroFee' -count=1
```

This focused command is not a full-suite certification. Use disposable local chains and synthetic keys for client integration checks; synthetic signing does not establish actual wallet-extension behavior.
