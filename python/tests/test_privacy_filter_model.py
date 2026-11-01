import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MODEL_DIR = ROOT / '.pi/m0-resources/privacy-filter-int8'
MANIFEST = ROOT / 'resources/models/privacy-filter-int8.json'


@unittest.skipUnless((MODEL_DIR / 'model_quantized.onnx_data').exists(), 'privacy-filter model resource not downloaded')
class RealPrivacyFilterTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        from tacet_detect_privacy_filter.detector import Detector
        cls.manifest = json.loads(MANIFEST.read_text())
        cls.detector = Detector(MODEL_DIR, cls.manifest)

    def spans(self, text, floor=0.0):
        return [(s.label, text[s.start:s.end]) for s in self.detector.detect(text, floor=floor)]

    def test_english_card_example(self):
        self.assertEqual(self.spans('Contact Jane Doe at jane.doe@example.org or +1 415-555-0132, 221B Baker Street, London.'),
                         [('private_person', 'Jane Doe'), ('private_email', 'jane.doe@example.org'),
                          ('private_phone', '+1 415-555-0132'), ('private_address', '221B Baker Street, London')])

    def test_chinese_spans_are_whole_and_trimmed(self):
        found = self.spans('请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。王芳的生日是 1992 年 7 月 8 日。')
        self.assertIn(('private_person', '张伟'), found)
        self.assertIn(('private_phone', '13812345678'), found)
        self.assertIn(('private_email', 'zhangwei@example.com'), found)
        self.assertIn(('private_date', '1992 年 7 月 8 日'), found)  # argmax splits it; Viterbi keeps it whole

    def test_astral_characters_and_control_token_text(self):
        text = '😀😀 <|endoftext|> 邮箱 jane.doe@example.org'
        self.assertIn(('private_email', 'jane.doe@example.org'), self.spans(text))

    def test_windows_cover_the_whole_text(self):
        text = 'public sample text. ' * 2500 + 'Call Jane Doe at +1 415-555-0132.'
        self.assertGreater(len(self.detector._tokenizer.encode(text, add_special_tokens=False).ids), 4096 * 2)
        self.assertIn(('private_phone', '+1 415-555-0132'), self.spans(text))

    def test_floor_drops_weak_spans_only(self):
        text = '王芳的生日是 1992 年 7 月 8 日，电话 13812345678。'
        weak = {(s.label, text[s.start:s.end]): s.score for s in self.detector.detect(text, floor=0.0)}
        strong = self.spans(text, floor=0.9)
        for key, score in weak.items():
            self.assertEqual(key in strong, score >= 0.9)


if __name__ == '__main__':
    unittest.main()
