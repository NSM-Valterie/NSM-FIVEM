"""Run with: python -m unittest discover -s tools/masterlist -v"""
import http.client
import json
import threading
import unittest
from http.server import ThreadingHTTPServer
import masterlist as m


class EncodingTests(unittest.TestCase):
    def test_negative_varint(self):
        self.assertEqual(m._varint(-1), b'\xff' * 9 + b'\x01')

    def test_integer_bounds(self):
        for value in (-(1 << 63) - 1, 1 << 64):
            with self.assertRaises(ValueError):
                m._varint(value)

    def test_default_game(self):
        self.assertEqual(m.server_data_from_heartbeat({})['vars']['gamename'], 'gta5')


class HTTPTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(('127.0.0.1', 0), m.Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def request(self, method, path, body=None, headers=None):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        try:
            conn.request(method, path, body, headers or {})
            response = conn.getresponse()
            return response.status, response.read()
        finally:
            conn.close()

    def test_invalid_heartbeats(self):
        for body in ([], 1, {'fallbackData': 7},
                     {'fallbackData': {'dynamic': [] , 'info': 4}},
                     {'fallbackData': {'info': {'resources': [4]}}},
                     {'fallbackData': {'dynamic': {'hostname': 4}}},
                     {'ipOverride': 4}, {'port': -1}, {'port': 65536},
                     {'fallbackData': {'dynamic': {'clients': -1}}},
                     {'fallbackData': {'dynamic': {'svMaxclients': 1 << 64}}}):
            with self.subTest(body=body):
                status, _ = self.request('POST', '/api/serverlist/ingress', json.dumps(body))
                self.assertEqual(status, 400)

    def test_chunked_body_rejected(self):
        status, _ = self.request('POST', '/api/serverlist/ingress',
                                 headers={'Transfer-Encoding': 'chunked'})
        self.assertEqual(status, 400)

    def test_malformed_json(self):
        status, _ = self.request('POST', '/api/serverlist/ingress', '{')
        self.assertEqual(status, 400)

    def test_oversized_body(self):
        status, _ = self.request('POST', '/api/serverlist/ingress',
                                 headers={'Content-Length': str(m.MAX_HEARTBEAT_BYTES + 1)})
        self.assertEqual(status, 413)

    def test_valid_heartbeat_stream(self):
        body = {'port': 30120, 'fallbackData': {'dynamic': {'clients': 1}}}
        status, _ = self.request('POST', '/api/serverlist/ingress', json.dumps(body))
        self.assertEqual(status, 200)
        status, stream = self.request('GET', '/api/servers/streamRedir/')
        self.assertEqual(status, 200)
        self.assertTrue(stream)


if __name__ == '__main__':
    unittest.main()
