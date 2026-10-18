import json, pathlib, subprocess, signal, sys, time, urllib.request
ROOT = pathlib.Path(sys.argv[1]).resolve()
F = ROOT / 'native-fixture'
(ROOT / 'loopback.toml').write_text('[Node.P2P]\nListenAddr = "127.0.0.1:0"\nNoDial = true\nNoDiscovery = true\nMaxPeers = 0\n')
ACTORS = json.loads((F / 'actors.json').read_text())
A = {x['name']: x for x in ACTORS}
TOKENS = ['0x0000000000000000000000000000000000001234', '0x0000000000000000000000000000000000001235']
BINARIES = {'pre': str(ROOT / 'aplo-pre'), 'post': str(ROOT / 'aplo-post'), 'fixed': str(ROOT / 'aplo-fixed')}
processes = []

def rpc(port, method, params):
    request = dict(jsonrpc='2.0', id=1, method=method, params=params)
    req = urllib.request.Request(f'http://127.0.0.1:{port}', json.dumps(request).encode(), {'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=8) as result:
        return json.load(result)

def result(port, method, params):
    response = rpc(port, method, params)
    if 'error' in response:
        raise RuntimeError(response)
    return response['result']

def calldata(addr):
    return '0x70a08231' + addr[2:].rjust(64, '0')

def state(port):
    return {a['name']: {'native': result(port, 'eth_getProof', [a['address'], [], 'latest'])['balance'], 'gaplo': result(port, 'eth_getBalance', [a['address'], 'latest']), 'nonce': result(port, 'eth_getTransactionCount', [a['address'], 'latest'])} for a in ACTORS}
try:
    for index, (name, binary) in enumerate(BINARIES.items()):
        port = 19845 + index
        data = ROOT / f'native-{name}'
        data.mkdir(exist_ok=False)
        with (data / 'setup.log').open('wb') as out:
            subprocess.run([binary, '--datadir', str(data / 'data'), 'init', str(F / 'genesis.json')], stdout=out, stderr=subprocess.STDOUT, check=True)
            subprocess.run([binary, '--datadir', str(data / 'data'), 'account', 'import', '--password', str(F / 'password.txt'), str(F / 'synthetic-authority.key')], stdout=out, stderr=subprocess.STDOUT, check=True)
        out = (data / 'node.log').open('wb')
        proc = subprocess.Popen([binary, '--config', str(ROOT / 'loopback.toml'), '--datadir', str(data / 'data'), '--networkid', '424242', '--syncmode', 'full', '--mine', '--miner.etherbase', A['authority']['address'], '--miner.gasprice', '1', '--unlock', A['authority']['address'], '--password', str(F / 'password.txt'), '--allow-insecure-unlock', '--http', '--http.addr', '127.0.0.1', '--http.port', str(port), '--http.api', 'eth,net,web3', '--http.vhosts', 'localhost,127.0.0.1', '--ipcdisable', '--authrpc.port', str(19851 + index), '--maxpeers', '0', '--nodiscover'], stdout=out, stderr=subprocess.STDOUT)
        processes.append((proc, out))
        deadline = time.monotonic() + 20
        while True:
            try:
                result(port, 'eth_chainId', [])
                break
            except Exception:
                if proc.poll() is not None or time.monotonic() > deadline:
                    raise
                time.sleep(0.1)
        genesis = result(port, 'eth_getBlockByNumber', ['0x0', False])
        before = state(port)
        calls = []
        for token in TOKENS:
            for from_label, frm in [('omitted', None), ('zero', '0x' + '0' * 40), ('funded', A['ordinary_transfer']['address'])]:
                for fee_label, fields in [('omitted', {}), ('zero', {'gasPrice': '0x0'}), ('legacy', {'gasPrice': '0x2', 'gas': '0x186a0'}), ('dynamic', {'maxFeePerGas': '0x2', 'maxPriorityFeePerGas': '0x1', 'gas': '0x186a0'})]:
                    for block in ['0x0', 'latest', 'pending']:
                        arg = {'to': token, 'data': calldata(A['ordinary_transfer']['address']), **fields}
                        if frm is not None:
                            arg['from'] = frm
                        response = rpc(port, 'eth_call', [arg, block])
                        calls.append(dict(target=token, sender=from_label, fee=fee_label, block=block, request=arg, response=response))
        estimates = []
        for label in ['ordinary_transfer', 'aplo_only', 'gaplo_only_value', 'exact_fee_plus_value', 'exact_native_value', 'gaplo_only_no_value', 'stake']:
            a = A[label]
            arg = {'from': a['address'], 'to': TOKENS[1] if label == 'stake' else '0x000000000000000000000000000000000000b00b', 'value': '0x0' if label in ['stake', 'gaplo_only_no_value'] else '0x7', 'gasPrice': '0x2'}
            if label == 'stake':
                arg['data'] = '0xa694fc3a' + hex(1000 * 10 ** 18)[2:].rjust(64, '0')
            estimates.append(dict(name=label, request=arg, response=rpc(port, 'eth_estimateGas', [arg, 'latest'])))
        after = state(port)
        assert before == after, 'simulations mutated live state'
        transactions = []
        for label in ['ordinary_transfer', 'aplo_only', 'gaplo_only_value', 'exact_native_value', 'gaplo_only_no_value', 'stake', 'gaplo_transfer', 'dynamic_value', 'zero_fee_value', 'exact_fee_plus_value']:
            a = A[label]
            response = rpc(port, 'eth_sendRawTransaction', [a['raw']])
            row = dict(name=label, response=response)
            if 'result' in response:
                end = time.monotonic() + 4
                while True:
                    rec = result(port, 'eth_getTransactionReceipt', [response['result']])
                    if rec is not None or time.monotonic() > end:
                        break
                    time.sleep(0.1)
                row['receipt'] = rec
            transactions.append(row)
        block = transactions[0]['receipt']['blockNumber']
        owner = A['ordinary_transfer']['address']
        semantics = {}
        for b in ['0x0', block, 'latest', 'pending']:
            semantics[b] = {'eth_getBalance': rpc(port, 'eth_getBalance', [owner, b]), 'eth_getProof': rpc(port, 'eth_getProof', [owner, [], b]), 'GAplo.balanceOf': rpc(port, 'eth_call', [{'from': A['authority']['address'], 'to': TOKENS[0], 'data': calldata(owner)}, b]), 'APLO.balanceOf': rpc(port, 'eth_call', [{'from': A['authority']['address'], 'to': TOKENS[1], 'data': calldata(owner)}, b]), 'BALANCE': rpc(port, 'eth_call', [{'from': A['authority']['address'], 'to': '0x000000000000000000000000000000000000ba1a', 'data': '0x' + owner[2:].rjust(64, '0')}, b]), 'SELFBALANCE': rpc(port, 'eth_call', [{'from': A['authority']['address'], 'to': '0x000000000000000000000000000000000000ba1b'}, b])}
        evidence = dict(version=name, binary=binary, chain_id=result(port, 'eth_chainId', []), genesis=genesis['hash'], genesis_root=genesis['stateRoot'], initial_state=before, simulation_state_unchanged=True, calls=calls, estimates=estimates, transactions=transactions, balance_semantics=semantics, final_state=state(port))
        (ROOT / f'native-{name}.json').write_text(json.dumps(evidence, indent=2) + '\n')
        print(name, 'genesis', genesis['hash'], 'calls', len(calls), 'transactions', len(transactions), flush=True)
finally:
    for proc, out in processes:
        if proc.poll() is None:
            proc.send_signal(signal.SIGINT)
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.terminate()
                proc.wait(timeout=5)
        out.close()
