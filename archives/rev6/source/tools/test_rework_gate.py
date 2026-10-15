import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import rework_gate


class EvidenceTests(unittest.TestCase):
    def summary(self, events, required=('TestRequired',)):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'go.jsonl'
            p.write_text('\n'.join(json.dumps(e) for e in events)+'\n')
            return rework_gate.summarize_events(p, required)

    def test_zero_tests_and_skip_are_not_success(self):
        for events in [[], [{'Action':'pass','Package':'pkg'}], [{'Action':'skip','Test':'TestRequired'}]]:
            self.assertEqual(['TestRequired'], self.summary(events)['missing_required'])

    def test_unmatched_selection_is_not_a_substitute(self):
        d=self.summary([{'Action':'pass','Test':'TestSomethingElse'}])
        self.assertEqual(['TestRequired'],d['missing_required'])

    def test_later_pass_does_not_erase_earlier_failure(self):
        d=self.summary([{'Action':'fail','Test':'TestRequired'}, {'Action':'pass','Test':'TestRequired'}])
        self.assertEqual(['TestRequired'],d['failed'])
        self.assertEqual([],d['missing_required'])

    def test_step_rejects_skipped_required_test_even_with_exit_zero(self):
        with tempfile.TemporaryDirectory() as d:
            out=Path(d)
            def fake(command,cwd,env,stdout,stderr):
                stdout.write(json.dumps({'Action':'skip','Test':'TestRequired'})+'\n')
                return type('Result',(),{'returncode':0})()
            with patch.object(rework_gate.subprocess,'run',side_effect=fake):
                result=rework_gate.run_step('gate',['go','test'],rework_gate.ROOT,{},out,required=('TestRequired',),tests=True)
            self.assertFalse(result['ok'])
            self.assertEqual(0,result['exit_code'])


if __name__=='__main__':
    unittest.main()
