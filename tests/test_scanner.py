from pathlib import Path
import tempfile
import unittest

from space_sheriff.scanner import scan_largest


class ScannerTests(unittest.TestCase):
    def test_filters_and_sorts_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "small.bin").write_bytes(b"x" * 3)
            (root / "large.bin").write_bytes(b"x" * 20)
            (root / "medium.bin").write_bytes(b"x" * 10)
            records, stats = scan_largest(root, minimum_size=5, limit=10)
        self.assertEqual([item.size for item in records], [20, 10])
        self.assertEqual(stats.files_seen, 3)

    def test_honors_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for index in range(5):
                (root / f"{index}.bin").write_bytes(b"x" * (index + 1))
            records, _ = scan_largest(root, minimum_size=0, limit=2)
        self.assertEqual([item.size for item in records], [5, 4])

    def test_can_cancel(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "file.bin").write_bytes(b"x")
            records, stats = scan_largest(root, 0, 10, cancelled=lambda: True)
        self.assertEqual(records, [])
        self.assertEqual(stats.files_seen, 0)


if __name__ == "__main__":
    unittest.main()
