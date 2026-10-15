"""Offline client contract checks; run only on the authorized remote host."""
import unittest
import importlib.util
import hashlib
import hmac
from pathlib import Path

spec = importlib.util.spec_from_file_location('aksk_vpc_client', Path(__file__).resolve().parents[1] / 'ops/aksk_vpc_client.py')
client = importlib.util.module_from_spec(spec)
spec.loader.exec_module(client)


class SigningClientTests(unittest.TestCase):
    def test_fixed_vector(self):
        _, headers = client.sign_vpc('ak-doc-example', 'sk-doc-example-not-a-real-secret', 'vpc_0123456789abcdef0123456789abcdef', 1700000000)
        self.assertEqual(headers['X-Signature'], '7df61f06b799ae477b51975097825e2e154ff62dffc46842964a51ed1afaebeb')
        self.assertNotIn('sk-doc-example-not-a-real-secret', str(headers))

    def test_invalid_inputs(self):
        valid = ['ak-example', 'sk-example', 'vpc_' + 'a' * 32, 1700000000]
        for index, value in [(0, 'bad'), (1, ' sk-example'), (2, 'vpc_' + 'A' * 32), (3, '01700000000'), (3, 0), (3, 2**63)]:
            args = valid.copy()
            args[index] = value
            with self.assertRaises(ValueError):
                client.sign_vpc(*args)

    def test_http_opt_in_and_origin_validation(self):
        import os
        from unittest.mock import patch
        with patch.dict(os.environ, {}, clear=True):
            for origin in ['http://example.test', 'https://user:password@example.test', 'https://example.test/api/v1', 'https://example.test?x=1', 'https://example.test/#frag']:
                with self.assertRaises(ValueError):
                    client.get_vpc(origin, 'ak-example', 'sk-example', 'vpc_' + 'a' * 32)

    def test_redirects_refused(self):
        self.assertIsNone(client.NoRedirect().redirect_request(None, None, 302, 'Found', {}, 'https://other.test'))

    def test_write_body_and_sorted_query_signature(self):
        body = b'{"name":"demo","idempotency_key":"create-vpc"}'
        target, headers = client.sign_network('ak-example', 'sk-example', 'POST', '/api/v1/networks/vpcs', body=body, timestamp=1700000000)
        self.assertEqual(target, '/api/v1/networks/vpcs')
        canonical = '\n'.join(['ANI-HMAC-SHA256', 'POST', target, '', 'ak-example', '1700000000', hashlib.sha256(body).hexdigest()])
        self.assertEqual(headers['X-Signature'], hmac.new(b'sk-example', canonical.encode(), hashlib.sha256).hexdigest())
        self.assertEqual(headers['Content-Type'], 'application/json')
        changed = client.sign_network('ak-example', 'sk-example', 'POST', target, body=b'{ "name":"demo","idempotency_key":"create-vpc"}', timestamp=1700000000)[1]
        self.assertNotEqual(headers['X-Signature'], changed['X-Signature'])
        target, headers = client.sign_network('ak-example', 'sk-example', 'GET', '/api/v1/networks/subnets', {'name': 'a b/c', 'limit': 1}, timestamp=1700000000)
        self.assertEqual(target, '/api/v1/networks/subnets?limit=1&name=a+b%2Fc')
        canonical = '\n'.join(['ANI-HMAC-SHA256', 'GET', '/api/v1/networks/subnets', 'limit=1&name=a+b%2Fc', 'ak-example', '1700000000', hashlib.sha256(b'').hexdigest()])
        self.assertEqual(headers['X-Signature'], hmac.new(b'sk-example', canonical.encode(), hashlib.sha256).hexdigest())

    def test_exact_all_network_routes(self):
        routes = [
            ('POST', '/api/v1/networks/vpcs'), ('GET', '/api/v1/networks/vpcs/vpc_'+'a'*32),
            ('GET', '/api/v1/networks/vpcs'), ('DELETE', '/api/v1/networks/vpcs/vpc_'+'a'*32),
            ('GET', '/api/v1/networks/operations/01234567-89ab-cdef-0123-456789abcdef'),
            ('POST', '/api/v1/networks/subnets'), ('GET', '/api/v1/networks/subnets/subnet_'+'a'*32),
            ('GET', '/api/v1/networks/subnets'), ('DELETE', '/api/v1/networks/subnets/subnet_'+'a'*32),
            ('POST', '/api/v1/networks/eips'), ('GET', '/api/v1/networks/eips/eip_'+'a'*32),
            ('GET', '/api/v1/networks/eips'), ('DELETE', '/api/v1/networks/eips/eip_'+'a'*32),
            ('GET', '/api/v1/networks/vpcs/vpc_'+'a'*32+'/snat'),
            ('POST', '/api/v1/networks/vpcs/vpc_'+'a'*32+'/snat/bindings'),
            ('GET', '/api/v1/networks/snat/bindings/snat_'+'a'*32),
            ('PATCH', '/api/v1/networks/snat/bindings/snat_'+'a'*32),
            ('DELETE', '/api/v1/networks/snat/bindings/snat_'+'a'*32),
            ('POST', '/api/v1/networks/load-balancers'), ('GET', '/api/v1/networks/load-balancers/lb_'+'a'*32),
            ('GET', '/api/v1/networks/load-balancers'), ('PATCH', '/api/v1/networks/load-balancers/lb_'+'a'*32),
            ('DELETE', '/api/v1/networks/load-balancers/lb_'+'a'*32),
            ('GET', '/api/v1/networks/load-balancers/operations/01234567-89ab-cdef-0123-456789abcdef'),
        ]
        self.assertEqual(len(routes), 24)
        for method, path in routes:
            with self.subTest(method=method, path=path):
                body = b'{}' if method in ('POST', 'PATCH') else b''
                target, headers = client.sign_network('ak-example', 'sk-example', method, path, body=body, timestamp=1700000000)
                self.assertEqual(target, path)
                self.assertRegex(headers['X-Signature'], r'^[a-f0-9]{64}$')

    def test_unsupported_or_ambiguous_requests(self):
        calls = [
            ('GET', '/api/v1/networks/vpcs?limit=1', {}, b''),
            ('POST', '/api/v1/networks/vlans', {}, b'{}'),
            ('GET', '/api/v1/networks/vpcs/vpc_'+'A'*32, {}, b''),
            ('GET', '/api/v1/networks/vpcs', {'tenant_id': 'foreign'}, b''),
            ('GET', '/api/v1/networks/vpcs', {'limit': [1, 2]}, b''),
            ('GET', '/api/v1/networks/vpcs', {'limit': '01'}, b''),
            ('GET', '/api/v1/networks/vpcs', {'limit': 101}, b''),
            ('GET', '/api/v1/networks/vpcs', {}, b'{}'),
            ('DELETE', '/api/v1/networks/vpcs/vpc_'+'a'*32, {}, b'{}'),
            ('POST', '/api/v1/networks/vpcs', {}, b'{"name":"a","name":"b"}'),
            ('POST', '/api/v1/networks/vpcs', {}, b'[]'),
            ('POST', '/api/v1/networks/vpcs', {}, b''),
        ]
        for method, path, query, body in calls:
            with self.subTest(method=method, path=path, query=query):
                with self.assertRaises(ValueError):
                    client.sign_network('ak-example', 'sk-example', method, path, query, body, 1700000000)

    def test_page_signature_and_validation(self):
        target, headers = client.sign_network('ak-example', 'sk-example', 'GET', '/api/v1/networks/vpcs', {'pageSize': 2, 'page': 3}, timestamp=1700000000)
        self.assertEqual(target, '/api/v1/networks/vpcs?page=3&pageSize=2')
        changed = client.sign_network('ak-example', 'sk-example', 'GET', '/api/v1/networks/vpcs', {'pageSize': 2, 'page': 2}, timestamp=1700000000)[1]
        self.assertNotEqual(headers['X-Signature'], changed['X-Signature'])
        for query in ({'page': 0}, {'page': '01'}, {'page': 4294967296}, {'pageSize': 101}, {'page': 1, 'cursor': 'old'}, {'limit': 1}):
            with self.assertRaises(ValueError):
                client.sign_network('ak-example', 'sk-example', 'GET', '/api/v1/networks/vpcs', query, timestamp=1700000000)

    def test_direct_get_response(self):
        from unittest.mock import patch
        vpc_id = 'vpc_'+'a'*32
        with patch.object(client, 'request_network', return_value={'id': vpc_id, 'name': 'demo'}):
            self.assertEqual(client.get_vpc('https://example.test', 'ak-example', 'sk-example', vpc_id)['id'], vpc_id)


if __name__ == '__main__':
    unittest.main()
