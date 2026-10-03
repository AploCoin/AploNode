# Protocol-native Session Keys

This implementation follows the three-page `session_keys_design.pdf`. The earlier 17-page concept has conflicting caller semantics. At the top call `msg.sender` is the session signer and `tx.origin` is its owner. Nested calls, proxies and delegatecalls retain ordinary EVM caller semantics. A target whitelist constrains only the top contract and exact four-byte selector. Contracts must not treat `tx.origin` alone as owner authorization.

## Native genesis and supported assets

Session Keys are a permanent part of Aplo. There is no Session Keys activation field, runtime flag, activation transaction or first-block initializer. `Genesis.ToBlock`, `Genesis.Commit` and `SetupGenesisBlock` use the same allocation normalization: the reserved, code-free registry at 0x1237 has nonce one and empty storage; GAplo at 0x1234 has exactly the canonical `params.GAPLO` runtime. The account nonce keeps the empty registry alive through EIP161. No initialization storage marker is used.

A missing registry/GAplo allocation is supplied automatically without modifying the caller's allocation map. Explicit registry code, storage or a nonce above one is rejected. Explicit GAplo code must match the canonical runtime; token storage requires that runtime to be supplied explicitly. A fresh native genesis requires a nonnegative uint256 chain ID, EIP155 at block zero, and a valid alternate chain ID if the existing EthPoW fork is scheduled. Unrelated historical fork schedules and fork-ID machinery remain intact; Session Keys add no fork-ID height. Historical Ethereum genesis configurations that postpone EIP155 are not supported native Aplo genesis configurations.

Native dust explicitly allocated to the reserved registry is preserved when its nonce is normalized to one; the reserved account cannot act as an ordinary EOA. Do not allocate spendable user funds to it. Ropsten and Rinkeby presets delay EIP155 and are rejected; the supported default Aplo, Goerli and Sepolia presets have new native genesis hashes rather than historical Ethereum identities.

Primary chain ID is immutable once blocks have been imported, independently of EIP158. The existing EthPoW fork selects and locks the alternate domain when it takes effect. State validation is read-only: execution, mining and import reject missing registry/canonical GAplo state instead of creating it. Startup rejects any old database whose genesis lacks these accounts. Genesis-state recovery verifies the persisted allocation against the original header root before writing it. This release intentionally changes fresh genesis identity and provides no old-chain migration. Recreate a disposable devnet using a fresh data directory; retain any needed old data and keys. No code path deletes an existing data directory.

`geth --dev` funds its ordinary EOA faucet separately with APLO and canonical GAplo, so its first protected transaction can pay fees. Reserved faucet addresses are rejected. The example genesis likewise prepares the protocol accounts and funds its owner. 0x1235 APLO and 0x1236 oracle retain their existing addresses.

Expiry is inclusive: finalizers pay rewards and then remove the current absolute-height bucket, so a session is usable in its last block. Revoke removes authorization immediately. Keys cannot be reused or resumed. Permanent used-key owner tombstones redirect later credits and prevent revoked/expired keys from sending ordinary EOA transactions.

Native top value and APLO builtin transfer debit owner funds and the APLO allowance. GAplo transaction fees debit owner GAplo and the separate gas allowance. Native AddBalance covers ordinary transfer, builtin credit, internal CALL, SELFDESTRUCT, fee DAO credit and consensus rewards. Canonical GAplo transfer/transferFrom/refund credit recipients are normalized to owner in EVM CALL dispatch; storage layout and deployed runtime remain intact, so no token-state migration is needed. Root refund authority stays with zero caller. GAplo mining metadata remains attached to the caller; protocol staking multiplier and resulting reward use owner. Rewards/refunds never renew allowances. `balanceOf(session)` reports its actual zero token balance; query the owner for spendable funds.

Other token contracts own their storage; arbitrary ERC20/ERC721 balances are not redirected. Session staking calls retain session caller identity and do not spend owner stake or native funds. Native APLO and GAplo funding checks are separate for all transactions; fee-contract errors invalidate the transaction atomically; zero-address tips burn instead of attempting prohibited GAplo mint-to-zero. The repository's existing `eth_getBalance` RPC reports GAplo; this patch preserves that API convention. APLO.balanceOf and account state report native APLO.

## ABI

All state mutations require a direct zero-value EOA-signed transaction. Sessions, nested calls, CALLCODE, DELEGATECALL and STATICCALL cannot create/revoke. `getSession` supports read-only use. Canonical ABI encoding is mandatory.

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

The ABI payload uses the first six creation arguments, including ordered selectors; every field is bound. The effective chain ID is ChainID, or ChainID_ALT after the existing EthPoW fork. This is a chain-ID replay domain, not a unique genesis identity: networks intentionally sharing an ID can replay the acceptance proof, just as they can replay EIP155 transactions. The owner must still sign the direct registration transaction. The proof is mandatory, cannot authorize revoke, and cannot make an indirect registry call valid. It extends the PDF's creation ABI to close address squatting; clients must use this seven-argument version. `sessionkeys.ProofHash` and the example provide exact signing bytes.

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

## Local example

`examples/sessionkeys` generates a random session key in local process memory, signs its acceptance proof, signs registration with a development owner key supplied locally through `APLO_OWNER_KEY`, and signs a session call at nonce zero. It prints the two raw transactions and public session address, never private keys. For later use persist the session private key in the client's secure keystore; this example discards it on exit.

```
go run ./examples/sessionkeys -genesis /tmp/aplo-session-dev.json -owner YOUR_DEV_OWNER_ADDRESS
go build -o /tmp/aplo-geth ./cmd/geth
/tmp/aplo-geth --datadir /tmp/aplo-session-chain init /tmp/aplo-session-dev.json
# Start this isolated local node with its normal mining/account configuration.
# Set APLO_OWNER_KEY securely in your local shell, then:
go run ./examples/sessionkeys -owner-nonce 0 -block 1 -expiry 100
```

The genesis generator uses chain ID 424242, enables EIP155 at genesis, funds the requested owner with native APLO and canonical GAplo (including totalSupply), and deploys a target returning origin/caller. Submit registration using eth_sendRawTransaction and wait for its successful receipt before submitting the session call. Gas/budgets are illustrative dev units. The example writes an isolated genesis and signs offline; it does not deploy a node or broadcast transactions.

See `session-keys-design.md` for requirement decisions and metering; independent role reports record tested snapshots and any verification limits.
