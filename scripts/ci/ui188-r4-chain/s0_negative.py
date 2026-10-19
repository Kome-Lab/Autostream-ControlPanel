"""Real HTTP negatives in this B3 only; response/credential values stay in memory."""
from http.cookiejar import CookieJar
import json
import os
import subprocess
import time
import urllib.error
import urllib.request


def verify_s0(request, client, csrf, credentials, schema, profile, saved, configs, evidence, attempt):
    assert schema == 'ui188_r9_b5' and attempt == 'b5'
    events = []
    checks = []
    profile_path = '/profiles/encoder/' + profile
    update = {'name': 'UI188 rejected mutation sentinel', 'config': saved['config']}
    good_update = {'name': saved['name'], 'config': saved['config']}
    output = evidence / 's0-negative-b3.json'

    def save():
        output.write_text(json.dumps({'run': 'B3', 'schema': schema, 'profile_id': profile, 'events': events, 'checks': checks, 'response_values_retained': False, 'credentials_retained': False}, indent=2) + '\n', encoding='utf-8')

    def http(opener, method, path, body=None, headers=None, expected=200, code=None, label=None):
        h = {'Accept': 'application/json', 'Content-Type': 'application/json', **(headers or {})}
        req = urllib.request.Request('http://127.0.0.1:18841' + path, data=None if body is None else json.dumps(body).encode(), headers=h, method=method)
        try:
            with opener.open(req, timeout=25) as res:
                status = res.status
                data = res.read()
        except urllib.error.HTTPError as res:
            status = res.code
            data = res.read()
        value = json.loads(data) if data else None
        actual_code = value.get('code') if isinstance(value, dict) else None
        events.append({'at': time.time(), 'label': label, 'method': method, 'path': path, 'body_fields': sorted(body or {}), 'cookie_header_recorded': False, 'csrf_header_present': 'X-CSRF-Token' in h, 'authorization_header_present': 'Authorization' in h, 'status': status, 'code': actual_code})
        save()
        assert status == expected and (code is None or actual_code == code), (label, status, actual_code)
        if expected in (401, 403):
            assert isinstance(value, dict) and set(value) == {'code'}, 'rejection exposed unexpected response fields'
        return value

    def sql(statement):
        return subprocess.check_output(['mariadb', '--host=db', '--user=ui188_r9', '--batch', '--skip-column-names', schema, '--execute', statement], env={**os.environ, 'MYSQL_PWD': credentials['db_password']})

    tables = ('profiles', 'secrets', 'runtime_secret_leases', 'stream_service_assignments', 'streams')

    def snapshot():
        # Encoded DB values are compared only in memory, never written or printed.
        raw = {table: sql('SELECT * FROM ' + table + ' ORDER BY 1,2,3') for table in tables}
        counts = {table: int(sql('SELECT COUNT(*) FROM ' + table).strip()) for table in tables}
        assert counts['runtime_secret_leases'] == counts['stream_service_assignments'] == counts['streams'] == 0
        return raw, counts, request('GET', profile_path)

    def negative(label, opener, method, path, body, headers, status, code):
        before, counts, before_profile = snapshot()
        http(opener, method, path, body, headers, status, code, label)
        after, after_counts, after_profile = snapshot()
        assert before == after and counts == after_counts and before_profile == after_profile == saved, label + ': protected state changed'
        checks.append({'label': label, 'status': status, 'code': code, 'protected_tables_unchanged': {table: True for table in tables}, 'row_counts_before': counts, 'row_counts_after': after_counts, 'profile_GET_unchanged': True, 'additional_protected_mutations': 0, 'secret_response_fields': False})
        save()
        assert request('PUT', profile_path, good_update) == request('GET', profile_path)
        # The legitimate same-value PUT may update timestamp; use its DB response
        # as the next baseline, never manufacture profile values.
        return request('GET', profile_path)

    # Normal session refresh precedes all denial checks and keeps the same cookie.
    refreshed = request('POST', '/auth/session/refresh', {})
    assert refreshed['status'] == 'refreshed'
    anonymous = urllib.request.build_opener()
    saved = negative('unauthenticated', anonymous, 'PUT', profile_path, update, {'X-CSRF-Token': csrf}, 401, 'unauthorized')

    secondary_jar = CookieJar()
    secondary = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(secondary_jar))
    login = http(secondary, 'POST', '/auth/login', {'username': 'ui188-r4', 'password': credentials['admin_password']}, label='secondary normal login')
    cookie_req = urllib.request.Request('http://127.0.0.1:18841' + profile_path)
    secondary_jar.add_cookie_header(cookie_req)
    retired_cookie = cookie_req.get_header('Cookie')
    assert retired_cookie and login['csrf_token']
    http(secondary, 'POST', '/auth/logout', {}, {'X-CSRF-Token': login['csrf_token']}, label='secondary normal logout')
    saved = negative('revoked_by_regular_logout', anonymous, 'PUT', profile_path, update, {'Cookie': retired_cookie, 'X-CSRF-Token': login['csrf_token']}, 401, 'unauthorized')
    retired_cookie = None
    saved = negative('missing_CSRF', client, 'PUT', profile_path, update, {}, 403, 'csrf_failed')
    saved = negative('invalid_CSRF', client, 'PUT', profile_path, update, {'X-CSRF-Token': 'B3-intentionally-invalid-CSRF'}, 403, 'csrf_failed')

    token = request('POST', '/api-tokens', {'service_type': 'discord_bot', 'scopes': ['service.config.read']}, 201)
    assert token['token'] and token['scopes'] == ['service.config.read']
    secret_name = 'discord_bot_token_' + configs['a'].strip().lower()
    # The named, configured secret exists in the fresh DB, but this token has
    # no resolve capability. The real middleware rejects before lease claims.
    assert int(sql("SELECT COUNT(*) FROM secrets WHERE name='" + secret_name + "'").strip()) == 1
    saved = negative('secret_resolve_without_scope', anonymous, 'POST', '/services/runtime-secrets/resolve', {'service_id': 'bot-a', 'secret_name': secret_name}, {'Authorization': 'Bearer ' + token['token']}, 403, 'missing_service_scope')
    request('DELETE', '/api-tokens/' + token['id'])
    token = None
    assert len(checks) == 5 and all(row['additional_protected_mutations'] == 0 for row in checks)
    request('GET', '/auth/me')
    save()
    record = json.loads(output.read_text())
    record.update({'aggregate': 'PASS', 'normal_session_refresh': True, 'normal_followup_PUT_GET': True, 'normal_service_secret_followup': 'PENDING_SAME_RUN_S1_S2; no manual successful resolve or lease claim', 'scope_rejection_service_audit': 'No handler audit by design; rejected in authenticateService before resolve handler', 'run_continuation': 'S1 follows in this same runtime process'})
    output.write_text(json.dumps(record, indent=2) + '\n', encoding='utf-8')
