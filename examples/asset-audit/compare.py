#!/usr/bin/env python3
"""Run isolated cross-revision asset probes. Never changes the checkout/datadirs.

A successful exit proves fixture equivalence and candidate/post ordinary parity;
it does not certify historical economic choices or full repository test health.
Use --native only for disposable localhost chains and synthetic public test keys.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--out', type=Path, required=True, help='new output directory')
p.add_argument('--go', default='go')
p.add_argument('--candidate', default='HEAD')
p.add_argument('--native', action='store_true')
a = p.parse_args()
root = a.out.resolve()
root.mkdir(parents=True, exist_ok=False)
env = os.environ.copy()
versions = {'pre': '0416fd355f53f0d09b256e194d4f44f9bb91663e',
            'post': '4d3d3f8c63fb314ec23d7369f687033cdf26ac93',
            'fixed': a.candidate}
commits = {}

def run(cmd, cwd, logfile, extra=None):
    with (root / logfile).open('wb') as log:
        subprocess.run(cmd, cwd=cwd, env={**env, **(extra or {})},
                       stdout=log, stderr=subprocess.STDOUT, check=True)

for name, ref in versions.items():
    commit = subprocess.check_output(['git', 'rev-parse', ref + '^{commit}'], cwd=REPO, text=True).strip()
    commits[name] = commit
    dest = root / name
    dest.mkdir()
    archive = root / (name + '.tar')
    with archive.open('wb') as output:
        subprocess.run(['git', 'archive', commit], cwd=REPO, stdout=output, check=True)
    with tarfile.open(archive) as package:
        for member in package.getmembers():
            if Path(member.name).is_absolute() or '..' in Path(member.name).parts:
                raise ValueError('unsafe archive path')
        package.extractall(dest)
    # Old baseline core tests do not compile (removed genesis constants). Exclude
    # unrelated root-package tests on EVERY revision, preserving production bytes.
    excluded = root / ('excluded-' + name)
    excluded.mkdir()
    for test in (dest / 'core').glob('*_test.go'):
        test.rename(excluded / test.name)
    shutil.copyfile(HERE / 'core_probe_test.go.txt', dest / 'core/asset_comparison_test.go')
    extra = {'APLO_AUDIT_OUT': str(root / ('core-' + name + '.json')),
             'APLO_AUDIT_BLOCK_OUT': str(root / (name + '-block.rlp'))}
    if name != 'pre':
        extra['APLO_AUDIT_BLOCK_IN'] = str(root / 'pre-block.rlp')
    run([a.go, 'test', './core', '-run', '^TestAssetComparisonProbe$', '-count=1', '-v'], dest, 'core-' + name + '.log', extra)
    if a.native:
        run([a.go, 'build', '-ldflags', '-X main.gitCommit=' + commit,
             '-o', str(root / ('aplo-' + name)), './cmd/geth'], dest, 'build-' + name + '.log')

rows = {name: json.loads((root / ('core-' + name + '.json')).read_text()) for name in versions}
for pre, post, fixed in zip(rows['pre'], rows['post'], rows['fixed']):
    if 'initial_root' in pre:
        assert pre['name'] == post['name'] == fixed['name']
        assert pre['initial_root'] == post['initial_root'] == fixed['initial_root']
        assert pre['tx_hash'] == post['tx_hash'] == fixed['tx_hash']
find_block = lambda name: next(row for row in rows[name] if row['name'] == 'signed_block_import')
assert find_block('pre')['genesis'] == find_block('post')['genesis'] == find_block('fixed')['genesis']
assert rows['post'] == rows['fixed'], 'candidate changes ordinary signed semantics relative to post-session'
assert not rows['post'][-1]['accepted'], 'expected cross-version baseline block to expose the known fork'
summary = {'commits': commits, 'equivalent_initial_roots_and_transactions': True,
           'candidate_matches_post_for_ordinary_probes': True, 'rows': rows,
           'scope': 'Dedicated probe only; unrelated legacy core tests excluded on all archives. Not a full-suite result.'}
(root / 'comparison.json').write_text(json.dumps(summary, indent=2) + '\n')
if a.native:
    shutil.copyfile(HERE / 'native_fixture.go.txt', root / 'native-fixture.go')
    run([a.go, 'run', str(root / 'native-fixture.go'), str(root / 'native-fixture')], root / 'fixed', 'fixture.log')
    run(['python3', str(HERE / 'native_compare.py'), str(root)], REPO, 'native-compare.log')
    native = {name: json.loads((root / ('native-' + name + '.json')).read_text()) for name in versions}
    assert len({v['genesis_root'] for v in native.values()}) == 1
    assert len({v['genesis'] for v in native.values()}) == 1
    assert native['pre']['calls'] == native['fixed']['calls']
    assert all(v['simulation_state_unchanged'] for v in native.values())
print(json.dumps({'output': str(root), 'commits': commits, 'native': a.native}, indent=2))
