# Cross-version APLO/GAplo audit

Run from the repository with Python 3 and a working Go toolchain:

```sh
python3 examples/asset-audit/compare.py --out /tmp/aplo-asset-audit-new \
  --go /path/to/go --candidate HEAD --native
```

The output directory must not exist. The runner exports exact pre-session
`0416fd355`, merged `4d3d3f8c6` and candidate commits into that directory. It
never checks out another branch or edits existing chain data. The `.go.txt`
files are Go probe templates, copied into the temporary archives; they are not
part of ordinary `go test ./...` package discovery.

Every revision receives byte-identical fixtures: canonical GAplo code/storage,
registry nonce one (inert in the old node), independent limited APLO/GAplo
balances, configuration, nonce, transactions and beneficiaries. The runner
asserts equal initial state roots, transaction hashes and genesis hashes. The
core probe applies signed transactions through pool validation, ApplyTransaction,
GenerateChain and BlockChain.InsertChain. It exports an actual old-version block
and attempts to import it with the newer versions. Candidate signed execution
must exactly match the merged post-session version for these probes.

Old baseline `core` tests do not compile because they reference removed genesis
constants. The runner moves unrelated root-package `_test.go` files out of EACH
temporary archive and installs only the dedicated probe. Production source bytes
are unchanged. Passing this probe is **not** a baseline/full-suite pass.

`--native` additionally builds the three nodes, constructs one identical custom
Clique genesis (chain ID 424242) with distinct limited-balance synthetic actors,
and runs raw HTTP calls and signed transactions against new localhost chains.
HTTP ports are 19845–19847, authenticated RPC ports 19851–19853; P2P binds only
loopback, with dialing, peers and discovery disabled. The script uses only
public synthetic integer-derived keys, never a user's wallet/keystore. It
shuts down its processes and retains logs/datadirs. Avoid concurrent runs using
these ports. No external RPC or public-chain transaction is used.

`comparison.json`, `core-*.json` and `native-*.json` contain the full observations.
The native matrix includes sender omitted/zero/funded, fee omitted/zero/legacy/
dynamic, genesis/latest/pending, both token reads, estimation, exact-balance value
transfers, APLO-only/GAplo-only funding, staking, token transfer and post-transfer
historical balance/proof/BALANCE/SELFBALANCE checks. Zero-price transactions on
the custom London chain are observed as unmined, not called successful simply
because the RPC accepted their hash.

This checks node requests and native execution. It creates no EIP-1193 adapter
and does not establish real browser-extension compatibility. See the audit report
and the separate consensus decision record for interpretation of differences.
