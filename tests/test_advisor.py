from pathlib import Path
import time
import unittest

from space_sheriff.advisor import advise


class AdvisorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.now = time.time()

    def test_system_file_is_protected(self):
        advice = advise(Path("/Windows/System32/large.dll"), 20_000, self.now, self.now)
        self.assertEqual(advice.level, "danger")

    def test_old_cache_is_usually_safe(self):
        modified = self.now - 40 * 86400
        advice = advise(Path("/Users/me/Library/Caches/app/data.bin"), 20_000, modified, self.now)
        self.assertEqual(advice.level, "safe")
        self.assertIn("40", advice.reason)

    def test_recent_cache_requires_review(self):
        modified = self.now - 2 * 86400
        advice = advise(Path("/Users/me/tmp/data.bin"), 20_000, modified, self.now)
        self.assertEqual(advice.level, "review")

    def test_personal_document_requires_review(self):
        modified = self.now - 500 * 86400
        advice = advise(Path("/Users/me/Documents/report.pdf"), 20_000, modified, self.now)
        self.assertEqual(advice.level, "review")

    def test_old_installer_is_usually_safe(self):
        modified = self.now - 60 * 86400
        advice = advise(Path("/Users/me/setup.dmg"), 20_000, modified, self.now)
        self.assertEqual(advice.level, "safe")


if __name__ == "__main__":
    unittest.main()
