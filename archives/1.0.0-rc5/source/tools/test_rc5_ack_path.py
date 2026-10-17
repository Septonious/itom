import json
import tempfile
import unittest
from pathlib import Path
from rc5_ack_path import correlate


class TestCorrelator(unittest.TestCase):
    def test_authenticated_counter_matches_both_directions(self):
        with tempfile.TemporaryDirectory() as tmp:
            client, server = Path(tmp) / 'client', Path(tmp) / 'server'
            client.mkdir()
            server.mkdir()

            def ev(stamp, kind, side, count, name='WINDOW'):
                return json.dumps({'time': stamp, 'event': kind, 'side': side,
                    'account': 'a01', 'user': 'u', 'session': 'abc',
                    'counter': count, 'name': name}) + '\n'

            (client / 'transport-trace.jsonl').write_text(
                json.dumps({'time':'2026-10-17T00:00:00.001Z',
                    'event':'control_wait','side':'client','account':'a01',
                    'user':'u','counter':10,'name':'WINDOW','duration_us':125000}) + '\\n'
                + ev('2026-10-17T00:00:00Z', 'frame_sealed', 'client', 10)
                + ev('2026-10-17T00:00:02Z', 'frame_rx', 'client', 11))
            (server / 'transport-trace.jsonl').write_text(
                ev('2026-10-17T00:00:00.200Z', 'frame_rx', 'server', 10)
                + ev('2026-10-17T00:00:01.500Z', 'frame_sealed', 'server', 11))
            result = correlate(client, server)
            self.assertEqual(result['paired_controls'], 2)
            groups = {(row['sender'], row['frame']): row for row in result['groups']}
            self.assertEqual(groups['client', 'WINDOW']['median_ms'], 200)
            self.assertEqual(groups['client', 'WINDOW']['sampled_preseal_count'], 1)
            self.assertEqual(groups['client', 'WINDOW']['sampled_queue_p50_ms'], 125)
            self.assertEqual(groups['client', 'WINDOW']['sampled_queue_plus_wire_p95_ms'], 325)
            self.assertEqual(groups['server', 'WINDOW']['median_ms'], 500)
            self.assertEqual(result['negative_cross_host_intervals'], 0)


if __name__ == '__main__':
    unittest.main()
