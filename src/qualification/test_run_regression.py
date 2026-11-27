import os
from pathlib import Path
import re
import sys
import tempfile
import unittest

from run_regression import batches, execute, selector


class RegressionRunnerTest(unittest.TestCase):
    def test_batches_cover_every_discovered_name_once(self):
        names = ["TestA", "TestAB", "ExampleOne", "FuzzTwo", "TestC"]
        groups = list(batches(names, 2))
        self.assertEqual([name for group in groups for name in group], names)
        self.assertEqual([len(group) for group in groups], [2, 2, 1])
        for group in groups:
            self.assertEqual([name for name in names if re.fullmatch(selector(group), name)], group)
        self.assertFalse(re.fullmatch(selector([]), "TestA"))
        with self.assertRaises(ValueError):
            list(batches(names, 0))

    def test_exit_code_and_timeout_are_not_passes(self):
        with tempfile.TemporaryDirectory() as directory:
            cwd = Path(directory)
            env = dict(os.environ)
            code, text, _ = execute([sys.executable, "-c", "print('failure'); raise SystemExit(7)"],
                                    cwd, env, cwd / "failed.log", 5)
            self.assertEqual(code, 7)
            self.assertIn("failure", text)
            code, text, _ = execute([sys.executable, "-c", "import time; time.sleep(5)"],
                                    cwd, env, cwd / "timeout.log", 0.1)
            self.assertEqual(code, 124)
            self.assertIn("External timeout", text)


if __name__ == "__main__":
    unittest.main()
