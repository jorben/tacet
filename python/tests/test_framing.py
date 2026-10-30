import io
import struct
import unittest

from tacet_plugin_sdk import framing


class FramingTest(unittest.TestCase):
    def test_round_trip(self):
        msg = {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"text": "中文 ✓"}}
        stream = io.BytesIO(framing.encode(msg) + framing.encode({"id": 2}))
        self.assertEqual(framing.read(stream), msg)
        self.assertEqual(framing.read(stream), {"id": 2})
        self.assertIsNone(framing.read(stream))

    def test_rejects_oversized_length_before_reading_body(self):
        stream = io.BytesIO(struct.pack(">I", framing.MAX_MESSAGE_BYTES + 1))
        with self.assertRaises(framing.FrameError):
            framing.read(stream)

    def test_rejects_truncated_frame(self):
        frame = framing.encode({"id": 1})
        with self.assertRaises(framing.FrameError):
            framing.read(io.BytesIO(frame[:-1]))

    def test_rejects_non_json_body(self):
        with self.assertRaises(framing.FrameError):
            framing.read(io.BytesIO(struct.pack(">I", 3) + b"\xff\xfe!"))

    def test_encode_refuses_oversized_message(self):
        with self.assertRaises(framing.FrameError):
            framing.encode("x" * framing.MAX_MESSAGE_BYTES)


if __name__ == "__main__":
    unittest.main()
