import json
import tempfile
import unittest
from pathlib import Path
from mime_efficiency import measure, report

class MIMEEfficiencyTests(unittest.TestCase):
    def test_ratio_uses_only_verified_bytes_and_provider_append_body(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            for i in (1,2):
                folder=root/str(i);folder.mkdir()
                for name,overhead in (("rc2",13680),("candidate",12550)):
                    data={"Provider":{"appended_body_bytes":overhead},
                          "Phases":[{"name":"upload","upload":{"verified_bytes":6000},"download":{"verified_bytes":0}},
                                    {"name":"download","download":{"verified_bytes":4000},"upload":{"verified_bytes":0}}]}
                    (folder/(name+".json")).write_text(json.dumps(data))
            self.assertEqual(measure(root/"1"/"candidate.json")["mime_bytes_per_verified_app_byte"],1.255)
            data=report([root/"1",root/"2"])
            self.assertGreater(data["mime_byte_reduction_percent"],8)

    def test_missing_provider_counter_is_an_error(self):
        with tempfile.TemporaryDirectory() as temp:
            p=Path(temp)/"result.json"
            p.write_text(json.dumps({"Provider":{},"Phases":[]}))
            with self.assertRaisesRegex(ValueError,"counter is absent"):
                measure(p)

if __name__=="__main__":
    unittest.main()
